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

var secretListBucketID string
var secretRemoveBucketID string
var secretListFlags listFlags

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

type secretCredentialInput struct {
	exportable   bool
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
// `connect` and interactive SDK planning. Optional setup is accepted before
// collecting credentials so an operator can keep a valid plan without supplying
// material. Explicit secret writes retain the same validation and persistence.
func setSecretForAuth(client *api.Client, serviceID, bucketID string, auth *api.AuthConfig, value string, expiresAt *time.Time, serviceDisplay string, out io.Writer, mutation credentialMutationOptions) error {
	// Exportability is restricted before prompting, so unsupported families never collect input.
	if mutation.exportable && canonicalSecretAuthType(auth) != "api_key" {
		return fmt.Errorf("--exportable supports only API keys")
	}
	// Declining optional setup must not first require a valid credential pair.
	if err := authorizeCredentialMutation(mutation); err != nil {
		return err
	}
	input, err := collectSecretCredentialInput(auth, value)
	// After opting in, invalid or cancelled input must never reach storage.
	if err != nil {
		return err
	}
	input.exportable = mutation.exportable
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
		username, password, err := resolveBasicSecretInput(auth.BasicPasswordMode)
		return secretCredentialInput{username: username, password: password}, err
	case "mtls":
		cert, key, err := resolveMTLSSecretInput()
		return secretCredentialInput{cert: cert, key: key}, err
	case "oauth", "oidc":
		clientID, clientSecret, err := resolveOAuthApplicationSecretInput()
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
		return client.UpsertSecret(serviceID, name, canonicalSecretAuthType(auth), input.token, bucketID, expiresAt, input.exportable)
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

// resolveOAuthApplicationSecretInput prompts for the two application-registration values as one atomic mutation.
func resolveOAuthApplicationSecretInput() (string, string, error) {
	if err := requireInteractive("pass \"client_id=..., client_secret=...\" with --no-input"); err != nil {
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
		return "", "", errors.New("OAuth/OIDC application credentials require exactly client_id and client_secret")
	}
	return clientID, clientSecret, nil
}

// resolveBasicSecretInput prompts for and validates Basic-auth credential fields.
func resolveBasicSecretInput(mode api.BasicPasswordMode) (string, string, error) {
	if err := requireInteractive("pass \"username=..., password=...\" with --no-input"); err != nil {
		return "", "", err
	}
	var username, password string
	if err := huh.NewInput().Title("Username:").Value(&username).Run(); err != nil {
		return "", "", err
	}
	// Skip the password prompt when the provider scheme explicitly prohibits passwords.
	if basicPasswordRequired(mode) || mode == api.BasicPasswordMode("optional") {
		if err := huh.NewInput().Title("Password (optional when supported):").EchoMode(huh.EchoModePassword).Value(&password).Run(); err != nil {
			return "", "", err
		}
	}
	return validateBasicSecretInput(mode, username, password)
}

// validateBasicSecretInput enforces the scheme's username and password policy before storage.
func validateBasicSecretInput(mode api.BasicPasswordMode, username, password string) (string, string, error) {
	if strings.TrimSpace(username) == "" {
		return "", "", errors.New("basic auth requires a username")
	}
	if basicPasswordRequired(mode) && password == "" {
		return "", "", errors.New("basic auth requires a password")
	}
	if mode == api.BasicPasswordMode("empty") && password != "" {
		return "", "", errors.New("this Basic authentication scheme requires an empty password")
	}
	return username, password, nil
}

func basicPasswordRequired(mode api.BasicPasswordMode) bool {
	return mode == "" || mode == api.BasicPasswordMode("required")
}

// resolveMTLSSecretInput prompts for and validates both halves of an mTLS credential.
func resolveMTLSSecretInput() (string, string, error) {
	var cert, key string
	if err := requireInteractive("pass \"cert=..., key=...\" with --no-input"); err != nil {
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

// runSecretList projects identity and export policy while keeping credential values private.
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
		fmt.Fprintf(cmd.OutOrStdout(), "ID: %s, Exportable: %t, Service: %s, Key: %s, Bucket: %s, Type: %s, Expires: %s, Updated: %s\n", s.ID, s.Exportable, s.ServiceID, s.KeyName, bucket, s.CredentialType, expiry, s.UpdatedAt.Format("2006-01-02 15:04:05"))
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

// init registers resource-specific flags before the public command tree is assembled.
func init() {
	RootCmd.AddCommand(secretCmd)
	secretCmd.AddCommand(secretListCmd, secretDeleteCmd)
	addJSONOutputFlag(secretListCmd)
	addListFlags(secretListCmd, &secretListFlags)
	secretDeleteCmd.Flags().StringVar(&secretRemoveBucketID, "bucket", "", "Bucket name or UUID; omit to use the default bucket")
}
