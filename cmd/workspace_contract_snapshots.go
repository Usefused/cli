package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

const defaultMissingContractsRefreshTimeout = 10 * time.Minute

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
	client, err := getAPIClientWithTimeout(missingContractsRefreshTimeout(cmd))
	// Client resolution must succeed before the maintenance mutation can begin.
	if err != nil {
		return err
	}
	result, err := client.RefreshMissingServiceContracts(workspaceRefreshMissingContractsLimit)
	// Engine owns the exact missing-or-unpinned classification and returns its typed failure unchanged.
	if err != nil {
		return err
	}
	// Structured callers need exact batch outcomes to decide whether another bounded repair pass is required.
	if wantsJSON(cmd) {
		return writeJSON(cmd, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Refreshed %d of %d missing or unpinned runtime contract snapshots (%d failed).\n", result.Refreshed, result.Missing, result.Failed)
	return nil
}

// missingContractsRefreshTimeout gives a full repair pass enough time while preserving an explicit operator budget.
func missingContractsRefreshTimeout(cmd *cobra.Command) time.Duration {
	// An explicit timeout remains authoritative for automation that needs a stricter deadline.
	if timeoutFlagChanged(cmd) {
		return RequestTimeout
	}
	return defaultMissingContractsRefreshTimeout
}

// init registers the compatibility-named maintenance command with wording that reflects both repairable snapshot states.
func init() {
	workspaceRefreshMissingContractsCmd.Flags().IntVar(&workspaceRefreshMissingContractsLimit, "limit", 100, "Maximum missing or unpinned activated service versions to refresh")
	addJSONOutputFlag(workspaceRefreshMissingContractsCmd)
	workspaceServicesCmd.AddCommand(workspaceRefreshMissingContractsCmd)
}
