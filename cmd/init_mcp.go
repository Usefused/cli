package cmd

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

type mcpInitFlags struct{ tools, prompts, resources, templates, all, revisions []string }

// present separates unsupported imported intent from ordinary endpoint-only commands.
func (flags mcpInitFlags) present() bool {
	return len(flags.tools)+len(flags.prompts)+len(flags.resources)+len(flags.templates)+len(flags.all)+len(flags.revisions) > 0
}

// addMCPInitFlags shares exact, comma-preserving capability syntax between creation and extension.
func addMCPInitFlags(command *cobra.Command, flags *mcpInitFlags) {
	command.Flags().StringArrayVar(&flags.tools, "mcp-tool", nil, "Imported tool as <service>=<name>; repeatable, MCP apps only")
	command.Flags().StringArrayVar(&flags.prompts, "mcp-prompt", nil, "Imported prompt as <service>=<name>; repeatable, MCP apps only")
	command.Flags().StringArrayVar(&flags.resources, "mcp-resource", nil, "Imported resource as <service>=<URI>; repeatable, MCP apps only")
	command.Flags().StringArrayVar(&flags.templates, "mcp-resource-template", nil, "Imported template as <service>=<URI-template>; repeatable, MCP apps only")
	command.Flags().StringArrayVar(&flags.all, "mcp-all", nil, "Select all currently imported capabilities for a service; repeatable")
	command.Flags().StringArrayVar(&flags.revisions, "mcp-revision", nil, "Require reviewed catalog <service>=<revision-id>; explicitly approves a changed pin on extend")
}

// parseMCPInitFlags preserves provider identity after the first equals sign, including commas and template expressions.
func parseMCPInitFlags(flags mcpInitFlags) (map[string]mcpSelectOptions, error) {
	requests := map[string]mcpSelectOptions{}
	for _, group := range []struct {
		kind   string
		values []string
	}{{"tools", flags.tools}, {"prompts", flags.prompts}, {"resources", flags.resources}, {"resource_templates", flags.templates}, {"revision", flags.revisions}} {
		for _, raw := range group.values {
			service, value, ok := strings.Cut(raw, "=")
			// Empty selectors cannot safely inherit a service or capability identity.
			if !ok || strings.TrimSpace(service) == "" || value == "" {
				return nil, fmt.Errorf("MCP selectors require <service>=<exact-identity>")
			}
			service = strings.TrimSpace(service)
			request := requests[service]
			// Each flag contributes to exactly one protocol namespace.
			if err := appendMCPInitValue(&request, group.kind, value); err != nil {
				return nil, err
			}
			requests[service] = request
		}
	}
	for _, service := range flags.all {
		service = strings.TrimSpace(service)
		// An all-items snapshot still requires an explicit service boundary.
		if service == "" {
			return nil, fmt.Errorf("--mcp-all requires a service")
		}
		request := requests[service]
		request.all = true
		requests[service] = request
	}
	return requests, nil
}

// appendMCPInitValue keeps repeated revision guards unambiguous while retaining exact capability flag values.
func appendMCPInitValue(request *mcpSelectOptions, kind, value string) error {
	// Protocol categories remain independent even when provider names coincide.
	switch kind {
	case "tools":
		request.selection.Tools = append(request.selection.Tools, value)
	case "prompts":
		request.selection.Prompts = append(request.selection.Prompts, value)
	case "resources":
		request.selection.Resources = append(request.selection.Resources, value)
	case "resource_templates":
		request.selection.ResourceTemplates = append(request.selection.ResourceTemplates, value)
	case "revision":
		// Conflicting guards indicate two different reviewed snapshots and must never use last-wins semantics.
		if request.revision != "" && request.revision != value {
			return fmt.Errorf("conflicting --mcp-revision values")
		}
		request.revision = value
	}
	return nil
}

