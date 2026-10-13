package cmd

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// runtimeBucketInvocationFlag keeps explicit routing outside authored input and ordinary provider selectors.
func runtimeBucketInvocationFlag(cmd *cobra.Command, kind, operation string) (string, error) {
	// Direct helper calls without the public flag retain static routing.
	if cmd.Flags().Lookup("bucket-id") == nil {
		return "", nil
	}
	raw, _ := cmd.Flags().GetString("bucket-id")
	if !cmd.Flags().Changed("bucket-id") {
		return "", nil
	}
	if kind != "unified_app" || operation != "execute" {
		return "", fmt.Errorf("--bucket-id requires Unified App execute")
	}
	id, err := uuid.Parse(raw)
	// Empty and zero identities cannot mean 'fall back to another bucket'.
	if err != nil || id == uuid.Nil {
		return "", fmt.Errorf("--bucket-id must be a nonzero bucket UUID")
	}
	return id.String(), nil
}
