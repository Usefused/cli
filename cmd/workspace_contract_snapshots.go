package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var workspaceRefreshMissingContractsLimit int

var workspaceRefreshMissingContractsCmd = &cobra.Command{
	Use:   "refresh-missing-contracts",
	Short: "Backfill missing runtime contracts for activated workspace services",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.services.refresh_missing_contracts", func(cmd *cobra.Command, args []string) error {
		return runWorkspaceRefreshMissingContracts(cmd)
	}),
}

func runWorkspaceRefreshMissingContracts(cmd *cobra.Command) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	result, err := client.RefreshMissingServiceContracts(workspaceRefreshMissingContractsLimit)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Refreshed %d of %d missing runtime contracts (%d failed).\n", result.Refreshed, result.Missing, result.Failed)
	return nil
}

func init() {
	workspaceRefreshMissingContractsCmd.Flags().IntVar(&workspaceRefreshMissingContractsLimit, "limit", 100, "Maximum activated service versions to refresh")
	workspaceServicesCmd.AddCommand(workspaceRefreshMissingContractsCmd)
}