// sortedMCPRequestServices makes service resolution and review deterministic across map iteration order.
func sortedMCPRequestServices(requests map[string]mcpSelectOptions) []string {
	names := make([]string, 0, len(requests))
	for name := range requests {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// canonicalInitMCPRequests uses the same resolved aliases as physical operations and rejects undeclared scope.
func canonicalInitMCPRequests(requests map[string]mcpSelectOptions, services []sdkInitResolvedService) (map[string]mcpSelectOptions, error) {
	aliases := map[string]string{}
	for _, service := range services {
		aliases[service.target.slug] = service.target.slug
		for _, alias := range service.target.requestedRefs {
			aliases[alias] = service.target.slug
		}
	}
	result := map[string]mcpSelectOptions{}
	for _, name := range sortedMCPRequestServices(requests) {
		canonical, ok := aliases[name]
		// Capability flags never add an undeclared provider to an app.
		if !ok {
			return nil, fmt.Errorf("MCP service %q is not declared; add --service %s@<version>", name, name)
		}
		// Two aliases cannot hide competing all-items or revision intents for one service.
		if _, exists := result[canonical]; exists {
			return nil, fmt.Errorf("use one canonical service reference for MCP selections: %s", canonical)
		}
		result[canonical] = requests[name]
	}
	return result, nil
}

// completeInitCapabilitySelections combines imported and physical selectors before any workspace mutation.
func completeInitCapabilitySelections(cmd *cobra.Command, client *api.Client, request scaffoldRequest, services []sdkInitResolvedService) (scaffoldRequest, error) {
	request, err := completeInitMCPCapabilities(cmd, client, request, services)
	// An invalid or cancelled imported selection must not fall through to physical discovery.
	if err != nil {
		return request, err
	}
	return completeSDKInitOperationSelections(cmd, client, request, services)
}

// completeInitMCPCapabilities resolves approved catalogs before the existing endpoint selection and lifecycle gates.
func completeInitMCPCapabilities(cmd *cobra.Command, client *api.Client, request scaffoldRequest, services []sdkInitResolvedService) (scaffoldRequest, error) {
	// Non-MCP adapters retain their existing discovery behavior and cannot accept imported grants.
	if request.kind != configfile.KindMCP {
		// Internal callers must obey the same restriction as public command flags.
		if len(request.mcpRequests) > 0 {
			return request, fmt.Errorf("imported capabilities require an MCP app")
		}
		return request, nil
	}
	requests, err := canonicalInitMCPRequests(request.mcpRequests, services)
	// Alias failures precede catalog traffic and any workspace plan.
	if err != nil {
		return request, err
	}
	prior, err := priorInitMCPServices(request)
	// Existing desired state must remain readable before additive selection proceeds.
	if err != nil {
		return request, err
	}
	request.mcpSelections = map[string]*configfile.ImportedMCPSelection{}
	for _, service := range services {
		options, explicit := requests[service.target.slug]
		selection, err := completeInitMCPService(cmd, client, &request, service, options, explicit, prior[service.target.slug].MCP)
		// One invalid service must stop the composed lifecycle before any plan or apply.
		if err != nil {
			return request, fmt.Errorf("MCP selection for %s: %w", service.target.slug, err)
		}
		// Endpoint-only selections retain the established physical configuration fields.
		if selection != nil {
			request.mcpSelections[service.target.slug] = selection
		}
	}
	return request, nil
}

// completeInitMCPService resolves one catalog boundary without granting capabilities for unrelated explicit endpoint intent.
func completeInitMCPService(cmd *cobra.Command, client *api.Client, request *scaffoldRequest, service sdkInitResolvedService, options mcpSelectOptions, explicit bool, prior *configfile.ImportedMCPSelection) (*configfile.ImportedMCPSelection, error) {
	// Automation and explicit physical/event scope must never imply imported grants.
	if !explicit && !shouldPromptInitMCP(*request, service) {
		return nil, nil
	}
	snapshot, err := readInitMCPCatalog(client, service)
	// Permission and transport errors are authoritative, not an empty-catalog fallback.
	if err != nil {
		return nil, err
	}
	// Services without imported capabilities retain the familiar endpoint selector.
	if !explicit && !hasInitMCPCapabilities(snapshot) {
		return nil, nil
	}
	// Interactive users can choose physical and imported capabilities in one searchable list.
	if !explicit {
		options, err = chooseInitMCPCapabilities(cmd, client, request, service, snapshot)
		// Cancellation cannot publish partial desired state.
		if err != nil {
			return nil, err
		}
		// A physical-only choice must not introduce an empty imported selection.
		if !options.all && importedSelectionCount(&options.selection) == 0 {
			return nil, nil
		}
	}
	return resolveInitMCPSelection(snapshot, options, prior)
}

// hasInitMCPCapabilities avoids offering an all-items grant for a missing or empty saved catalog.
func hasInitMCPCapabilities(snapshot *api.MCPCatalogSnapshot) bool {
	// Missing catalogs leave endpoint discovery unchanged.
	if snapshot == nil {
		return false
	}
	for _, group := range mcpCatalogGroups(&snapshot.Catalog) {
		// One populated protocol namespace is enough to offer the combined selector.
		if len(*group.items) > 0 {
			return true
		}
	}
	return false
}

// shouldPromptInitMCP offers imported capabilities only when the user has not already supplied an explicit service boundary.
func shouldPromptInitMCP(request scaffoldRequest, service sdkInitResolvedService) bool {
	return !nonInteractive() && !sdkInitServiceHasOperationSelection(request, service.target.slug) && !sdkInitServiceHasEventSelection(request, service.target.slug)
}

// readInitMCPCatalog avoids querying inactive services, which cannot yet have an approved version-scoped import.
func readInitMCPCatalog(client *api.Client, service sdkInitResolvedService) (*api.MCPCatalogSnapshot, error) {
	// A newly enabled Registry version must first be imported through the owner-reviewed catalog workflow.
	if !containsString(service.target.enabledVersions, service.version) {
		return nil, nil
	}
	id, versionID, _, err := resolveMCPCatalogVersion(client, service.target.serviceID, service.version)
	// Workspace permission and exact identity checks remain shared with catalog commands.
	if err != nil {
		return nil, err
	}
	return client.GetMCPCatalog(id, versionID)
}

// priorInitMCPServices supplies the authored selection to additive extension without floating to a deployed latest version.
func priorInitMCPServices(request scaffoldRequest) (map[string]configfile.AppService, error) {
	// New apps have no scope to preserve.
	if !request.extend {
		return nil, nil
	}
	parsed, err := configfile.ParseFile(request.path)
	// A damaged existing declaration cannot become a smaller replacement app.
	if err != nil {
		return nil, err
	}
	// The same immutable kind boundary applies when called without public command validation.
	if parsed.MCP == nil {
		return nil, fmt.Errorf("MCP extension requires kind: mcp")
	}
	return parsed.MCP.Services, nil
}

// resolveInitMCPSelection merges only reviewed capabilities and requires explicit consent to move an existing catalog pin.
func resolveInitMCPSelection(snapshot *api.MCPCatalogSnapshot, options mcpSelectOptions, prior *configfile.ImportedMCPSelection) (*configfile.ImportedMCPSelection, error) {
	selected, err := selectMCPCatalog(snapshot, options)
	// Validate the new request independently so duplicate flags cannot be hidden by union logic.
	if err != nil {
		return nil, err
	}
	// A first imported selection has no earlier capabilities to retain.
	if prior == nil {
		return selected, nil
	}
	// Refreshing the service must never implicitly change definitions in an app extension.
	if prior.RevisionID != selected.RevisionID && options.revision != selected.RevisionID {
		return nil, fmt.Errorf("catalog revision changed; review it and pass --mcp-revision <service>=%s", selected.RevisionID)
	}
	selected.Tools = unionMCPNames(prior.Tools, selected.Tools)
	selected.Prompts = unionMCPNames(prior.Prompts, selected.Prompts)
	selected.Resources = unionMCPNames(prior.Resources, selected.Resources)
	selected.ResourceTemplates = unionMCPNames(prior.ResourceTemplates, selected.ResourceTemplates)
	// A deliberate pin change still must retain every prior capability or fail rather than silently remove it.
	return selectMCPCatalog(snapshot, mcpSelectOptions{selection: *selected, revision: selected.RevisionID})
}

// unionMCPNames preserves previous ordering for idempotent source comparison and never mutates the accepted config.
func unionMCPNames(prior, added []string) []string {
	result := append([]string(nil), prior...)
	for _, name := range added {
		// Re-selecting an existing grant is a no-op rather than another immutable scope change.
		if !containsString(result, name) {
			result = append(result, name)
		}
	}
	return result
}

// mergeInitMCPCapabilities updates only the imported selection field after resolution has preserved the prior grants.
func mergeInitMCPCapabilities(config *configfile.AppConfig, selections map[string]*configfile.ImportedMCPSelection) (bool, error) {
	changed := false
	for name, selection := range selections {
		service, exists := config.Services[name]
		// Never synthesize an unversioned service while merging a scoped capability grant.
		if !exists {
			return false, fmt.Errorf("MCP service %q is not declared", name)
		}
		// Exact repeats do not trigger automatic successor version inference.
		if !reflect.DeepEqual(service.MCP, selection) {
			service.MCP = selection
			config.Services[name] = service
			changed = true
		}
	}
	return changed, nil
}

// importedSelectionCount supports concise review while keeping all protocol namespaces independently selectable.
func importedSelectionCount(selection *configfile.ImportedMCPSelection) int {
	// Physical-only choices carry no imported grant.
	if selection == nil {
		return 0
	}
	return len(selection.Tools) + len(selection.Prompts) + len(selection.Resources) + len(selection.ResourceTemplates)
}

// describeInitMCPSelection keeps imported-only and mixed app reviews accurate before lifecycle confirmation.
func describeInitMCPSelection(request scaffoldRequest, physical string) string {
	count := 0
	for _, selection := range request.mcpSelections {
		count += importedSelectionCount(selection)
	}
	// Existing endpoint-only review text stays unchanged.
	if count == 0 {
		return physical
	}
	imported := fmt.Sprintf("%d selected imported MCP capabilities", count)
	// Imported-only apps must not imply an additional physical operation grant.
	if len(request.operations)+len(request.selectAll)+len(request.events) == 0 {
		return imported
	}
	return physical + " and " + imported
}
