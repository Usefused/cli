package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
	"strings"
	"text/tabwriter"
	"unicode"
)

type workspaceMCPOptions struct{ version, url, bucket, secret, draft, kind, query string }

// newWorkspaceMCPCatalogCommand keeps each invocation's flags isolated and delegates all import authority to Engine.
func newWorkspaceMCPCatalogCommand(action string) *cobra.Command {
	options := &workspaceMCPOptions{}
	command := &cobra.Command{Use: action + " <service-slug-or-id>", Short: map[string]string{"show": "Show the service's saved MCP catalog", "discover": "Preview an MCP import, refresh, or URL change", "apply": "Import an exact reviewed MCP draft"}[action], Args: cobra.ExactArgs(1)}
	// Telemetry records the action only; URLs, queries, and provider definitions remain request data.
	command.RunE = WithTelemetry("cli.workspace.service.mcp."+action, func(cmd *cobra.Command, args []string) error {
		return runWorkspaceMCPCatalog(cmd, args[0], action, *options)
	})
	command.Flags().StringVar(&options.version, "version", "", "Exact enabled service version (required)")
	_ = command.MarkFlagRequired("version")
	// Discovery accepts bucket references only, never a provider token on the command line.
	if action == "discover" {
		command.Flags().StringVar(&options.url, "url", "", "Public HTTPS MCP URL (required)")
		command.Flags().StringVar(&options.bucket, "bucket", "", "Existing bucket used for bearer authentication")
		command.Flags().StringVar(&options.secret, "secret", "", "Existing generic bucket secret name")
		_ = command.MarkFlagRequired("url")
		command.MarkFlagsRequiredTogether("bucket", "secret")
	}
	// Apply is a separate review boundary and cannot accept definitions or a replacement URL.
	if action == "apply" {
		command.Flags().StringVar(&options.draft, "draft-id", "", "Exact reviewed preview ID (required)")
		_ = command.MarkFlagRequired("draft-id")
	}
	// Filtering changes only the display of saved definitions, never app authority.
	if action == "show" {
		command.Flags().StringVar(&options.kind, "type", "", "Filter: tools, prompts, resources, resource_templates")
		command.Flags().StringVar(&options.query, "query", "", "Search names, descriptions, or resource identities")
	}
	addJSONOutputFlag(command)
	return command
}

// resolveMCPCatalogVersion requires explicit active-version identity instead of silently floating to the newest catalog.
func resolveMCPCatalogVersion(client *api.Client, ref, version string) (string, string, string, error) {
	// Exact version selection is mandatory for both catalog mutations and selection generation.
	if strings.TrimSpace(version) == "" {
		return "", "", "", fmt.Errorf("--version is required")
	}
	serviceID, err := resolveServiceIDFromSlug(client, ref)
	// Ambiguous service references must fail before any catalog access.
	if err != nil {
		return "", "", "", err
	}
	service, err := workspaceServiceByID(client, serviceID, ref)
	// Registry visibility alone does not grant Engine workspace membership.
	if err != nil {
		return "", "", "", err
	}
	for _, enabled := range service.EnabledVersions {
		// Match the authored version exactly and preserve the immutable version UUID.
		if enabled.Version == version {
			return serviceID, enabled.ServiceVersionID, service.ServiceSlug, nil
		}
	}
	return "", "", "", fmt.Errorf("service %s version %s is not enabled in this workspace", ref, version)
}

// runWorkspaceMCPCatalog performs one bounded action; failed discovery never replaces the saved snapshot.
func runWorkspaceMCPCatalog(cmd *cobra.Command, ref, action string, options workspaceMCPOptions) error {
	// Validate local filters before network access so misspellings cannot look like empty catalogs.
	if err := validateMCPCatalogType(options.kind); err != nil {
		return err
	}
	client, err := getAPIClient()
	// Missing Engine credentials cannot trigger provider discovery.
	if err != nil {
		return err
	}
	serviceID, versionID, _, err := resolveMCPCatalogVersion(client, ref, options.version)
	// Exact service identity is shared by all three catalog operations.
	if err != nil {
		return err
	}
	// The closed action set prevents arbitrary control-plane requests.
	switch action {
	case "discover":
		draft, err := client.DiscoverMCPCatalog(serviceID, versionID, api.MCPDiscoveryInput{URL: options.url, BucketName: options.bucket, SecretName: options.secret})
		// Failed previews provide no draft to approve.
		if err != nil {
			return err
		}
		return printMCPDraft(cmd, draft)
	case "apply":
		snapshot, err := client.ApplyMCPCatalog(serviceID, versionID, options.draft)
		// Conflicts and authorization failures must not be reported as imported.
		if err != nil {
			return err
		}
		return printMCPCatalog(cmd, snapshot, "", "")
	default:
		snapshot, err := client.GetMCPCatalog(serviceID, versionID)
		// Storage failures remain distinguishable from an empty service.
		if err != nil {
			return err
		}
		return printMCPCatalog(cmd, snapshot, options.kind, options.query)
	}
}

