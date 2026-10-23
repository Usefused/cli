package cmd

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// validateSecretExportArgs requires an opaque metadata ID so generic and provider secrets cannot collide by name.
func validateSecretExportArgs(cmd *cobra.Command, args []string) error {
	// Both bucket and exact secret are mandatory; neither is inferred.
	if err := cobra.ExactArgs(2)(cmd, args); err != nil {
		return err
	}
	// Invalid IDs fail locally before bucket resolution or network calls.
	if _, err := uuid.Parse(args[1]); err != nil {
		return fmt.Errorf("secret ID must be a UUID from bucket secret list --json")
	}
	return nil
}

// runBucketSecretReveal writes plaintext only after this explicitly named retrieval command succeeds.
func runBucketSecretReveal(cmd *cobra.Command, args []string) error {
	client, bucketID, err := bucketClientAndID(args[0])
	// Bucket authority remains the existing name/ID resolution path.
	if err != nil {
		return err
	}
	value, err := client.RevealBucketSecret(bucketID, args[1])
	// Denials and errors must never print a partial credential.
	if err != nil {
		return err
	}
	// JSON is explicit machine output, never a telemetry or mutation receipt.
	if wantsJSON(cmd) {
		return writeJSON(cmd, map[string]string{"value": value})
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), value)
	return err
}

// runBucketSecretExportable updates opt-in policy while keeping the value private.
func runBucketSecretExportable(cmd *cobra.Command, args []string) error {
	// Omission cannot silently disable a previously enabled policy.
	if !cmd.Flags().Changed("enabled") {
		return fmt.Errorf("pass --enabled=true or --enabled=false explicitly")
	}
	enabled, _ := cmd.Flags().GetBool("enabled")
	client, bucketID, err := bucketClientAndID(args[0])
	// Resolution errors must not redirect the policy change to another bucket.
	if err != nil {
		return err
	}
	// Engine validates family eligibility and management authority.
	if err := client.SetBucketSecretExportable(bucketID, args[1], enabled); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Secret exportable: %t\n", enabled)
	return nil
}

// init keeps retrieval explicit and separate from metadata listing and ordinary credential use.
func init() {
	reveal := &cobra.Command{Use: "reveal <bucket-name-or-id> <secret-id>", Short: "Retrieve an exportable API key or generic secret (prints plaintext)", Args: validateSecretExportArgs, RunE: runBucketSecretReveal}
	policy := &cobra.Command{Use: "exportable <bucket-name-or-id> <secret-id>", Short: "Allow or disable secret retrieval without replacing its value", Args: validateSecretExportArgs, RunE: runBucketSecretExportable}
	policy.Flags().Bool("enabled", false, "Whether callers with credentials.reveal may retrieve this secret")
	addJSONOutputFlag(reveal)
	bucketSecretCmd.AddCommand(reveal, policy)
}
