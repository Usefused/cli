package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Manage deployed MCP servers",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var mcpListFlags listFlags
var mcpVersionsFlags listFlags
var mcpPlanJSON bool
var mcpPlanReceiptOut string
var mcpPlanOwnerTeam string
var mcpApplyPlanID string
var mcpApplyReceiptPath string

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List MCP applications once per unique name",
	Args:  cobra.NoArgs,
	// Keep application discovery separate from immutable version inspection.
	RunE: WithTelemetry("cli.mcp.list", func(cmd *cobra.Command, _ []string) error {
		return runMCPList(cmd)
	}),
}

var mcpVersionsCmd = &cobra.Command{
	Use:   "versions [mcp-name-or-id]",
	Short: "List MCP versions, optionally for one application",
	Args:  cobra.MaximumNArgs(1),
	// An omitted selector preserves discovery of all exact MCP versions.
	RunE: WithTelemetry("cli.mcp.versions", func(cmd *cobra.Command, args []string) error {
		target := ""
		// Resolve an explicitly supplied application rather than filtering names by substring.
		if len(args) == 1 {
			target = strings.TrimSpace(args[0])
		}
		return runMCPVersions(cmd, target)
	}),
}

var mcpOperationsCmd = &cobra.Command{
	Use:   "operations <mcp-name@version-or-version-id>",
	Short: "List every operation allowed by one exact MCP version",
	Args: func(cmd *cobra.Command, args []string) error {
		// Operation scope is immutable-version state, so a family name alone must never float to another version.
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return err
		}
		return validateExactAppReference(args[0], cmd.CommandPath())
	},
	RunE: WithTelemetry("cli.mcp.operations", func(cmd *cobra.Command, args []string) error {
		return runMCPOperations(cmd, args[0])
	}),
}

var mcpPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan MCP server configuration",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.mcp.plan", func(_ *cobra.Command, _ []string) error {
		return runConfigPlan(planOptions{filter: filterMCP, jsonOut: mcpPlanJSON, receiptOut: mcpPlanReceiptOut, ownerTeamSlug: mcpPlanOwnerTeam})
	}),
}

var mcpApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply MCP server configuration",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.mcp.apply", func(cmd *cobra.Command, _ []string) error {
		return runConfigApply(withApplyAudit(cmd, applyOptions{filter: filterMCP, planID: mcpApplyPlanID, receiptPath: mcpApplyReceiptPath}))
	}),
}

var mcpValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate MCP server configuration",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.mcp.validate", func(cmd *cobra.Command, _ []string) error {
		run, err := configfile.LoadRun(effectiveConfigFile())
		if err != nil {
			return err
		}
		count := 0
		for _, config := range run.Configs {
			if config.Kind == configfile.KindMCP {
				count++
			}
		}
		if count == 0 {
			return fmt.Errorf("no mcp configs found")
		}
		if wantsJSON(cmd) {
			return writeJSON(cmd, validationResult("mcp", count))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "validated %d mcp config\n", count)
		return nil
	}),
}

// runMCPList presents one application row without implying that the newest version is promoted.
func runMCPList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	// Authentication configuration must be valid before discovery.
	if err != nil {
		return err
	}
	page, err := client.ListApplications("mcp", mcpListFlags.pageOptions())
	// Never render a partial grouping when any Engine catalogue page failed.
	if err != nil {
		return err
	}
	// Both output formats paginate applications rather than immutable versions.
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, mcpListFlags)
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tMCP_ID\tVERSIONS\tLATEST_VERSION\tSTABLE_VERSION\tDEFAULT_TRANSPORT\tSTREAMABLE HTTP (STABLE, RECOMMENDED)\tSSE (STABLE, LEGACY)")
	for _, app := range page.Items {
		streamableHTTP, sse := "", ""
		// Unpromoted applications remain visible without inventing a usable endpoint.
		if app.TransportURLs != nil {
			streamableHTTP, sse = app.TransportURLs.StreamableHTTP, app.TransportURLs.SSE
		}
		fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n", app.Name, app.AppFamilyID, app.VersionCount, app.LatestVersion, app.StableVersion, app.DefaultTransport, streamableHTTP, sse)
	}
	// Report output errors rather than claiming the application page was delivered.
	if err := writer.Flush(); err != nil {
		return err
	}
	printPageSummary(cmd.OutOrStdout(), page.Total, mcpListFlags)
	return nil
}

