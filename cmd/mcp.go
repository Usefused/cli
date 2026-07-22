package cmd

import (
	"fmt"
	"text/tabwriter"

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
	fmt.Fprintln(w, "NAME\tID\tACTIVE\tURL")
	for _, server := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%t\t%s\n", server.Name, server.ID, server.Active, server.MCPURL)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, mcpListFlags)
	return nil
}

func init() {
	addListFlags(mcpCmd, &mcpListFlags)
	RootCmd.AddCommand(mcpCmd)
}
