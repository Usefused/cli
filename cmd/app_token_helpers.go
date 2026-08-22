package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

type appTokenKind string

const (
	appTokenKindSDK appTokenKind = "sdk"
	appTokenKindMCP appTokenKind = "mcp"
)

// issueAppToken keeps SDK and MCP token mutation, audit, and output behavior aligned.
func issueAppToken(cmd *cobra.Command, kind appTokenKind, target string, request api.AppTokenGenerateRequest) error {
	client, familyID, err := appTokenFamilyClient(kind, target)
	// Family resolution must succeed before a credential can be issued to the requested app kind.
	if err != nil {
		return err
	}
	result, err := client.GenerateAppToken(familyID, request)
	// Failed issuance must not be reported as an applied user change.
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), string(kind)+"_token")
	// Structured output preserves the one-time token and exact expiry for automation.
	if wantsJSON(cmd) {
		return writeJSON(cmd, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Token generated: %s\n", result.Token)
	// Operators need the absolute boundary when they deliberately issue temporary access.
	if result.ExpiresAt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Expires: %s\n", result.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Make sure to copy it now; it won't be shown again.")
	return nil
}

// addAppTokenExpiryFlag gives SDK and MCP token commands one duration contract.
func addAppTokenExpiryFlag(cmd *cobra.Command) {
	cmd.Flags().String("expires-in", "", "Optional token lifetime, for example 15m or 1h")
}

// appTokenExpirySeconds converts a human duration into the Engine's whole-second policy value.
func appTokenExpirySeconds(cmd *cobra.Command) (*int64, error) {
	raw, err := cmd.Flags().GetString("expires-in")
	// An omitted lifetime intentionally retains the existing non-expiring token behavior.
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	duration, err := time.ParseDuration(raw)
	// Whole seconds avoid silently changing the lifetime the operator requested.
	if err != nil || duration <= 0 || duration%time.Second != 0 {
		return nil, errors.New("--expires-in must be a positive whole-second duration")
	}
	seconds := int64(duration / time.Second)
	return &seconds, nil
}

// loadAppTokens resolves the adapter boundary before requesting family token metadata.
func loadAppTokens(kind appTokenKind, target string) ([]api.AppTokenResponse, error) {
	client, familyID, err := appTokenFamilyClient(kind, target)
	// A failed family lookup must not fall back to a different adapter or identifier.
	if err != nil {
		return nil, err
	}
	return client.ListAppTokens(familyID)
}

// revokeAppToken shares exact-family revocation and successful-change auditing across adapters.
func revokeAppToken(cmd *cobra.Command, kind appTokenKind, target, name string) error {
	client, familyID, err := appTokenFamilyClient(kind, target)
	// Family resolution must succeed before a named credential can be revoked.
	if err != nil {
		return err
	}
	// Failed revocation must not be recorded as an applied user change.
	if err := client.RevokeAppToken(familyID, name); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), string(kind)+"_token")
	fmt.Fprintf(cmd.OutOrStdout(), "Token %q revoked successfully.\n", name)
	return nil
}

// appTokenFamilyClient prevents an SDK command from targeting MCP state and vice versa.
func appTokenFamilyClient(kind appTokenKind, target string) (*api.Client, string, error) {
	client, err := getAPIClient()
	// Client construction errors must stop before any remote lookup is attempted.
	if err != nil {
		return nil, "", err
	}
	reference := strings.TrimSpace(target)
	// Adapter-specific resolution preserves the public SDK and MCP identity boundaries.
	switch kind {
	case appTokenKindSDK:
		// Resolving through the adapter boundary prevents a valid family ID for
		// the other app kind from being targeted accidentally.
		familyID, err := client.ResolveSDKFamilyReference(reference)
		return client, familyID, err
	case appTokenKindMCP:
		// MCP lookup rejects SDK families even when the supplied value is a valid UUID.
		familyID, err := client.ResolveMCPFamilyReference(reference)
		return client, familyID, err
	default:
		// Unknown internal kinds fail closed instead of selecting a default adapter.
		return nil, "", errors.New("unsupported app token kind")
	}
}
