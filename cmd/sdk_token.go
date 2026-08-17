package cmd

import (
	"fmt"

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
	Use:   "revoke <sdk-name-or-id> <token-name>",
	Short: "Revoke an execution token shared by all versions of an SDK",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.sdk.token.revoke", func(cmd *cobra.Command, args []string) error {
		return runSDKTokenRevoke(cmd, args[0], args[1])
	}),
}

func runSDKTokenGenerate(cmd *cobra.Command, target, name string) error {
	return issueAppToken(cmd, appTokenKindSDK, target, api.AppTokenGenerateRequest{Name: name})
}

func runSDKTokenList(cmd *cobra.Command, target string) error {
	tokens, err := loadAppTokens(appTokenKindSDK, target)
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, tokens)
	}
	for _, token := range tokens {
		fmt.Fprintf(cmd.OutOrStdout(), "ID: %s, Name: %s, Created: %s\n", token.ID, token.Name, token.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}

func runSDKTokenRevoke(cmd *cobra.Command, target, name string) error {
	return revokeAppToken(cmd, appTokenKindSDK, target, name)
}

func init() {
	sdkCmd.AddCommand(sdkTokenCmd)
	sdkTokenCmd.AddCommand(sdkTokenGenerateCmd, sdkTokenListCmd, sdkTokenRevokeCmd)
	addJSONOutputFlag(sdkTokenGenerateCmd, sdkTokenListCmd)
}
