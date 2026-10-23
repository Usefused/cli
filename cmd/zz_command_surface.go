package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// relocateCommand gives each operation exactly one public owner; no forwarding alias is retained.
func relocateCommand(parent, child *cobra.Command, use, short string) {
	// Detach before adding because Cobra does not remove a child from its previous parent.
	if old := child.Parent(); old != nil {
		old.RemoveCommand(child)
	}
	child.Use, child.Short, child.Aliases, child.Hidden = use, short, nil, false
	parent.AddCommand(child)
}

// registrySelected keeps catalog scope explicit on the operation that consumes it.
func registrySelected(cmd *cobra.Command) bool {
	selected, _ := cmd.Flags().GetBool("registry")
	return selected
}

// requireWorkspaceService checks membership before a Registry-backed detail read can run by default.
func requireWorkspaceService(cmd *cobra.Command, args []string) error {
	// Explicit catalog inspection may read services that have not been activated.
	if registrySelected(cmd) {
		return nil
	}
	client, err := getAPIClient()
	// Missing credentials must not change the requested scope.
	if err != nil {
		return err
	}
	id, err := resolveServiceIDFromSlug(client, args[0])
	// An unresolved name cannot authorize a broader catalog fallback.
	if err != nil {
		return err
	}
	_, err = workspaceServiceByID(client, id, args[0])
	return err
}

// scopeServiceRead preserves the workspace-first handler while allowing an explicit Registry read.
func scopeServiceRead(command *cobra.Command, registry func(*cobra.Command, []string) error) {
	workspace := command.RunE
	command.Flags().Bool("registry", false, "Read the Registry catalog instead of enabled workspace services")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		// Catalog access is opt-in and failures never silently switch scopes.
		if registrySelected(cmd) {
			return registry(cmd, args)
		}
		return workspace(cmd, args)
	}
}

// configureServiceSurface makes enabled workspace services the default discovery and management scope.
func configureServiceSurface() {
	serviceOperationCmd.Short = "Inspect workspace operations; use --registry for catalog reads"
	serviceCmd.Short = "Manage workspace services; use --registry for catalog reads"
	serviceCmd.RemoveCommand(serviceSearchCmd, serviceVersionsCmd, serviceOperationsCmd, serviceWebhooksCmd)
	workspaceCmd.RemoveCommand(workspaceServicesCmd, workspaceServiceCmd, workspaceHasCmd)
	relocateCommand(serviceCmd, workspaceServicesListCmd, "list", "List enabled services; --registry searches the catalog")
	scopeServiceRead(workspaceServicesListCmd, func(cmd *cobra.Command, args []string) error {
		serviceSearchQuery = workspaceServicesListQuery
		// Registry free-text search needs a query; no unbounded catalog crawl is implied.
		if strings.TrimSpace(serviceSearchQuery) == "" {
			return fmt.Errorf("--search is required with --registry")
		}
		return serviceSearchCmd.RunE(cmd, args)
	})
	relocateCommand(serviceCmd, workspaceHasCmd, "has <service-slug>", "Check whether a service is enabled")
	for _, c := range []*cobra.Command{workspaceServiceAddCmd, workspaceServiceConnectCmd, workspaceServiceDeleteCmd, workspaceServiceDeprecateCmd, workspaceServiceVersionCmd} {
		relocateCommand(serviceCmd, c, c.Use, c.Short)
	}
	workspaceServiceDeleteCmd.Use = "remove <service-slug>"
	workspaceServiceDeleteCmd.Short = "Remove a service from workspace configuration"
	workspaceServiceVersionDeleteCmd.Use = "remove <service-slug> <version>"
	relocateCommand(workspaceServiceVersionCmd, workspaceServiceVersionsCmd, "list <service-slug>", "List enabled service versions; --registry lists catalog versions")
	scopeServiceRead(workspaceServiceVersionsCmd, func(cmd *cobra.Command, args []string) error { return runServiceVersions(cmd, args[0]) })
	relocateCommand(serviceOperationCmd, workspaceServiceOperationsCmd, "list <service-slug>", "List enabled operations; --registry reads catalog operations")
	scopeServiceRead(workspaceServiceOperationsCmd, func(cmd *cobra.Command, args []string) error {
		serviceOperationsQuery, serviceOperationsVersion = workspaceServiceOperationsQuery, workspaceServiceOperationsVersion
		serviceListFlags = workspaceServiceListFlags
		return runServiceOperations(cmd, args[0])
	})
	for _, c := range []*cobra.Command{serviceShowCmd, serviceOperationShowCmd} {
		c.Flags().Bool("registry", false, "Read the Registry catalog instead of enabled workspace services")
		c.PreRunE = requireWorkspaceService
	}
	// Operation details also require the selected version to be enabled in workspace scope.
	serviceOperationShowCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		// Registry inspection is explicitly unconstrained by workspace activation.
		if registrySelected(cmd) {
			return nil
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		} // A missing client cannot authorize a scope change.
		id, err := resolveServiceIDFromSlug(client, args[0])
		if err != nil {
			return err
		} // Exact identity must be resolved first.
		service, err := workspaceServiceByID(client, id, args[0])
		if err != nil {
			return err
		} // Membership failures remain failures.
		_, err = resolveWorkspaceOperationVersion(service, serviceOperationVersion)
		return err
	}
	relocateCommand(serviceCmd, workspaceRefreshMissingContractsCmd, "refresh-missing-contracts", workspaceRefreshMissingContractsCmd.Short)
	// MCP catalogs belong to enabled services, independently of registry webhook definitions.
	for _, c := range workspaceServiceCmd.Commands() {
		if c.Name() == "mcp" {
			relocateCommand(serviceCmd, c, c.Use, c.Short)
		} // Preserve imported MCP workflows.
	}
	definitions := commandGroup("webhook", "Inspect provider webhook definitions")
	relocateCommand(definitions, serviceWebhooksCmd, "list <service-slug>", "List Registry webhook definitions (requires --registry)")
	serviceWebhooksCmd.Flags().Bool("registry", false, "Inspect provider definitions in the Registry")
	serviceWebhooksCmd.PreRunE = requireRegistryMutation
	serviceCmd.AddCommand(definitions)
	// Catalog metadata mutations must never look like changes to local workspace settings.
	for _, c := range serviceCmd.Commands() {
		if c.Name() == "edit" {
			c.Flags().Bool("registry", false, "Edit Registry metadata")
			c.PreRunE = requireRegistryMutation
		}
	}
	serviceConnectionProfileSetCmd.Flags().Bool("registry", false, "Publish the profile to the Registry")
	serviceConnectionProfileSetCmd.PreRunE = requireRegistryMutation
}

