package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp [list]",
	Short: "Inspect deployed MCP servers",
	Args:  validateMCPArgs,
	RunE: WithTelemetry("cli.mcp", func(cmd *cobra.Command, args []string) error {
		return runMCPAction(cmd, args)
	}),
}

var mcpListFlags listFlags
var mcpPlanJSON bool
var mcpPlanReceiptOut string
var mcpApplyPlanID string
var mcpApplyReceiptPath string

var mcpPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan MCP configuration",
	RunE: WithTelemetry("cli.mcp.plan", func(cmd *cobra.Command, args []string) error {
		return runConfigPlan(planOptions{filter: filterMCP, jsonOut: mcpPlanJSON, receiptOut: mcpPlanReceiptOut})
	}),
}

var mcpApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply MCP configuration",
	RunE: WithTelemetry("cli.mcp.apply", func(cmd *cobra.Command, args []string) error {
		return runConfigApply(applyOptions{filter: filterMCP, planID: mcpApplyPlanID, receiptPath: mcpApplyReceiptPath})
	}),
}

var mcpValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate MCP configuration",
	RunE: WithTelemetry("cli.mcp.validate", func(cmd *cobra.Command, args []string) error {
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

func validateMCPArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) == 1 && args[0] == "list" {
		return nil
	}
	return fmt.Errorf("unknown mcp command %q", args[0])
}

func runMCPAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return runMCPList(cmd)
}

func runMCPList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	page, err := client.ListMCPServers(mcpListFlags.pageOptions())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tID\tACTIVE\tURL")
	for _, server := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", server.Name, server.Version, server.ID, server.Active, server.MCPURL)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, mcpListFlags)
	return nil
}

func init() {
	addListFlags(mcpCmd, &mcpListFlags)
	mcpPlanCmd.Flags().BoolVar(&mcpPlanJSON, "json", false, "Print plan receipt as JSON")
	mcpPlanCmd.Flags().StringVar(&mcpPlanReceiptOut, "receipt-out", "", "Write the plan receipt to this path")
	mcpApplyCmd.Flags().StringVar(&mcpApplyPlanID, "plan-id", "", "Apply this plan ID")
	mcpApplyCmd.Flags().StringVar(&mcpApplyReceiptPath, "receipt", "", "Read a plan receipt from this path")
	mcpCmd.AddCommand(mcpPlanCmd, mcpApplyCmd, mcpValidateCmd)
	RootCmd.AddCommand(mcpCmd)
}
