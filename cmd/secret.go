package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage workspace secrets",
}

var secretSetInteractive bool

var secretSetCmd = &cobra.Command{
	Use:   "set <service-slug> <value>",
	Short: "Set a workspace secret",
	Args: func(cmd *cobra.Command, args []string) error {
		if secretSetInteractive {
			if len(args) != 1 {
				return fmt.Errorf("accepts exactly 1 arg (service-slug) when using interactive mode")
			}
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("accepts exactly 2 args (service-slug, value). Use -i for interactive mode if the service has multiple auth methods")
		}
		return nil
	},
	RunE: WithTelemetry("cli.secret.set", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		serviceSlug := args[0]
		bucketID := secretSetBucketID
		var expiresAt *time.Time
		if secretSetExpiresAt != "" {
			parsed, err := time.Parse(time.RFC3339, secretSetExpiresAt)
			if err != nil {
				return fmt.Errorf("invalid --expires-at %q, expected RFC3339 (e.g. 2026-12-31T23:59:59Z): %w", secretSetExpiresAt, err)
			}
			expiresAt = &parsed
		}
		resolvedBucketID, err := resolveBucketIDPrompt(client, bucketID)
		if err != nil {
			return err
		}
		bucketID = resolvedBucketID

		serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
		if err != nil {
			return err
		}

		info, err := client.GetServiceInfo(serviceSlug)
		if err != nil {
			return err
		}
		if len(info.AuthConfigs) == 0 {
			return fmt.Errorf("service %s does not declare any authentication methods in its OpenAPI spec", serviceSlug)
		}

		var value string
		if len(args) == 2 {
			value = args[1]
		}

		var selectedAuth *api.AuthConfig
		if secretSetType != "" {
			for _, a := range info.AuthConfigs {
				if a.Name == secretSetType {
					selectedAuth = &a
					break
				}
			}
			if selectedAuth == nil {
				var validTypes []string
				for _, a := range info.AuthConfigs {
					validTypes = append(validTypes, a.Name)
				}
				return fmt.Errorf("service %s does not support authentication method '%s'. Valid methods are: %s. Run 'fused-cli service show %s' for details", serviceSlug, secretSetType, strings.Join(validTypes, ", "), serviceSlug)
			}
		}

		// If no type specified and not already forced into interactive mode, try to auto-select
		if selectedAuth == nil && !secretSetInteractive {
			if len(info.AuthConfigs) == 1 {
				selectedAuth = &info.AuthConfigs[0]
			} else {
				var validTypes []string
				for _, a := range info.AuthConfigs {
					validTypes = append(validTypes, a.Name)
				}
				return fmt.Errorf("service %s has multiple authentication methods. Please use interactive mode (-i) or specify --type. Valid methods are: %s. Run 'fused-cli service show %s' for details", serviceSlug, strings.Join(validTypes, ", "), serviceSlug)
			}
		}

		// If interactive mode is enabled and we still haven't selected an auth, prompt for one
		if secretSetInteractive && selectedAuth == nil {
			options := make([]huh.Option[int], len(info.AuthConfigs))
			for i, auth := range info.AuthConfigs {
				title := fmt.Sprintf("%s (Key: %s)", auth.Name, auth.Name)
				if auth.Type == "http" {
					title = fmt.Sprintf("HTTP %s (%s)", auth.Scheme, auth.Name)
				}
				options[i] = huh.NewOption(title, i)
			}
			
			var selected int
			err = huh.NewSelect[int]().
				Title("Which authentication method would you like to configure?").
				Options(options...).
				Value(&selected).
				Run()
			if err != nil {
				return err
			}
			selectedAuth = &info.AuthConfigs[selected]
		}

		auth := *selectedAuth

		// Handle Basic Auth (Requires 2 inputs)
		if auth.Type == "http" && strings.ToLower(auth.Scheme) == "basic" {
			if value != "" && !secretSetInteractive {
				return fmt.Errorf("basic auth requires two values (username and password). Please use interactive mode (-i)")
			}
			var username, password string
			err = huh.NewInput().Title("Username:").Value(&username).Run()
			if err != nil { return err }
			err = huh.NewInput().Title("Password:").EchoMode(huh.EchoModePassword).Value(&password).Run()
			if err != nil { return err }

			err = client.UpsertSecret(serviceID, auth.Name+"_username", "basic", username, bucketID, expiresAt)
			if err != nil { return err }
			err = client.UpsertSecret(serviceID, auth.Name+"_password", "basic", password, bucketID, expiresAt)
			if err != nil { return err }
			fmt.Printf("Basic Auth secrets set successfully.\n")
			return nil
		}

		// Handle all other single-token auth methods
		keyName := auth.Name
		credType := auth.Type
		promptTitle := fmt.Sprintf("Enter %s:", keyName)

		if auth.Type == "http" && strings.ToLower(auth.Scheme) == "bearer" {
			credType = "bearer"
			promptTitle = "Enter Bearer Token:"
		} else if auth.Type == "oauth2" || auth.Type == "openIdConnect" {
			promptTitle = "Enter OAuth2 Token:"
		}

		// Prompt for value if it wasn't provided
		if value == "" {
			err = huh.NewInput().Title(promptTitle).EchoMode(huh.EchoModePassword).Value(&value).Run()
			if err != nil {
				return err
			}
		}

		err = client.UpsertSecret(serviceID, keyName, credType, value, bucketID, expiresAt)
		if err != nil {
			return err
		}
		if expiresAt != nil {
			fmt.Printf("Secret set successfully for %s (expires %s).\n", serviceSlug, expiresAt.Format(time.RFC3339))
		} else {
			fmt.Printf("Secret set successfully for %s.\n", serviceSlug)
		}
		return nil
	}),
}