// runMCPVersions renders Engine-owned stable and pinned routes for exact immutable versions.
func runMCPVersions(cmd *cobra.Command, target string) error {
	client, err := getAPIClient()
	// Failed reads must not render an incomplete version catalogue.
	if err != nil {
		return err
	}
	page, err := client.ListAppVersions("mcp", target, mcpVersionsFlags.pageOptions())
	// Failed reads must not render an incomplete version catalogue.
	if err != nil {
		return err
	}
	// Preserve exact version fields for automation callers.
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, mcpVersionsFlags)
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tVERSION\tMCP_ID\tVERSION_ID\tSTABLE\tSTABLE_VERSION_ID\tSTATUS\tCREATED\tDEFAULT_TRANSPORT\tSTREAMABLE HTTP (STABLE, RECOMMENDED)\tSTREAMABLE HTTP (VERSION-PINNED)\tSSE (STABLE, LEGACY)\tSSE (VERSION-PINNED, LEGACY)")
	for _, app := range page.Items {
		streamableHTTP, versionedStreamableHTTP, sse, versionedSSE := "", "", "", ""
		// A hard-deactivated promoted version leaves stable endpoints empty until
		// an explicit apply selects the next immutable target.
		if app.TransportURLs != nil {
			streamableHTTP, versionedStreamableHTTP = app.TransportURLs.StreamableHTTP, app.TransportURLs.VersionedStreamableHTTP
			sse, versionedSSE = app.TransportURLs.SSE, app.TransportURLs.VersionedSSE
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%t\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			app.Name, app.Version, app.AppFamilyID, app.AppID, app.Stable, app.StableVersionID, app.Status, app.CreatedAt,
			app.DefaultTransport, streamableHTTP, versionedStreamableHTTP, sse, versionedSSE)
	}
	_ = writer.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, mcpVersionsFlags)
	return nil
}

// runMCPOperations resolves one immutable MCP version and renders its Engine-authoritative callable catalogue.
func runMCPOperations(cmd *cobra.Command, target string) error {
	if err := validateExactAppReference(target, "mcp operations"); err != nil {
		return err
	}
	client, err := getAPIClient()
	// Local client configuration must be valid before attempting exact MCP resolution.
	if err != nil {
		return err
	}
	name, version := parseSDKDownloadName(strings.TrimSpace(target))
	appID, err := client.ResolveMCPAppReference(name, version)
	// Exact kind-scoped resolution prevents an SDK version UUID from crossing into MCP discovery.
	if err != nil {
		return fmt.Errorf("resolve MCP server %q: %w", target, err)
	}
	catalogue, err := client.ListMCPAppOperations(appID)
	// Engine owns select-all expansion and Unified descriptor integrity, so no local fallback is safe.
	if err != nil {
		return fmt.Errorf("list MCP operations: %w", err)
	}
	// The response must remain bound to the exact version resolved immediately before this read.
	if strings.TrimSpace(catalogue.VersionID) != appID {
		return fmt.Errorf("list MCP operations: Engine returned a different MCP version")
	}
	// Structured output retains exact identity and provenance for automation consumers.
	if wantsJSON(cmd) {
		return writeJSON(cmd, catalogue)
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "OPERATION_ID\tKIND")
	// Human output stays compact while still distinguishing direct and Unified invocation names.
	for _, operation := range catalogue.Operations {
		_, _ = fmt.Fprintf(writer, "%s\t%s\n", operation.OperationID, operation.Kind)
	}
	return writer.Flush()
}

var mcpDeactivateCmd = &cobra.Command{
	Use:   "deactivate <mcp-name@version-or-version-id>",
	Short: "Permanently deactivate one exact MCP version",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return err
		}
		return validateExactAppReference(args[0], cmd.CommandPath())
	},
	RunE: WithTelemetry("cli.mcp.deactivate", func(cmd *cobra.Command, args []string) error {
		return runMCPDeactivate(cmd, args[0])
	}),
}

func runMCPDeactivate(cmd *cobra.Command, target string) error {
	if err := validateExactAppReference(target, "mcp deactivate"); err != nil {
		return err
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	// Resolve UUIDs too so the Engine verifies that the target is an MCP server;
	// otherwise a valid SDK UUID could cross the product boundary.
	mcpName, mcpVersion := parseSDKDownloadName(strings.TrimSpace(target))
	id, err := client.ResolveMCPAppReference(mcpName, mcpVersion)
	if err != nil {
		return fmt.Errorf("resolve MCP server %q: %w", target, err)
	}
	if err := client.DeactivateApp(id); err != nil {
		return fmt.Errorf("deactivate MCP server: %w", err)
	}
	recordAppliedChange(cmd.Context(), "mcp.deactivate", "mcp_server")
	fmt.Fprintf(cmd.OutOrStdout(), "Deactivated MCP server %s.\n", target)
	return nil
}

// init registers the MCP lifecycle and exact-version inspection commands.
func init() {
	RootCmd.AddCommand(mcpCmd)
	mcpCmd.AddCommand(mcpListCmd, mcpVersionsCmd, mcpOperationsCmd, mcpPlanCmd, mcpApplyCmd, mcpValidateCmd, mcpDeactivateCmd)
	addJSONOutputFlag(mcpListCmd, mcpVersionsCmd, mcpOperationsCmd, mcpValidateCmd)
	addListFlags(mcpListCmd, &mcpListFlags)
	addListFlags(mcpVersionsCmd, &mcpVersionsFlags)
	mcpPlanCmd.Flags().BoolVar(&mcpPlanJSON, "json", false, "Print plan result JSON")
	mcpPlanCmd.Flags().StringVar(&mcpPlanReceiptOut, "receipt-out", "", "Write the plan receipt to this path")
	mcpPlanCmd.Flags().StringVar(&mcpPlanOwnerTeam, "owner-team", "", "Optional owning team slug; defaults to the authenticated person")
	mcpApplyCmd.Flags().StringVar(&mcpApplyPlanID, "plan-id", "", "Apply a specific remote plan ID")
	mcpApplyCmd.Flags().StringVar(&mcpApplyReceiptPath, "receipt", "", "Read a plan receipt from this path")
}
