package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

type appTokenKind string

const (
	appTokenKindSDK appTokenKind = "sdk"
	appTokenKindMCP appTokenKind = "mcp"
)

func issueAppToken(cmd *cobra.Command, kind appTokenKind, target string, request api.AppTokenGenerateRequest) error {
	client, familyID, err := appTokenFamilyClient(kind, target)
	if err != nil {
		return err
	}
	result, err := client.GenerateAppToken(familyID, request)
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), string(kind)+"_token")
	if wantsJSON(cmd) {
		return writeJSON(cmd, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Token generated: %s\n", result.Token)
	fmt.Fprintln(cmd.OutOrStdout(), "Make sure to copy it now; it won't be shown again.")
	return nil
}

func loadAppTokens(kind appTokenKind, target string) ([]api.AppTokenResponse, error) {
	client, familyID, err := appTokenFamilyClient(kind, target)
	if err != nil {
		return nil, err
	}
	return client.ListAppTokens(familyID)
}

func revokeAppToken(cmd *cobra.Command, kind appTokenKind, target, name string) error {
	client, familyID, err := appTokenFamilyClient(kind, target)
	if err != nil {
		return err
	}
	if err := client.RevokeAppToken(familyID, name); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), string(kind)+"_token")
	fmt.Fprintf(cmd.OutOrStdout(), "Token %q revoked successfully.\n", name)
	return nil
}

func appTokenFamilyClient(kind appTokenKind, target string) (*api.Client, string, error) {
	client, err := getAPIClient()
	if err != nil {
		return nil, "", err
	}
	reference := strings.TrimSpace(target)
	switch kind {
	case appTokenKindSDK:
		// Resolving through the adapter boundary prevents a valid family ID for
		// the other app kind from being targeted accidentally.
		familyID, err := client.ResolveSDKFamilyReference(reference)
		return client, familyID, err
	case appTokenKindMCP:
		familyID, err := client.ResolveMCPFamilyReference(reference)
		return client, familyID, err
	default:
		return nil, "", errors.New("unsupported app token kind")
	}
}