// requireRegistryMutation makes catalog-only operations explicit before any network access.
func requireRegistryMutation(cmd *cobra.Command, _ []string) error {
	// A missing scope is an input error rather than permission to mutate a global provider contract.
	if !registrySelected(cmd) {
		return fmt.Errorf("%s requires --registry", cmd.CommandPath())
	}
	return nil
}

// configureSecretSurface keeps provider removal under buckets while connect owns credential setup.
func configureSecretSurface() {
	RootCmd.RemoveCommand(secretCmd)
	bucketCmd.RemoveCommand(bucketSecretsCmd)
	credential := commandGroup("credential", "Manage provider authentication credentials stored in a bucket")
	bucketCmd.AddCommand(credential)
	relocateCommand(credential, secretDeleteCmd, secretDeleteCmd.Use, secretDeleteCmd.Short)
	relocateCommand(bucketSecretCmd, secretListCmd, "list <bucket-name-or-id>", "List all secret and credential metadata in a bucket")
	secretListCmd.Args = cobra.ExactArgs(1)
	secretListCmd.RunE = func(cmd *cobra.Command, args []string) error { secretListBucketID = args[0]; return runSecretList(cmd) }
}

// configureRemainingSurface gives source extraction and registration discovery one public path.
func configureRemainingSurface() {
	source := commandGroup("source", "Manage local Unified App source files")
	unifiedCmd.AddCommand(source)
	relocateCommand(source, unifiedSyncCmd, "extract", "Extract editable TypeScript from local Unified App configs")
	// Only one registration-list command remains; it accepts an ordinary service reference.
	workspaceServiceCmd.RemoveCommand(workspaceServiceWebhooksCmd)
}

// init runs after resource registration and assembles the single canonical public command tree.
func init() {
	configureServiceSurface()
	configureSecretSurface()
	configureRemainingSurface()
	configureAppSurface()
}
