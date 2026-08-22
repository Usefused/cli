package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
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
var secretSetAuthName string
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
	if strings.TrimSpace(secretSetAuthName) != "" && strings.TrimSpace(secretSetType) == "" {
		return fmt.Errorf("--auth-name requires --type")
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
		return runSecretSet(cmd, args[0], value)
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
	return setSecretForAuth(client, info.ID, bucketID, auth, value, expiresAt, serviceSlug, cmd.OutOrStdout(), credentialMutationOptions{
		auditCtx: cmd.Context(), auditAction: cmd.CommandPath(), resourceKind: "secret",
	})
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

// selectSecretAuthByType uses --type for the public auth family and
// --auth-name for the exact provider scheme. Keeping those identities
// separate prevents two same-family schemes from silently sharing secrets.
func selectSecretAuthByType(info *api.ServiceInfo, serviceSlug string) (*api.AuthConfig, error) {
	requested := canonicalSecretTypeName(secretSetType)
	matches := make([]*api.AuthConfig, 0, len(info.AuthConfigs))
	for i := range info.AuthConfigs {
		auth := &info.AuthConfigs[i]
		if canonicalSecretAuthType(auth) == requested {
			matches = append(matches, auth)
		}
	}
	if len(matches) == 0 {
		return nil, unsupportedSecretAuthError(info, serviceSlug)
	}
	name := strings.TrimSpace(secretSetAuthName)
	if name != "" {
		for _, auth := range matches {
			if auth.Name == name {
				return auth, nil
			}
		}
		return nil, fmt.Errorf("service %s does not declare auth_name %q for auth type %q; matching names are: %s", serviceSlug, name, requested, strings.Join(secretAuthNames(matches), ", "))
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return nil, fmt.Errorf("service %s declares multiple %s authentication schemes; pass --auth-name with one of: %s", serviceSlug, requested, strings.Join(secretAuthNames(matches), ", "))
}

func unsupportedSecretAuthError(info *api.ServiceInfo, serviceSlug string) error {
	valid := make([]string, 0, len(info.AuthConfigs))
	for i := range info.AuthConfigs {
		valid = append(valid, canonicalSecretAuthType(&info.AuthConfigs[i])+":"+info.AuthConfigs[i].Name)
	}
	return fmt.Errorf("service %s does not support authentication type %q; valid type:name pairs are: %s", serviceSlug, secretSetType, strings.Join(valid, ", "))
}

func secretAuthNames(auths []*api.AuthConfig) []string {
	names := make([]string, 0, len(auths))
	for _, auth := range auths {
		names = append(names, auth.Name)
	}
	return names
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

type secretCredentialInput struct {
	username string
	password string
	cert     string
	key      string
	token    string
}

var collectSecretCredentialInput = resolveSecretCredentialInput

// setSecretForAuth is the one static-credential mutation path used by both
// `secret set` and interactive SDK planning. Keeping collection, provider key
// naming, validation, persistence, output, and audit together prevents a plan
// convenience flow from becoming a second credential implementation.
func setSecretForAuth(client *api.Client, serviceID, bucketID string, auth *api.AuthConfig, value string, expiresAt *time.Time, serviceDisplay string, out io.Writer, mutation credentialMutationOptions) error {
	input, err := collectSecretCredentialInput(auth, value)
	if err != nil {
		return err
	}
	if err := authorizeCredentialMutation(mutation); err != nil {
		return err
	}
	if err := persistSecretCredential(client, serviceID, bucketID, auth, input, expiresAt); err != nil {
		return err
	}
	recordCredentialMutation(mutation)
	printSecretSetResult(out, auth, serviceDisplay, expiresAt)
	return nil
}

func resolveSecretCredentialInput(auth *api.AuthConfig, value string) (secretCredentialInput, error) {
	switch canonicalSecretAuthType(auth) {
	case "basic":
		username, password, err := resolveBasicSecretInput(auth.BasicPasswordMode, value)
		return secretCredentialInput{username: username, password: password}, err
	case "mtls":
		cert, key, err := resolveMTLSSecretInput(value)
		return secretCredentialInput{cert: cert, key: key}, err
	default:
		token, err := resolveTokenSecretInput(auth, value)
		return secretCredentialInput{token: token}, err
	}
}

func persistSecretCredential(client *api.Client, serviceID, bucketID string, auth *api.AuthConfig, input secretCredentialInput, expiresAt *time.Time) error {
	name := secretAuthCredentialName(auth)
	switch canonicalSecretAuthType(auth) {
	case "basic":
		return client.UpsertSecrets(bucketID, []api.SecretUpsertRequest{
			{ServiceID: serviceID, KeyName: name + "_username", CredentialType: "basic", Value: input.username, ExpiresAt: expiresAt},
			{ServiceID: serviceID, KeyName: name + "_password", CredentialType: "basic", Value: input.password, ExpiresAt: expiresAt},
		})
	case "mtls":
		return client.UpsertSecrets(bucketID, []api.SecretUpsertRequest{
			{ServiceID: serviceID, KeyName: name + "_cert", CredentialType: "mtls", Value: input.cert, ExpiresAt: expiresAt},
			{ServiceID: serviceID, KeyName: name + "_key", CredentialType: "mtls", Value: input.key, ExpiresAt: expiresAt},
		})
	default:
		return client.UpsertSecret(serviceID, name, canonicalSecretAuthType(auth), input.token, bucketID, expiresAt)
	}
}

func printSecretSetResult(out io.Writer, auth *api.AuthConfig, serviceDisplay string, expiresAt *time.Time) {
	if out == nil {
		out = io.Discard
	}
	if canonicalSecretAuthType(auth) == "basic" {
		fmt.Fprintln(out, "Basic Auth secrets set successfully.")
		return
	}
	if canonicalSecretAuthType(auth) == "mtls" {
		fmt.Fprintln(out, "mTLS secrets set successfully.")
		return
	}
	if expiresAt != nil {
		fmt.Fprintf(out, "Secret set successfully for %s (expires %s).\n", serviceDisplay, expiresAt.Format(time.RFC3339))
		return
	}
	fmt.Fprintf(out, "Secret set successfully for %s.\n", serviceDisplay)
}

func resolveBasicSecretInput(mode api.BasicPasswordMode, value string) (string, string, error) {
	if value != "" {
		pairs := parseInlineKeyValuePairs(value)
		return validateBasicSecretInput(mode, pairs["username"], pairs["password"])
	}
	if err := requireInteractive("provide Basic authentication material using the command's value input"); err != nil {
		return "", "", err
	}
	var username, password string
	if err := huh.NewInput().Title("Username:").Value(&username).Run(); err != nil {
		return "", "", err
	}
	if basicPasswordRequired(mode) || mode == api.BasicPasswordMode("optional") {
		if err := huh.NewInput().Title("Password (optional when supported):").EchoMode(huh.EchoModePassword).Value(&password).Run(); err != nil {
			return "", "", err
		}
	}
	return validateBasicSecretInput(mode, username, password)
}

func validateBasicSecretInput(mode api.BasicPasswordMode, username, password string) (string, string, error) {
	if strings.TrimSpace(username) == "" {
		return "", "", errors.New("basic auth requires a username")
	}
	if basicPasswordRequired(mode) && password == "" {
		return "", "", errors.New("basic auth requires a password; provide format 'username=...;password=...' or use interactive mode (-i)")
	}
	if mode == api.BasicPasswordMode("empty") && password != "" {
		return "", "", errors.New("this Basic authentication scheme requires an empty password")
	}
	return username, password, nil
}

func basicPasswordRequired(mode api.BasicPasswordMode) bool {
	return mode == "" || mode == api.BasicPasswordMode("required")
}

func resolveMTLSSecretInput(value string) (string, string, error) {
	var cert, key string

	if value != "" {
		pairs := parseInlineKeyValuePairs(value)
		cert, key = pairs["cert"], pairs["key"]
		if cert == "" || key == "" {
			return "", "", fmt.Errorf("mTLS auth requires both cert and key. Provide format 'cert=...;key=...' or use interactive mode (-i)")
		}
	} else {
		if err := requireInteractive("provide the certificate and private key using the command's value input"); err != nil {
			return "", "", err
		}
		err := huh.NewText().Title("Client certificate PEM:").Value(&cert).Run()
		if err != nil {
			return "", "", err
		}
		// Private keys must not be echoed even though certificates are safe to
		// review in the multiline editor.
		err = huh.NewInput().Title("Client private key PEM:").EchoMode(huh.EchoModePassword).Value(&key).Run()
		if err != nil {
			return "", "", err
		}
	}

	if err := validateMTLSSecretPair(cert, key); err != nil {
		return "", "", err
	}
	return cert, key, nil
}

// resolveTokenSecretInput masks every token-shaped provider credential while
// leaving exact scheme naming to the shared persistence path.
func resolveTokenSecretInput(auth *api.AuthConfig, value string) (string, error) {
	keyName := secretAuthCredentialName(auth)
	authType := canonicalSecretAuthType(auth)
	promptTitle := fmt.Sprintf("Enter %s:", keyName)

	if authType == "bearer" {
		promptTitle = "Enter Bearer Token:"
	} else if authType == "oauth" {
		promptTitle = "Enter OAuth Token:"
	}

	if value == "" {
		if err := requireInteractive("provide the credential value explicitly"); err != nil {
			return "", err
		}
		err := huh.NewInput().Title(promptTitle).EchoMode(huh.EchoModePassword).Value(&value).Run()
		if err != nil {
			return "", err
		}
	}
	if value == "" {
		return "", errors.New("credential value cannot be empty")
	}
	return value, nil
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
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, secretListFlags)
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
	addJSONOutputFlag(secretListCmd)

	secretSetCmd.Flags().StringVar(&secretSetBucketID, "bucket", "", "Bucket name or UUID; omit to use the default bucket")
	secretSetCmd.Flags().StringVar(&secretSetExpiresAt, "expires-at", "", "RFC3339 expiry timestamp; omit for no expiry")
	secretSetCmd.Flags().StringVar(&secretSetType, "type", "", "Authentication family, such as bearer, api_key, oauth, or mtls")
	secretSetCmd.Flags().StringVar(&secretSetAuthName, "auth-name", "", "Exact provider auth scheme name; required when --type matches multiple schemes")
	secretSetCmd.Flags().BoolVarP(&secretSetInteractive, "interactive", "i", false, "Prompt for the supported authentication method and value")
	secretSetCmd.Flags().BoolVar(&secretSetValueStdin, "value-stdin", false, "Read the credential value from stdin")
	secretListCmd.Flags().StringVar(&secretListBucketID, "bucket", "", "Bucket name or UUID (required)")
	addListFlags(secretListCmd, &secretListFlags)
	secretDeleteCmd.Flags().StringVar(&secretRemoveBucketID, "bucket", "", "Bucket name or UUID; omit to use the default bucket")
}
