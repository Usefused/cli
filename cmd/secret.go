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

// selectSecretAuthInteractively is replaceable in tests so secure method
// selection can be verified without attaching a real terminal.
var selectSecretAuthInteractively = promptSecretAuthSelect

// validateSecretSetArgs preserves secure terminal prompting by default while
// requiring explicit stdin input whenever prompting has been disabled.
func validateSecretSetArgs(cmd *cobra.Command, args []string) error {
	// Positional validation runs first so credential values can never be accepted in argv.
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	// The two explicit input modes are mutually exclusive even though terminal
	// prompting no longer needs a flag.
	if secretSetInteractive && secretSetValueStdin {
		return fmt.Errorf("choose only one credential input: --interactive or --value-stdin")
	}
	// An explicit interaction request must fail rather than silently consuming stdin in automation.
	if secretSetInteractive {
		if err := requireInteractive("omit --interactive and use --value-stdin, or unset --no-input/CI"); err != nil {
			return err
		}
	}
	// Non-interactive execution needs the dedicated secret-safe stdin channel.
	if !secretSetValueStdin && nonInteractive() {
		return errors.New("credential input is required in non-interactive mode; use --value-stdin")
	}
	// Exact provider scheme selection cannot be interpreted without its public auth family.
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

// readSecretValue defers collection to the provider-aware secure prompts for
// terminal use and otherwise reads the dedicated stdin channel exactly once.
func readSecretValue(cmd *cobra.Command) (string, error) {
	// Interactive collectors need an empty seed so they can request the fields
	// appropriate to the selected authentication method.
	if secretSetUsesInteractiveInput() {
		return "", nil
	}
	// Secrets never belong in argv because shell history and process listings
	// can retain them long after the command finishes.
	return readSensitiveValue(cmd, "credential")
}

// secretSetUsesInteractiveInput makes prompting the normal terminal path while
// allowing --value-stdin, --no-input, and CI to remain deterministic.
func secretSetUsesInteractiveInput() bool {
	return !secretSetValueStdin && !nonInteractive()
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
	requestedType := canonicalSecretTypeName(secretSetType)
	// Local assignment validation must finish before any metadata request can carry ambiguous input.
	if err := validateSecretInlineInput(requestedType, value); err != nil {
		return err
	}
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

// validateSecretInlineInput checks only explicit multi-field credential
// grammars while preserving opaque token values for provider-aware handling.
func validateSecretInlineInput(requestedType, value string) error {
	// Empty or opaque single-value input needs no assignment parsing at this stage.
	if value == "" || requestedType != "basic" && requestedType != "mtls" && requestedType != "oauth" && requestedType != "oidc" {
		return nil
	}
	// Basic may intentionally author an empty password only when the later reviewed auth contract requires it.
	if requestedType == "basic" {
		_, err := parseInlineKeyValuePairsAllowingEmpty(value, "password")
		return err
	}
	// Paired families require valid assignments before remote metadata lookup.
	_, err := parseInlineKeyValuePairs(value)
	return err
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
	// Non-interactive stdin may safely auto-select a sole method, but ambiguous
	// services still require an exact public family from the caller.
	if !secretSetUsesInteractiveInput() {
		// A sole declared method is deterministic and needs no synthetic choice.
		if len(info.AuthConfigs) == 1 {
			return &info.AuthConfigs[0], nil
		}
		var validTypes []string
		for _, a := range info.AuthConfigs {
			validTypes = append(validTypes, a.Name)
		}
		return nil, fmt.Errorf("service %s has multiple authentication methods; specify --type. Valid methods are: %s. Run 'fused-cli service %s show' for details", serviceSlug, strings.Join(validTypes, ", "), serviceSlug)
	}
	// Prompting remains gated at the point of use in case this function is called outside Cobra validation.
	if err := requireInteractive("pass --type and provide the credential value explicitly"); err != nil {
		return nil, err
	}
	return selectSecretAuthInteractively(info)
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
	username     string
	password     string
	cert         string
	key          string
	clientID     string
	clientSecret string
	token        string
}

var collectSecretCredentialInput = resolveSecretCredentialInput

// setSecretForAuth is the one static-credential mutation path used by both
// `secret set` and interactive SDK planning. Optional setup is accepted before
// collecting credentials so an operator can keep a valid plan without supplying
// material. Explicit secret writes retain the same validation and persistence.
func setSecretForAuth(client *api.Client, serviceID, bucketID string, auth *api.AuthConfig, value string, expiresAt *time.Time, serviceDisplay string, out io.Writer, mutation credentialMutationOptions) error {
	// Declining optional setup must not first require a valid credential pair.
	if err := authorizeCredentialMutation(mutation); err != nil {
		return err
	}
	input, err := collectSecretCredentialInput(auth, value)
	// After opting in, invalid or cancelled input must never reach storage.
	if err != nil {
		return err
	}
	// A failed write remains visible; it cannot be treated as skipped setup.
	if err := persistSecretCredential(client, serviceID, bucketID, auth, input, expiresAt); err != nil {
		return err
	}
	recordCredentialMutation(mutation)
	printSecretSetResult(out, auth, serviceDisplay, expiresAt)
	return nil
}

// resolveSecretCredentialInput routes each declared family through its one secure collector.
func resolveSecretCredentialInput(auth *api.AuthConfig, value string) (secretCredentialInput, error) {
	// Family dispatch prevents OAuth application pairs from entering the opaque token path.
	switch canonicalSecretAuthType(auth) {
	case "basic":
		username, password, err := resolveBasicSecretInput(auth.BasicPasswordMode, value)
		return secretCredentialInput{username: username, password: password}, err
	case "mtls":
		cert, key, err := resolveMTLSSecretInput(value)
		return secretCredentialInput{cert: cert, key: key}, err
	case "oauth", "oidc":
		clientID, clientSecret, err := resolveOAuthApplicationSecretInput(value)
		return secretCredentialInput{clientID: clientID, clientSecret: clientSecret}, err
	default:
		token, err := resolveTokenSecretInput(auth, value)
		return secretCredentialInput{token: token}, err
	}
}

// persistSecretCredential maps validated input to the atomic API contract owned by its family.
func persistSecretCredential(client *api.Client, serviceID, bucketID string, auth *api.AuthConfig, input secretCredentialInput, expiresAt *time.Time) error {
	name := secretAuthCredentialName(auth)
	// Paired families use one bulk transaction while single values retain point writes.
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
	case "oauth", "oidc":
		return client.UpsertCredentialFamily(bucketID, api.CredentialFamilyUpsertRequest{
			ServiceID: serviceID, CredentialType: canonicalSecretAuthType(auth), AuthName: name,
			Values: map[string]string{"client_id": input.clientID, "client_secret": input.clientSecret}, ExpiresAt: expiresAt,
		})
	default:
		return client.UpsertSecret(serviceID, name, canonicalSecretAuthType(auth), input.token, bucketID, expiresAt)
	}
}

// printSecretSetResult confirms the credential family without echoing names or secret material.
func printSecretSetResult(out io.Writer, auth *api.AuthConfig, serviceDisplay string, expiresAt *time.Time) {
	// A nil writer is valid for silent interactive remediation paths.
	if out == nil {
		out = io.Discard
	}
	// Family-specific messages make atomic pair behavior visible to the caller.
	if canonicalSecretAuthType(auth) == "basic" {
		fmt.Fprintln(out, "Basic Auth secrets set successfully.")
		return
	}
	if canonicalSecretAuthType(auth) == "mtls" {
		fmt.Fprintln(out, "mTLS secrets set successfully.")
		return
	}
	if authType := canonicalSecretAuthType(auth); authType == "oauth" || authType == "oidc" {
		fmt.Fprintln(out, "OAuth application credentials set successfully.")
		return
	}
	// Expiry is shown only for single credentials because paired success already has a dedicated result.
	if expiresAt != nil {
		fmt.Fprintf(out, "Secret set successfully for %s (expires %s).\n", serviceDisplay, expiresAt.Format(time.RFC3339))
		return
	}
	fmt.Fprintf(out, "Secret set successfully for %s.\n", serviceDisplay)
}

// resolveOAuthApplicationSecretInput collects the two application-registration values as one mutation.
func resolveOAuthApplicationSecretInput(value string) (string, string, error) {
	// Structured non-interactive input shares the assignment parser used by Basic and mTLS.
	if value != "" {
		pairs, err := parseInlineKeyValuePairs(value)
		if err != nil {
			return "", "", err
		}
		return validateOAuthApplicationSecretInput(pairs["client_id"], pairs["client_secret"], len(pairs))
	}
	if err := requireInteractive("provide client_id and client_secret using the command's value input"); err != nil {
		return "", "", err
	}
	var clientID, clientSecret string
	if err := huh.NewInput().Title("OAuth client ID:").Value(&clientID).Run(); err != nil {
		return "", "", err
	}
	// Client secrets are always masked even though client identifiers are visible provider metadata.
	if err := huh.NewInput().Title("OAuth client secret:").EchoMode(huh.EchoModePassword).Value(&clientSecret).Run(); err != nil {
		return "", "", err
	}
	return validateOAuthApplicationSecretInput(clientID, clientSecret, 2)
}

// validateOAuthApplicationSecretInput keeps the accepted structured shape closed and complete.
func validateOAuthApplicationSecretInput(clientID, clientSecret string, fieldCount int) (string, string, error) {
	// Redirects and token fields are Engine-managed or user-grant material and cannot enter this family.
	if fieldCount != 2 || strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return "", "", errors.New("OAuth/OIDC application credentials require exactly 'client_id=...;client_secret=...'")
	}
	return clientID, clientSecret, nil
}