var secretSetBucketID string
var secretSetExpiresAt string
var secretSetType string

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace secrets",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.secret.list", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		bucketID := secretListBucketID
		resolvedBucketID, err := resolveBucketID(client, bucketID)
		if err != nil {
			return err
		}
		secrets, err := client.ListSecrets(resolvedBucketID)
		if err != nil {
			return err
		}
		for _, s := range secrets {
			bucket := s.BucketID
			expiry := "never"
			if s.ExpiresAt != nil {
				expiry = s.ExpiresAt.Format("2006-01-02 15:04:05")
				if s.ExpiresAt.Before(time.Now()) {
					expiry += " (EXPIRED)"
				}
			}
			fmt.Printf("Service: %s, Key: %s, Bucket: %s, Type: %s, Expires: %s, Updated: %s\n", s.ServiceID, s.KeyName, bucket, s.CredentialType, expiry, s.UpdatedAt.Format("2006-01-02 15:04:05"))
		}
		return nil
	}),
}

var secretListBucketID string

var secretRemoveCmd = &cobra.Command{
	Use:   "remove <service-slug> <key-name>",
	Short: "Remove a workspace secret",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.secret.remove", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		serviceSlug := args[0]
		keyName := args[1]
		bucketID := secretRemoveBucketID

		resolvedBucketID, err := resolveBucketID(client, bucketID)
		if err != nil {
			return err
		}

		serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
		if err != nil {
			return err
		}

		err = client.DeleteSecret(serviceID, keyName, resolvedBucketID)
		if err != nil {
			return err
		}
		fmt.Printf("Secret '%s' removed successfully.\n", keyName)
		return nil
	}),
}

var secretRemoveBucketID string

func init() {
	RootCmd.AddCommand(secretCmd)
	
	secretSetCmd.Flags().StringVar(&secretSetBucketID, "bucket-id", "", "Set secret as an override for a specific Bucket")
	secretSetCmd.Flags().StringVar(&secretSetExpiresAt, "expires-at", "", "RFC3339 expiry timestamp (e.g. 2026-12-31T23:59:59Z); omit for no expiry")
	secretSetCmd.Flags().StringVar(&secretSetType, "type", "", "Specify the logical authentication method name (e.g., bearerAuth)")
	secretSetCmd.Flags().BoolVarP(&secretSetInteractive, "interactive", "i", false, "Interactive mode to prompt for service's supported authentication methods")
	secretCmd.AddCommand(secretSetCmd)

	secretListCmd.Flags().StringVar(&secretListBucketID, "bucket-id", "", "Filter secrets by Bucket ID")
	secretCmd.AddCommand(secretListCmd)

	secretRemoveCmd.Flags().StringVar(&secretRemoveBucketID, "bucket-id", "", "Remove override secret for a specific Bucket")
	secretCmd.AddCommand(secretRemoveCmd)
}
