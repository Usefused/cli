package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Inspect deployed MCP servers",
	Args:  cobra.ArbitraryArgs,
	RunE: WithTelemetry("cli.mcp", func(cmd *cobra.Command, args []string) error {
		return runMCPDynamicAction(cmd, args)
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

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List deployed MCP servers",
	RunE: WithTelemetry("cli.mcp.list", func(cmd *cobra.Command, args []string) error {
		return runMCPList(cmd)
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
	page, err := client.ListMCPServers(mcpListFlags.pageOptions())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tID\tACTIVE\tCREATED\tURL")
	for _, server := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\t%s\n", server.Name, server.Version, server.ID, server.Active, server.CreatedAt, server.MCPURL)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, mcpListFlags)
	return nil
}

func runMCPRemove(cmd *cobra.Command, target string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}

	id := target
	// If it's not a UUID, resolve it via GetMCPServerByName
	if _, err := uuid.Parse(target); err != nil {
		parts := strings.SplitN(target, "@", 2)
		name := parts[0]
		version := ""
		if len(parts) > 1 {
			version = parts[1]
		}
		
		server, err := client.GetMCPServerByName(name, version)
		if err != nil {
			return fmt.Errorf("failed to resolve mcp server %q: %w", target, err)
		}
		id = server.ID
	}

	if err := client.DeactivateSDK(id); err != nil {
		return fmt.Errorf("failed to remove mcp server: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Successfully removed MCP server %s\n", target)
	return nil
}

func init() {
	addListFlags(mcpListCmd, &mcpListFlags)
	addListFlags(mcpCmd, &mcpListFlags)
	mcpPlanCmd.Flags().BoolVar(&mcpPlanJSON, "json", false, "Print plan receipt as JSON")
	mcpPlanCmd.Flags().StringVar(&mcpPlanReceiptOut, "receipt-out", "", "Write the plan receipt to this path")
	mcpApplyCmd.Flags().StringVar(&mcpApplyPlanID, "plan-id", "", "Apply this plan ID")
	mcpApplyCmd.Flags().StringVar(&mcpApplyReceiptPath, "receipt", "", "Read a plan receipt from this path")
	mcpCmd.AddCommand(mcpListCmd, mcpPlanCmd, mcpApplyCmd, mcpValidateCmd)
	RootCmd.AddCommand(mcpCmd)
}

