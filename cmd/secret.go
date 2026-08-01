package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
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
	// Write to OTEL to audit user/agent-triggered mutative execution.
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
	if len(args) == 0 || args[0] == "list" {
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("secret requires an action (e.g. set, remove)")
	}
	action := args[1]
	if action == "set" {
		return validateSecretSetArgs(args)
	}
	if action == "remove" {
		if len(args) != 3 {
			return fmt.Errorf("remove accepts exactly 1 arg after action (key-name)")
		}
		return nil
	}
	return fmt.Errorf("unknown secret action %q", action)
}

func validateSecretSetArgs(args []string) error {
	// Interactive mode handles value input dynamically through UI prompts, so we don't expect it as a CLI argument.
	if secretSetInteractive {
		if len(args) != 2 {
			return fmt.Errorf("accepts exactly 1 arg after action (value is omitted) when using interactive mode")
		}
		return nil
	}
	if len(args) != 3 {
		return fmt.Errorf("set accepts exactly 1 arg after action (value). Use -i for interactive mode if the service has multiple auth methods")
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

// runSecretSet resolves the bucket and service before inspecting auth metadata
// so secret keys are stored under the same provider-specific names the Engine
// later uses for request-time credential resolution.
func runSecretSet(cmd *cobra.Command, serviceSlug, value string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	expiresAt, err := parseSecretExpiresAt()
	if err != nil {
		return err
	}
	bucketID, err := resolveBucketIDPrompt(client, secretSetBucketID)
	if err != nil {
		return err
	}
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}
	info, err := client.GetServiceInfo(serviceSlug)
	if err != nil {
		return err
	}
	auth, err := selectSecretAuth(info, serviceSlug)
	if err != nil {
		return err
	}
	authType := canonicalSecretAuthType(auth)
	// Basic auth requires two distinct inputs (username and password) which can't be cleanly parsed from a single positional argument, so we route it to a specialized handler.
	if authType == "basic" {
		return handleBasicSecretSet(client, serviceID, bucketID, auth, value, expiresAt)
	}
	if authType == "mtls" {
		return handleMTLSSecretSet(client, serviceID, bucketID, auth, value, expiresAt)
	}
	return handleTokenSecretSet(client, serviceID, bucketID, auth, value, expiresAt, serviceSlug)
}

// parseSecretExpiresAt validates expiry locally so malformed timestamps do not
// reach the API and create ambiguous secret lifecycle state.
func parseSecretExpiresAt() (*time.Time, error) {
	if secretSetExpiresAt == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, secretSetExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("invalid --expires-at %q, expected RFC3339 (e.g. 2026-12-31T23:59:59Z): %w", secretSetExpiresAt, err)
	}
	return &parsed, nil
}

// selectSecretAuth keeps manual secret setup tied to the service's declared
// auth scheme; bucket secrets must use those names for runtime lookup to work.
func selectSecretAuth(info *api.ServiceInfo, serviceSlug string) (*api.AuthConfig, error) {
	if len(info.AuthConfigs) == 0 {
		return nil, fmt.Errorf("service %s does not declare any authentication methods in its OpenAPI spec", serviceSlug)
	}
	if secretSetType != "" {
		return selectSecretAuthByType(info, serviceSlug)
	}
	// If only one method is available and we're not in interactive mode, auto-select it for better UX rather than forcing the user to specify it.
	if !secretSetInteractive {
		if len(info.AuthConfigs) == 1 {
			return &info.AuthConfigs[0], nil
		}
		var validTypes []string
		for _, a := range info.AuthConfigs {
			validTypes = append(validTypes, a.Name)
		}
		return nil, fmt.Errorf("service %s has multiple authentication methods. Please use interactive mode (-i) or specify --type. Valid methods are: %s. Run 'fused-cli service %s show' for details", serviceSlug, strings.Join(validTypes, ", "), serviceSlug)
	}
	if err := requireInteractive("pass --type and provide the credential value explicitly"); err != nil {
		return nil, err
	}
	return promptSecretAuthSelect(info)
}

// selectSecretAuthByType treats --type as a concrete auth scheme name rather
// than a broad family because static secrets are keyed by scheme identity.
func selectSecretAuthByType(info *api.ServiceInfo, serviceSlug string) (*api.AuthConfig, error) {
	requested := canonicalSecretTypeName(secretSetType)
	for i := range info.AuthConfigs {
		auth := &info.AuthConfigs[i]
		if auth.Name == secretSetType || canonicalSecretAuthType(auth) == requested {
			return &info.AuthConfigs[i], nil
		}
	}
	var validTypes []string
	for _, a := range info.AuthConfigs {
		validTypes = append(validTypes, a.Name)
	}
	return nil, fmt.Errorf("service %s does not support authentication method '%s'. Valid methods are: %s. Run 'fused-cli service %s show' for details", serviceSlug, secretSetType, strings.Join(validTypes, ", "), serviceSlug)
}

