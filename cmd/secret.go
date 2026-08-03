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
	Use:   "secret",
	Short: "Manage workspace secrets",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var secretSetInteractive bool
var secretSetValueStdin bool
var secretSetBucketID string
var secretSetExpiresAt string
var secretSetType string
var secretListBucketID string
var secretRemoveBucketID string
var secretListFlags listFlags

func validateSecretSetArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	if secretSetInteractive == secretSetValueStdin {
		return fmt.Errorf("choose exactly one credential input: --interactive or --value-stdin")
	}
	return nil
}

var secretSetCmd = &cobra.Command{
	Use:   "set <service-slug>",
	Short: "Set credentials for a service",
	Args:  validateSecretSetArgs,
	RunE: WithTelemetry("cli.secret.set", func(cmd *cobra.Command, args []string) error {
		value, err := readSecretValue(cmd)
		if err != nil {
			return err
		}
		if err := runSecretSet(cmd, args[0], value); err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), cmd.CommandPath(), "secret")
		return nil
	}),
}

func readSecretValue(cmd *cobra.Command) (string, error) {
	if secretSetInteractive {
		return "", nil
	}
	// Secrets never belong in argv because shell history and process listings
	// can retain them long after the command finishes.
	return readSensitiveValue(cmd, "credential")
}

var secretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secret metadata in a bucket",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.secret.list", func(cmd *cobra.Command, _ []string) error {
		return runSecretList(cmd)
	}),
}

var secretDeleteCmd = &cobra.Command{
	Use:   "delete <service-slug> <key-name>",
	Short: "Delete a service secret",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.secret.delete", func(cmd *cobra.Command, args []string) error {
		if err := runSecretDelete(cmd, args[0], args[1]); err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), cmd.CommandPath(), "secret")
		return nil
	}),
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
	bucketID, err := resolveOptionalBucketID(secretSetBucketID)
	if err != nil {
		return err
	}
	info, err := client.GetServiceInfo(serviceSlug)
	if err != nil {
		return err
	}
	if info == nil || strings.TrimSpace(info.ID) == "" {
		return fmt.Errorf("service %s not found", serviceSlug)
	}
	auth, err := selectSecretAuth(info, serviceSlug)
	if err != nil {
		return err
	}
	authType := canonicalSecretAuthType(auth)
	// Basic auth requires two distinct inputs (username and password) which can't be cleanly parsed from a single positional argument, so we route it to a specialized handler.
	if authType == "basic" {
		return handleBasicSecretSet(client, info.ID, bucketID, auth, value, expiresAt)
	}
	if authType == "mtls" {
		return handleMTLSSecretSet(client, info.ID, bucketID, auth, value, expiresAt)
	}
	return handleTokenSecretSet(client, info.ID, bucketID, auth, value, expiresAt, serviceSlug)
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

func runSecretList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	if secretListBucketID == "" {
		return fmt.Errorf("flag --bucket is required")
	}
	resolvedBucketID, err := resolveExplicitBucketID(secretListBucketID)
	if err != nil {
		return err
	}
	page, err := client.ListSecretMetaPage(resolvedBucketID, secretListFlags.pageOptions())
	if err != nil {
		return err
	}
	for _, s := range page.Items {
		bucket := s.BucketID
		expiry := "never"
		if s.ExpiresAt != nil {
			expiry = s.ExpiresAt.Format("2006-01-02 15:04:05")
			if s.ExpiresAt.Before(time.Now()) {
				expiry += " (EXPIRED)"
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Service: %s, Key: %s, Bucket: %s, Type: %s, Expires: %s, Updated: %s\n", s.ServiceID, s.KeyName, bucket, s.CredentialType, expiry, s.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	printPageSummary(cmd.OutOrStdout(), page.Total, secretListFlags)
	return nil
}

func runSecretDelete(cmd *cobra.Command, serviceSlug, keyName string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	bucketID := secretRemoveBucketID

	resolvedBucketID, err := resolveOptionalBucketID(bucketID)
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
	fmt.Fprintf(cmd.OutOrStdout(), "Secret %q deleted.\n", keyName)
	return nil
}

func resolveOptionalBucketID(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	return resolveExplicitBucketID(value)
}

func init() {
	RootCmd.AddCommand(secretCmd)
	secretCmd.AddCommand(secretSetCmd, secretListCmd, secretDeleteCmd)

	secretSetCmd.Flags().StringVar(&secretSetBucketID, "bucket", "", "Bucket name or UUID; omit to use the default bucket")
	secretSetCmd.Flags().StringVar(&secretSetExpiresAt, "expires-at", "", "RFC3339 expiry timestamp; omit for no expiry")
	secretSetCmd.Flags().StringVar(&secretSetType, "type", "", "Logical authentication method name, such as bearerAuth")
	secretSetCmd.Flags().BoolVarP(&secretSetInteractive, "interactive", "i", false, "Prompt for the supported authentication method and value")
	secretSetCmd.Flags().BoolVar(&secretSetValueStdin, "value-stdin", false, "Read the credential value from stdin")
	secretListCmd.Flags().StringVar(&secretListBucketID, "bucket", "", "Bucket name or UUID (required)")
	addListFlags(secretListCmd, &secretListFlags)
	secretDeleteCmd.Flags().StringVar(&secretRemoveBucketID, "bucket", "", "Bucket name or UUID; omit to use the default bucket")
}
