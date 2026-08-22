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

// runSDKTokenGenerate issues full-family SDK access with an optional time boundary.
func runSDKTokenGenerate(cmd *cobra.Command, target, name string) error {
	request, err := sdkTokenGenerateRequest(cmd, name)
	// Invalid lifetimes fail before family resolution or token mutation.
	if err != nil {
		return err
	}
	return issueAppToken(cmd, appTokenKindSDK, target, request)
}

// sdkTokenGenerateRequest deliberately adds expiry without introducing operation scope.
func sdkTokenGenerateRequest(cmd *cobra.Command, name string) (api.AppTokenGenerateRequest, error) {
	expiresIn, err := appTokenExpirySeconds(cmd)
	// Parser failures remain local to CLI input validation and never reach Engine.
	if err != nil {
		return api.AppTokenGenerateRequest{}, err
	}
	return api.AppTokenGenerateRequest{Name: name, ExpiresIn: expiresIn}, nil
}

// runSDKTokenList exposes retained family-token metadata without revealing credential material.
func runSDKTokenList(cmd *cobra.Command, target string) error {
	tokens, err := loadAppTokens(appTokenKindSDK, target)
	// Resolution or transport failure prevents presenting an incomplete token inventory.
	if err != nil {
		return err
	}
	// JSON output preserves the complete metadata contract for automation.
	if wantsJSON(cmd) {
		return writeJSON(cmd, tokens)
	}
	// Human output stays concise because SDK tokens do not expose operation scope controls.
	for _, token := range tokens {
		fmt.Fprintf(cmd.OutOrStdout(), "ID: %s, Name: %s, Created: %s\n", token.ID, token.Name, token.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}

// runSDKTokenRevoke removes one named family credential through the shared mutation path.
func runSDKTokenRevoke(cmd *cobra.Command, target, name string) error {
	return revokeAppToken(cmd, appTokenKindSDK, target, name)
}

// init registers SDK token actions and their action-specific flags.
func init() {
	sdkCmd.AddCommand(sdkTokenCmd)
	sdkTokenCmd.AddCommand(sdkTokenGenerateCmd, sdkTokenListCmd, sdkTokenRevokeCmd)
	addJSONOutputFlag(sdkTokenGenerateCmd, sdkTokenListCmd)
	addAppTokenExpiryFlag(sdkTokenGenerateCmd)
}