// printMCPDraft presents the exact review identity and changes without promoting the preview.
func printMCPDraft(cmd *cobra.Command, draft *api.MCPCatalogDraft) error {
	// JSON contains original definitions so automated reviewers can inspect complete schemas.
	if wantsJSON(cmd) {
		return writeJSON(cmd, draft)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Preview %s (expires %s)\nAdded: %d  Changed: %d  Removed: %d\nReview with --json for full definitions; apply with --draft-id %s.\n", draft.ID, draft.ExpiresAt, draft.Changes.Added, draft.Changes.Changed, draft.Changes.Removed, draft.ID)
	return printMCPCatalog(cmd, &draft.MCPCatalogSnapshot, "", "")
}

// printMCPCatalog exposes service-scoped definitions without inventing a separate MCP server display name.
func printMCPCatalog(cmd *cobra.Command, snapshot *api.MCPCatalogSnapshot, kind, query string) error {
	// No saved snapshot is a legitimate pre-import state.
	if snapshot == nil {
		// Machine callers need a typed absence rather than prose.
		if wantsJSON(cmd) {
			return writeJSON(cmd, map[string]any{"catalog": nil})
		}
		fmt.Fprintln(cmd.OutOrStdout(), "No MCP catalog imported for this service version.")
		return nil
	}
	filtered := filterMCPCatalog(*snapshot, kind, query)
	// Full raw definitions, including schemas, survive structured filtering.
	if wantsJSON(cmd) {
		return writeJSON(cmd, filtered)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Revision: %s\nURL: %s\n", snapshot.ID, snapshot.URL)
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(writer, "TYPE\tIDENTITY\tDESCRIPTION")
	for _, group := range mcpCatalogGroups(&filtered.Catalog) {
		for _, raw := range *group.items {
			item := readMCPCatalogItem(raw)
			fmt.Fprintf(writer, "%s\t%s\t%s\n", group.kind, mcpTerminalText(item.identity(group.kind)), mcpTerminalText(item.Description))
		}
	}
	return writer.Flush()
}

type mcpCatalogGroup struct {
	kind  string
	items *[]json.RawMessage
}
type mcpCatalogItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URI         string `json:"uri"`
	URITemplate string `json:"uriTemplate"`
}

// mcpCatalogGroups keeps UI-equivalent category order deterministic for output and explicit all-item selection.
func mcpCatalogGroups(catalog *api.MCPCatalog) []mcpCatalogGroup {
	return []mcpCatalogGroup{{"tools", &catalog.Tools}, {"prompts", &catalog.Prompts}, {"resources", &catalog.Resources}, {"resource_templates", &catalog.ResourceTemplates}}
}

// readMCPCatalogItem reads display fields without changing the original schema-bearing definition.
func readMCPCatalogItem(raw json.RawMessage) mcpCatalogItem {
	var item mcpCatalogItem
	_ = json.Unmarshal(raw, &item)
	return item
}

// identity matches the Engine's protocol-specific membership key rather than a provider display title.
func (item mcpCatalogItem) identity(kind string) string {
	// Resource names are labels; authority is attached to their URI or template.
	switch kind {
	case "resources":
		return item.URI
	case "resource_templates":
		return item.URITemplate
	default:
		return item.Name
	}
}

// validateMCPCatalogType makes unsupported types explicit instead of silently hiding every capability.
func validateMCPCatalogType(kind string) error {
	// An empty filter intentionally includes all protocol categories.
	switch kind {
	case "", "tools", "prompts", "resources", "resource_templates":
		return nil
	default:
		return fmt.Errorf("type must be tools, prompts, resources, or resource_templates")
	}
}

// filterMCPCatalog copies the snapshot before narrowing its visible definitions, preserving saved authority.
func filterMCPCatalog(snapshot api.MCPCatalogSnapshot, kind, query string) api.MCPCatalogSnapshot {
	query = strings.ToLower(strings.TrimSpace(query))
	for _, group := range mcpCatalogGroups(&snapshot.Catalog) {
		matched := []json.RawMessage{}
		for _, raw := range *group.items {
			item := readMCPCatalogItem(raw)
			// Type and text filters intersect; neither changes revision identity.
			if (kind == "" || kind == group.kind) && strings.Contains(strings.ToLower(item.Name+" "+item.Description+" "+item.identity(group.kind)), query) {
				matched = append(matched, raw)
			}
		}
		*group.items = matched
	}
	return snapshot
}

// mcpTerminalText prevents untrusted provider descriptions from injecting terminal controls or extra table rows.
func mcpTerminalText(value string) string {
	return strings.Map(func(r rune) rune {
		// JSON output retains exact values; human output strips control sequences at the rendering boundary.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, value)
}

// init registers service catalog management separately from hosted MCP app deployment.
func init() {
	command := &cobra.Command{Use: "mcp", Short: "Inspect and import a service's MCP catalog", Args: cobra.NoArgs, RunE: requireSubcommand}
	command.AddCommand(newWorkspaceMCPCatalogCommand("show"), newWorkspaceMCPCatalogCommand("discover"), newWorkspaceMCPCatalogCommand("apply"))
	workspaceServiceCmd.AddCommand(command)
}
