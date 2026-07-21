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
	Use:   "secret <service-slug|list> [set|remove] [args...]",
	Short: "Manage workspace secrets",
	Args:  validateSecretArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.secret", func(cmd *cobra.Command, args []string) error {
		return runSecretAction(cmd, args)
	}),
	ValidArgsFunction: completeSecretArgs,
}

var secretSetInteractive bool
var secretSetBucketID string
var secretSetExpiresAt string
var secretSetType string
var secretListBucketID string
var secretRemoveBucketID string

func validateSecretArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if args[0] == "list" {
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("secret requires an action (e.g. set, remove)")
	}
	action := args[1]
	if action != "set" && action != "remove" {
		return fmt.Errorf("unknown secret action %q", action)
	}
	if action == "set" {
		if secretSetInteractive {
			if len(args) != 2 {
				return fmt.Errorf("accepts exactly 1 arg after action (value is omitted) when using interactive mode")
			}
		} else {
			if len(args) != 3 {
				return fmt.Errorf("set accepts exactly 1 arg after action (value). Use -i for interactive mode if the service has multiple auth methods")
			}
		}
	}
	if action == "remove" {
		if len(args) != 3 {
			return fmt.Errorf("remove accepts exactly 1 arg after action (key-name)")
		}
	}
	return nil
}

func runSecretAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if args[0] == "list" {
		return runSecretList(cmd, args)
	}
	
	serviceSlug := args[0]
	action := args[1]

	switch action {
	case "set":
		var value string
		if len(args) > 2 {
			value = args[2]
		}
		return runSecretSet(cmd, serviceSlug, value)
	case "remove":
		keyName := args[2]
		return runSecretRemove(cmd, serviceSlug, keyName)
	default:
		return fmt.Errorf("unknown action %s", action)
	}
}

func runSecretSet(cmd *cobra.Command, serviceSlug, value string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
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
			return fmt.Errorf("service %s does not support authentication method '%s'. Valid methods are: %s. Run 'fused-cli service %s show' for details", serviceSlug, secretSetType, strings.Join(validTypes, ", "), serviceSlug)
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
			return fmt.Errorf("service %s has multiple authentication methods. Please use interactive mode (-i) or specify --type. Valid methods are: %s. Run 'fused-cli service %s show' for details", serviceSlug, strings.Join(validTypes, ", "), serviceSlug)
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
}

func runSecretList(cmd *cobra.Command, args []string) error {
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
}

func runSecretRemove(cmd *cobra.Command, serviceSlug, keyName string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
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
}

func completeSecretArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		client, err := getAPIClient()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		services, err := client.ListWorkspaceServices()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var candidates []string
		if strings.HasPrefix("list", toComplete) {
			candidates = append(candidates, "list")
		}
		for _, service := range services {
			slug := workspaceServiceSlugColumn(service)
			if slug != "-" && strings.HasPrefix(slug, toComplete) {
				candidates = append(candidates, slug)
			}
		}
		return candidates, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		if args[0] == "list" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		actions := []string{"set", "remove"}
		var matches []string
		for _, a := range actions {
			if strings.HasPrefix(a, toComplete) {
				matches = append(matches, a)
			}
		}
		return matches, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	RootCmd.AddCommand(secretCmd)
	
	// Flags for the "set" action
	secretCmd.Flags().StringVar(&secretSetBucketID, "bucket-id", "", "Set secret as an override for a specific Bucket (for 'set' action)")
	secretCmd.Flags().StringVar(&secretSetExpiresAt, "expires-at", "", "RFC3339 expiry timestamp (e.g. 2026-12-31T23:59:59Z); omit for no expiry (for 'set' action)")
	secretCmd.Flags().StringVar(&secretSetType, "type", "", "Specify the logical authentication method name (e.g., bearerAuth) (for 'set' action)")
	secretCmd.Flags().BoolVarP(&secretSetInteractive, "interactive", "i", false, "Interactive mode to prompt for service's supported authentication methods (for 'set' action)")

	// Flags for the "list" action
	secretCmd.Flags().StringVar(&secretListBucketID, "list-bucket-id", "", "Filter secrets by Bucket ID (for 'list' action)")

	// Flags for the "remove" action
	secretCmd.Flags().StringVar(&secretRemoveBucketID, "remove-bucket-id", "", "Remove override secret for a specific Bucket (for 'remove' action)")
}
