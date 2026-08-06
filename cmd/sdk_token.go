package cmd

import (
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var sdkTokenCmd = commandGroup("token", "Manage SDK execution tokens")

var sdkTokenGenerateCmd = &cobra.Command{
	Use:   "generate <sdk-name-or-id> <token-name>",
	Short: "Generate an execution token shared by all versions of an SDK",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.sdk.token.generate", func(cmd *cobra.Command, args []string) error {
		return runSDKTokenGenerate(cmd, args[0], args[1])
	}),
}

var sdkTokenListCmd = &cobra.Command{
	Use:   "list <sdk-name-or-id>",
	Short: "List execution tokens shared by all versions of an SDK",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.sdk.token.list", func(cmd *cobra.Command, args []string) error {
		return runSDKTokenList(cmd, args[0])
	}),
}

var sdkTokenRevokeCmd = &cobra.Command{
	Use:   "revoke <sdk-name-or-id> <token-name-or-id>",
	Short: "Revoke an execution token shared by all versions of an SDK",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.sdk.token.revoke", func(cmd *cobra.Command, args []string) error {
		return runSDKTokenRevoke(cmd, args[0], args[1])
	}),
}

func runSDKTokenGenerate(cmd *cobra.Command, target, name string) error {
	client, appFamilyID, err := sdkFamilyClient(target)
	if err != nil {
		return err
	}
	result, err := client.GenerateSDKToken(appFamilyID, name)
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_token")
	fmt.Fprintf(cmd.OutOrStdout(), "Token generated: %s\n", result.Token)
	fmt.Fprintln(cmd.OutOrStdout(), "Make sure to copy it now; it won't be shown again.")
	return nil
}

func runSDKTokenList(cmd *cobra.Command, target string) error {
	client, appFamilyID, err := sdkFamilyClient(target)
	if err != nil {
		return err
	}
	tokens, err := client.ListSDKTokens(appFamilyID)
	if err != nil {
		return err
	}
	for _, token := range tokens {
		fmt.Fprintf(cmd.OutOrStdout(), "ID: %s, Name: %s, Created: %s\n", token.ID, token.Name, token.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}

func runSDKTokenRevoke(cmd *cobra.Command, target, name string) error {
	client, appFamilyID, err := sdkFamilyClient(target)
	if err != nil {
		return err
	}
	if err := client.RevokeSDKToken(appFamilyID, name); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_token")
	fmt.Fprintf(cmd.OutOrStdout(), "Token %q revoked successfully.\n", name)
	return nil
}

func sdkFamilyClient(target string) (*api.Client, string, error) {
	client, err := getAPIClient()
	if err != nil {
		return nil, "", err
	}
	// Resolve UUIDs through the SDK-kind boundary so an MCP family can never be
	// targeted by a syntactically valid token command.
	appFamilyID, err := client.ResolveSDKFamilyReference(strings.TrimSpace(target))
	if err != nil {
		return nil, "", err
	}
	return client, appFamilyID, nil
}

func init() {
	sdkCmd.AddCommand(sdkTokenCmd)
	sdkTokenCmd.AddCommand(sdkTokenGenerateCmd, sdkTokenListCmd, sdkTokenRevokeCmd)
}
