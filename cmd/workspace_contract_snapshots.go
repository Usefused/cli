package cmd

import (
	"fmt"
	"strings"
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
	// Per-version failures need their stable code and safe diagnostic so an operator can repair the source without tracing server logs.
	for _, item := range result.Results {
		// Successful entries have no error code and do not need a second line after the aggregate summary.
		if item.Error == "" {
			continue
		}
		message := missingContractFailureMessage(item.Error, item.ErrorMessage)
		fmt.Fprintf(cmd.OutOrStdout(), "Could not refresh service %s (version %s): %s\n", item.ServiceID, item.Version, message)
	}
	return nil
}

// missingContractFailureMessage presents one stable reason label followed by plain guidance, including during a rolling Engine upgrade.
func missingContractFailureMessage(code, message string) string {
	message = strings.TrimSpace(message)
	// A current Engine supplies a reviewed explanation with its next repair step.
	if message != "" {
		return message
	}
	// Older Engines supplied only a stable code, so the CLI translates the known cases into actionable prose.
	switch code {
	case "runtime_contract_rejected":
		// Validation failures require replacing the unusable saved contract before retrying.
		return "runtime_contract_validation_failed: The saved API contract failed validation. Re-import the service from its original OpenAPI file or URL, then try again."
	case "runtime_contract_fetch_failed":
		// Fetch failures point administrators to Registry reachability without exposing transport details.
		return "runtime_contract_fetch_failed: Fused could not download this API contract from Registry. Ask your Fused administrator to check that Registry is reachable, then try again."
	case "runtime_contract_store_failed":
		// Store failures point administrators to Engine persistence without exposing database details.
		return "runtime_contract_store_failed: Fused could not save this API contract. Ask your Fused administrator to check Engine storage, then try again."
	default:
		// Unknown legacy codes retain a useful escalation path instead of printing an unexplained enum.
		return "runtime_contract_refresh_failed: Fused could not refresh this service. Ask your Fused administrator to check Engine and Registry logs, then try again."
	}
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
