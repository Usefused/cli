package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage workspace secrets",
}

var secretSetCmd = &cobra.Command{
	Use:   "set <service-id> <key-name> <credential-type> <value>",
	Short: "Set a workspace secret",
	Args:  cobra.ExactArgs(4),
	RunE: WithTelemetry("cli.secret.set", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		serviceID := args[0]
		keyName := args[1]
		credentialType := args[2]
		value := args[3]
		var sdkID *string
		if secretSetSDKID != "" {
			sdkID = &secretSetSDKID
		}
		err = client.UpsertSecret(serviceID, keyName, credentialType, value, sdkID)
		if err != nil {
			return err
		}
		fmt.Printf("Secret '%s' set successfully.\n", keyName)
		return nil
	}),
}

var secretSetSDKID string

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace secrets",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.secret.list", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		var sdkID *string
		if secretListSDKID != "" {
			sdkID = &secretListSDKID
		}
		secrets, err := client.ListSecrets(sdkID)
		if err != nil {
			return err
		}
		for _, s := range secrets {
			sdk := "workspace-default"
			if s.SDKID != nil {
				sdk = *s.SDKID
			}
			fmt.Printf("Service: %s, Key: %s, SDK: %s, Type: %s, Updated: %s\n", s.ServiceID, s.KeyName, sdk, s.CredentialType, s.UpdatedAt.Format("2006-01-02 15:04:05"))
		}
		return nil
	}),
}

var secretListSDKID string

var secretRemoveCmd = &cobra.Command{
	Use:   "remove <service-id> <key-name>",
	Short: "Remove a workspace secret",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.secret.remove", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		serviceID := args[0]
		keyName := args[1]
		var sdkID *string
		if secretRemoveSDKID != "" {
			sdkID = &secretRemoveSDKID
		}
		err = client.DeleteSecret(serviceID, keyName, sdkID)
		if err != nil {
			return err
		}
		fmt.Printf("Secret '%s' removed successfully.\n", keyName)
		return nil
	}),
}

var secretRemoveSDKID string

func init() {
	RootCmd.AddCommand(secretCmd)
	
	secretSetCmd.Flags().StringVar(&secretSetSDKID, "sdk-id", "", "Set secret as an override for a specific SDK")
	secretCmd.AddCommand(secretSetCmd)

	secretListCmd.Flags().StringVar(&secretListSDKID, "sdk-id", "", "Filter secrets by SDK ID")
	secretCmd.AddCommand(secretListCmd)

	secretRemoveCmd.Flags().StringVar(&secretRemoveSDKID, "sdk-id", "", "Remove override secret for a specific SDK")
	secretCmd.AddCommand(secretRemoveCmd)
}
