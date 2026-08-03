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
	Args:  cobra.ArbitraryArgs,
	RunE: WithTelemetry("cli.mcp", func(cmd *cobra.Command, args []string) error {
		return runMCPDynamicAction(cmd, args)
	}),
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
		fmt.Fprintf(cmd.OutOrStdout(), "validated %d mcp config\n", count)
		return nil
	}),
}

func runMCPDynamicAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if len(args) == 1 && args[0] == "list" {
		return runMCPList(cmd)
	}
	if len(args) == 2 && args[1] == "remove" {
		return runMCPRemove(cmd, args[0])
	}
	return fmt.Errorf("unknown mcp command or target: %v", args)
}

func runMCPList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	// MCP has its own command and response shape because its Engine-hosted URL
	// is runtime state, not generated package metadata.
	page, err := client.ListMCPServers(mcpListFlags.pageOptions())
	if err != nil {
		return err
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tVERSION\tID\tACTIVE\tCREATED\tURL")
	for _, server := range page.Items {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%t\t%s\t%s\n", server.Name, server.Version, server.ID, server.Active, server.CreatedAt, server.MCPURL)
	}
	_ = writer.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, mcpListFlags)
	return nil
}

func runMCPRemove(cmd *cobra.Command, target string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	// Resolve UUIDs too so the Engine verifies that the target is an MCP server;
	// otherwise a valid SDK UUID could cross the product boundary.
	id, err := client.ResolveMCPReference(strings.TrimSpace(target))
	if err != nil {
		return fmt.Errorf("resolve MCP server %q: %w", target, err)
	}
	if err := client.DeactivateSDK(id); err != nil {
		return fmt.Errorf("remove MCP server: %w", err)
	}
	recordAppliedChange(cmd.Context(), "mcp.remove", "mcp_server")
	fmt.Fprintf(cmd.OutOrStdout(), "Removed MCP server %s.\n", target)
	return nil
}

func init() {
	RootCmd.AddCommand(mcpCmd)
	mcpCmd.AddCommand(mcpListCmd, mcpPlanCmd, mcpApplyCmd, mcpValidateCmd)
	addListFlags(mcpListCmd, &mcpListFlags)
	mcpPlanCmd.Flags().BoolVar(&mcpPlanJSON, "json", false, "Print plan result JSON")
	mcpPlanCmd.Flags().StringVar(&mcpPlanReceiptOut, "receipt-out", "", "Write the plan receipt to this path")
	mcpPlanCmd.Flags().StringVar(&mcpPlanOwnerTeam, "owner-team", "", "Optional owning team slug; defaults to the authenticated person")
	mcpApplyCmd.Flags().StringVar(&mcpApplyPlanID, "plan-id", "", "Apply a specific remote plan ID")
	mcpApplyCmd.Flags().StringVar(&mcpApplyReceiptPath, "receipt", "", "Read a plan receipt from this path")
}
