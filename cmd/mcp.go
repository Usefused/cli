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
var mcpPlanJSON bool
var mcpPlanReceiptOut string
var mcpPlanOwnerTeam string
var mcpApplyPlanID string
var mcpApplyReceiptPath string

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List deployed MCP servers",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.mcp.list", func(cmd *cobra.Command, _ []string) error {
		return runMCPList(cmd)
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

// runMCPList renders Engine-owned stable and pinned routes without deriving public URLs locally.
func runMCPList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	page, err := client.ListApps("mcp", mcpListFlags.pageOptions())
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, mcpListFlags)
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
	printPageSummary(cmd.OutOrStdout(), page.Total, mcpListFlags)
	return nil
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

func init() {
	RootCmd.AddCommand(mcpCmd)
	mcpCmd.AddCommand(mcpListCmd, mcpPlanCmd, mcpApplyCmd, mcpValidateCmd, mcpDeactivateCmd)
	addJSONOutputFlag(mcpListCmd, mcpValidateCmd)
	addListFlags(mcpListCmd, &mcpListFlags)
	mcpPlanCmd.Flags().BoolVar(&mcpPlanJSON, "json", false, "Print plan result JSON")
	mcpPlanCmd.Flags().StringVar(&mcpPlanReceiptOut, "receipt-out", "", "Write the plan receipt to this path")
	mcpPlanCmd.Flags().StringVar(&mcpPlanOwnerTeam, "owner-team", "", "Optional owning team slug; defaults to the authenticated person")
	mcpApplyCmd.Flags().StringVar(&mcpApplyPlanID, "plan-id", "", "Apply a specific remote plan ID")
	mcpApplyCmd.Flags().StringVar(&mcpApplyReceiptPath, "receipt", "", "Read a plan receipt from this path")
}
