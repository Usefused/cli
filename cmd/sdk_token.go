package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var sdkTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Manage SDK tokens",
}

var sdkTokenGenerateCmd = &cobra.Command{
	Use:   "generate <sdk-id> <token-name>",
	Short: "Generate a new SDK token",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.sdk.token.generate", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		sdkID := args[0]
		name := args[1]
		res, err := client.GenerateSDKToken(sdkID, name)
		if err != nil {
			return err
		}
		fmt.Printf("Token generated: %s\n", res.Token)
		fmt.Printf("Make sure to copy it now, it won't be shown again.\n")
		return nil
	}),
}

var sdkTokenListCmd = &cobra.Command{
	Use:   "list <sdk-id>",
	Short: "List tokens for an SDK",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.sdk.token.list", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		sdkID := args[0]
		tokens, err := client.ListSDKTokens(sdkID)
		if err != nil {
			return err
		}
		for _, t := range tokens {
			fmt.Printf("ID: %s, Name: %s, Created: %s\n", t.ID, t.Name, t.CreatedAt.Format("2006-01-02 15:04:05"))
		}
		return nil
	}),
}

var sdkTokenRevokeCmd = &cobra.Command{
	Use:   "revoke <sdk-id> <token-name>",
	Short: "Revoke an SDK token",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.sdk.token.revoke", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		sdkID := args[0]
		name := args[1]
		err = client.RevokeSDKToken(sdkID, name)
		if err != nil {
			return err
		}
		fmt.Printf("Token '%s' revoked successfully.\n", name)
		return nil
	}),
}

func init() {
	sdkCmd.AddCommand(sdkTokenCmd)
	sdkTokenCmd.AddCommand(sdkTokenGenerateCmd)
	sdkTokenCmd.AddCommand(sdkTokenListCmd)
	sdkTokenCmd.AddCommand(sdkTokenRevokeCmd)
}