// resolveBasicSecretInput collects and validates Basic-auth credential fields.
func resolveBasicSecretInput(mode api.BasicPasswordMode, value string) (string, string, error) {
	// Explicit Basic credentials must satisfy the shared assignment grammar before field validation.
	if value != "" {
		var pairs map[string]string
		var err error
		// The reviewed empty-password mode is the sole exception to global blank-value rejection.
		if mode == api.BasicPasswordMode("empty") {
			pairs, err = parseInlineKeyValuePairsAllowingEmpty(value, "password")
		} else {
			pairs, err = parseInlineKeyValuePairs(value)
		}
		// Parser failures are already actionable and must not be replaced by a generic missing-field error.
		if err != nil {
			return "", "", err
		}
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

// resolveMTLSSecretInput collects and validates both halves of an mTLS credential.
func resolveMTLSSecretInput(value string) (string, string, error) {
	var cert, key string

	if value != "" {
		pairs, err := parseInlineKeyValuePairs(value)
		// Invalid inline mTLS assignments must fail before required-field validation obscures the cause.
		if err != nil {
			return "", "", err
		}
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
	secretSetCmd.Flags().BoolVarP(&secretSetInteractive, "interactive", "i", false, "Explicitly require authentication prompts (the terminal default)")
	secretSetCmd.Flags().BoolVar(&secretSetValueStdin, "value-stdin", false, "Read the credential value from stdin")
	secretListCmd.Flags().StringVar(&secretListBucketID, "bucket", "", "Bucket name or UUID (required)")
	addListFlags(secretListCmd, &secretListFlags)
	secretDeleteCmd.Flags().StringVar(&secretRemoveBucketID, "bucket", "", "Bucket name or UUID; omit to use the default bucket")
}
