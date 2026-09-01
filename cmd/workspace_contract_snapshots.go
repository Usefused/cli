package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var workspaceRefreshMissingContractsLimit int

var workspaceRefreshMissingContractsCmd = &cobra.Command{
	Use:   "refresh-missing-contracts",
	Short: "Repair missing or unpinned runtime contract snapshots for activated workspace service versions",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.services.refresh_missing_contracts", func(cmd *cobra.Command, args []string) error {
		return runWorkspaceRefreshMissingContracts(cmd)
	}),
}

// runWorkspaceRefreshMissingContracts repairs the bounded set of absent or generation-incomplete active snapshots reported by Engine.
func runWorkspaceRefreshMissingContracts(cmd *cobra.Command) error {
	client, err := getAPIClient()
	// Client resolution must succeed before the maintenance mutation can begin.
	if err != nil {
		return err
	}
	result, err := client.RefreshMissingServiceContracts(workspaceRefreshMissingContractsLimit)
	// Engine owns the exact missing-or-unpinned classification and returns its typed failure unchanged.
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Refreshed %d of %d missing or unpinned runtime contract snapshots (%d failed).\n", result.Refreshed, result.Missing, result.Failed)
	return nil
}

// init registers the compatibility-named maintenance command with wording that reflects both repairable snapshot states.
func init() {
	workspaceRefreshMissingContractsCmd.Flags().IntVar(&workspaceRefreshMissingContractsLimit, "limit", 100, "Maximum missing or unpinned activated service versions to refresh")
	workspaceServicesCmd.AddCommand(workspaceRefreshMissingContractsCmd)
}