// promptSecretAuthSelect shows scheme names next to labels because those names
// become the bucket secret keys the Engine reads during execution.
func promptSecretAuthSelect(info *api.ServiceInfo) (*api.AuthConfig, error) {
	options := make([]huh.Option[int], len(info.AuthConfigs))
	for i, auth := range info.AuthConfigs {
		title := fmt.Sprintf("%s (Key: %s)", auth.Name, auth.Name)
		if auth.Type == "http" {
			title = fmt.Sprintf("HTTP %s (%s)", auth.Scheme, auth.Name)
		}
		options[i] = huh.NewOption(title, i)
	}
	var selected int
	err := huh.NewSelect[int]().
		Title("Which authentication method would you like to configure?").
		Options(options...).
		Value(&selected).
		Run()
	if err != nil {
		return nil, err
	}
	return &info.AuthConfigs[selected], nil
}

func handleBasicSecretSet(client *api.Client, serviceID, bucketID string, auth *api.AuthConfig, value string, expiresAt *time.Time) error {
	var username, password string

	if value != "" {
		pairs := parseInlineKeyValuePairs(value)
		username, password = pairs["username"], pairs["password"]
		if username == "" || password == "" {
			return fmt.Errorf("basic auth requires both username and password. Provide format 'username=...;password=...' or use interactive mode (-i)")
		}
	} else {
		if err := requireInteractive("provide username and password using the command's value input"); err != nil {
			return err
		}
		err := huh.NewInput().Title("Username:").Value(&username).Run()
		if err != nil {
			return err
		}
		err = huh.NewInput().Title("Password:").EchoMode(huh.EchoModePassword).Value(&password).Run()
		if err != nil {
			return err
		}
	}

	name := secretAuthCredentialName(auth)
	err := client.UpsertSecrets(bucketID, []api.SecretUpsertRequest{
		{ServiceID: serviceID, KeyName: name + "_username", CredentialType: "basic", Value: username, ExpiresAt: expiresAt},
		{ServiceID: serviceID, KeyName: name + "_password", CredentialType: "basic", Value: password, ExpiresAt: expiresAt},
	})
	if err != nil {
		return err
	}
	fmt.Printf("Basic Auth secrets set successfully.\n")
	return nil
}

func handleMTLSSecretSet(client *api.Client, serviceID, bucketID string, auth *api.AuthConfig, value string, expiresAt *time.Time) error {
	var cert, key string

	if value != "" {
		pairs := parseInlineKeyValuePairs(value)
		cert, key = pairs["cert"], pairs["key"]
		if cert == "" || key == "" {
			return fmt.Errorf("mTLS auth requires both cert and key. Provide format 'cert=...;key=...' or use interactive mode (-i)")
		}
	} else {
		if err := requireInteractive("provide the certificate and private key using the command's value input"); err != nil {
			return err
		}
		err := huh.NewText().Title("Client certificate PEM:").Value(&cert).Run()
		if err != nil {
			return err
		}
		err = huh.NewText().Title("Client private key PEM:").Value(&key).Run()
		if err != nil {
			return err
		}
	}

	if err := validateMTLSSecretPair(cert, key); err != nil {
		return err
	}
	name := secretAuthCredentialName(auth)
	if err := client.UpsertSecrets(bucketID, []api.SecretUpsertRequest{
		{ServiceID: serviceID, KeyName: name + "_cert", CredentialType: "mtls", Value: cert, ExpiresAt: expiresAt},
		{ServiceID: serviceID, KeyName: name + "_key", CredentialType: "mtls", Value: key, ExpiresAt: expiresAt},
	}); err != nil {
		return err
	}
	fmt.Printf("mTLS secrets set successfully.\n")
	return nil
}

// handleTokenSecretSet maps imported auth spellings onto the public credential
// families while preserving the scheme key used for provider injection.
func handleTokenSecretSet(client *api.Client, serviceID, bucketID string, auth *api.AuthConfig, value string, expiresAt *time.Time, serviceSlug string) error {
	keyName := secretAuthCredentialName(auth)
	authType := canonicalSecretAuthType(auth)
	credType := authType
	promptTitle := fmt.Sprintf("Enter %s:", keyName)

	if authType == "bearer" {
		credType = "bearer"
		promptTitle = "Enter Bearer Token:"
	} else if authType == "oauth" {
		credType = "oauth"
		promptTitle = "Enter OAuth Token:"
	}

	if value == "" {
		if err := requireInteractive("provide the credential value explicitly"); err != nil {
			return err
		}
		err := huh.NewInput().Title(promptTitle).EchoMode(huh.EchoModePassword).Value(&value).Run()
		if err != nil {
			return err
		}
	}

	err := client.UpsertSecret(serviceID, keyName, credType, value, bucketID, expiresAt)
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

// canonicalSecretAuthType lets the CLI accept imported service metadata while
// still writing bucket secrets under the public credential family.
func canonicalSecretAuthType(auth *api.AuthConfig) string {
	normalized := canonicalSecretTypeName(auth.Type)
	if normalized == "mutualtls" || normalized == "mutual_tls" || normalized == "mtls" {
		return "mtls"
	}
	if normalized == "apikey" {
		return "api_key"
	}
	if normalized == "openidconnect" || normalized == "open_id_connect" {
		return "oidc"
	}
	if normalized == "oauth2" || normalized == "oauth2_authorization_code" {
		return "oauth"
	}
	if normalized == "http" {
		return canonicalSecretTypeName(auth.Scheme)
	}
	return normalized
}

// canonicalSecretTypeName keeps --type matching on public auth families while
// still accepting provider scheme names for teams that know the exact key.
func canonicalSecretTypeName(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return strings.ReplaceAll(normalized, "-", "_")
}

// validateMTLSSecretPair fails before any upsert so manual CLI setup cannot
// leave the bucket with one invalid half of a transport credential pair.
func validateMTLSSecretPair(certPEM, keyPEM string) error {
	if strings.TrimSpace(certPEM) == "" || strings.TrimSpace(keyPEM) == "" {
		return fmt.Errorf("mTLS certificate and key are required")
	}
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return fmt.Errorf("mTLS certificate/key invalid or mismatched")
	}
	if mtlsSecretCertExpired(certPEM) {
		return fmt.Errorf("mTLS certificate is expired")
	}
	return nil
}

// mtlsSecretCertExpired checks certificate NotAfter locally so expired client
// certs are rejected before they become bucket secrets.
func mtlsSecretCertExpired(certPEM string) bool {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	return time.Now().UTC().After(cert.NotAfter)
}

// secretAuthCredentialName mirrors Engine credential naming so manual bucket
// writes land under the exact keys request execution reads later.
func secretAuthCredentialName(auth *api.AuthConfig) string {
	if name := strings.TrimSpace(auth.Name); name != "" {
		return name
	}
	authType := canonicalSecretAuthType(auth)
	if authType == "api_key" && strings.TrimSpace(auth.KeyName) != "" {
		return strings.TrimSpace(auth.KeyName)
	}
	if authType == "mtls" {
		return "mtls"
	}
	if authType == "oauth" || authType == "oidc" || authType == "bearer" {
		return "Authorization"
	}
	return ""
}

func runSecretList(cmd *cobra.Command, args []string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	if secretListBucketID == "" {
		return fmt.Errorf("flag --list-bucket is required for secret list; use 'bucket <name-or-id> secrets' for bucket-scoped browsing")
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
		return completeSecretFirstArg(toComplete)
	}
	if len(args) == 1 && args[0] != "list" {
		return completeSecretActionArg(toComplete)
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func completeSecretFirstArg(toComplete string) ([]string, cobra.ShellCompDirective) {
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

func completeSecretActionArg(toComplete string) ([]string, cobra.ShellCompDirective) {
	actions := []string{"set", "remove"}
	var matches []string
	for _, a := range actions {
		if strings.HasPrefix(a, toComplete) {
			matches = append(matches, a)
		}
	}
	return matches, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	RootCmd.AddCommand(secretCmd)

	// Flags for the "set" action
	secretCmd.Flags().StringVar(&secretSetBucketID, "bucket", "", "Set secret as an override for a specific Bucket (name or ID) (for 'set' action)")
	secretCmd.Flags().StringVar(&secretSetExpiresAt, "expires-at", "", "RFC3339 expiry timestamp (e.g. 2026-12-31T23:59:59Z); omit for no expiry (for 'set' action)")
	secretCmd.Flags().StringVar(&secretSetType, "type", "", "Specify the logical authentication method name (e.g., bearerAuth) (for 'set' action)")
	secretCmd.Flags().BoolVarP(&secretSetInteractive, "interactive", "i", false, "Interactive mode to prompt for service's supported authentication methods (for 'set' action)")

	// Flags for the "list" action
	secretCmd.Flags().StringVar(&secretListBucketID, "list-bucket", "", "Filter secrets by Bucket (name or ID) (for 'list' action)")

	// Flags for the "remove" action
	secretCmd.Flags().StringVar(&secretRemoveBucketID, "remove-bucket", "", "Remove override secret for a specific Bucket (name or ID) (for 'remove' action)")
}
