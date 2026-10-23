package cmd

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

type connectOptions struct {
	bucket, authType, authName, expires string
}

// newConnectCommand keeps service credential setup independent of app creation and OAuth user consent.
func newConnectCommand() *cobra.Command {
	options := &connectOptions{}
	cmd := &cobra.Command{Use: "connect <service-name> [name=value,...]", Short: "Configure service authentication in a bucket", Args: cobra.RangeArgs(1, 2)}
	// Each invocation resolves its own flags without changing the legacy credential command's state.
	cmd.RunE = WithTelemetry("cli.connect", func(cmd *cobra.Command, args []string) error { return runConnect(cmd, args[0], args[1:], *options) })
	cmd.Flags().StringVar(&options.bucket, "bucket", "default", "Bucket name or UUID")
	cmd.Flags().StringVar(&options.authType, "type", "", "Authentication family, such as api_key, bearer, basic, oauth, oidc, or mtls")
	cmd.Flags().StringVar(&options.authName, "auth-name", "", "Exact authentication scheme name")
	cmd.Flags().StringVar(&options.expires, "expires-at", "", "Optional RFC3339 credential expiry")
	addJSONOutputFlag(cmd)
	return cmd
}

// runConnect exposes credential status before selection and writes only the method the caller chooses.
func runConnect(cmd *cobra.Command, service string, assignments []string, options connectOptions) error {
	fields, err := parseConnectAssignments(assignments)
	// Malformed assignments must fail before resolving or mutating a bucket.
	if err != nil {
		return err
	}
	expires, err := parseBucketSecretExpiresAt(options.expires)
	// Invalid lifecycle metadata must fail before credentials are collected.
	if err != nil {
		return err
	}
	client, bucketID, err := bucketClientAndID(options.bucket)
	// A denied bucket must never trigger default fallback or bucket creation.
	if err != nil {
		return err
	}
	info, choices, err := connectServiceChoices(client, service, options)
	// Only validated service metadata may determine a credential target.
	if err != nil {
		return err
	}
	available, err := loadConnectAuthOptions(client, bucketID, info.ID, choices)
	// Inspection errors must not be treated as permission to overwrite credentials.
	if err != nil {
		return err
	}
	selected, err := chooseConnectAuth(available, connectUsesAutomation(cmd))
	// Cancellation and ambiguous automation leave every credential unchanged.
	if err != nil {
		return err
	}
	auth := &selected.auth
	// Unattended inspection without new values may reuse a complete method; explicit input requests replacement.
	if connectUsesAutomation(cmd) && len(fields) == 0 && selected.status == "Configured" {
		return writeConnectResult(cmd, bucketID, auth, "configured")
	}
	input, err := connectCredentialInput(cmd, auth, fields)
	// Invalid or cancelled collection cannot reach the atomic credential write.
	if err != nil {
		return err
	}
	// Paired credentials retain the existing all-or-nothing persistence contract.
	if err := persistSecretCredential(client, info.ID, bucketID, auth, input, expires); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "secret")
	return writeConnectResult(cmd, bucketID, auth, "saved")
}

// connectServiceChoices resolves provider metadata and exact auth selectors before any credential lookup.
func connectServiceChoices(client *api.Client, service string, options connectOptions) (*api.ServiceInfo, []api.AuthConfig, error) {
	info, err := client.GetServiceInfo(service)
	// Transport and permission failures cannot masquerade as missing authentication.
	if err != nil {
		return nil, nil, err
	}
	// Only authoritative service identities can own credential storage keys.
	if info == nil || info.ID == "" {
		return nil, nil, fmt.Errorf("service not found")
	}
	choices, err := connectAuthChoices(info, options)
	return info, choices, err
}

// connectUsesAutomation reserves silent selection for unattended or machine-readable invocations.
func connectUsesAutomation(cmd *cobra.Command) bool {
	return nonInteractive() || wantsJSON(cmd)
}

// connectAuthChoices applies exact selectors before checking existing configuration or opening the menu.
func connectAuthChoices(info *api.ServiceInfo, options connectOptions) ([]api.AuthConfig, error) {
	var choices []api.AuthConfig
	for _, auth := range info.AuthConfigs {
		// An explicit family or scheme narrows the candidates and never changes their storage identities.
		if options.authType != "" && canonicalSecretAuthType(&auth) != canonicalSecretTypeName(options.authType) {
			continue
		}
		if options.authName != "" && auth.Name != options.authName {
			continue
		} // Same-family schemes remain distinct.
		choices = append(choices, auth)
	}
	// An empty selection is an input error, including services without declared authentication.
	if len(choices) == 0 {
		return nil, fmt.Errorf("no matching authentication methods; inspect the service auth methods or adjust --type/--auth-name")
	}
	return choices, nil
}

// connectAuthConfigured treats partial pairs and expired credentials as needing setup.
func connectAuthConfigured(metadata api.SecretMetaResponse, serviceID string, auth api.AuthConfig, now time.Time) bool {
	name, family := secretAuthCredentialName(&auth), canonicalSecretAuthType(&auth)
	// Never reuse a similarly named credential from another service or auth family.
	if metadata.ServiceID != serviceID || metadata.KeyName != name || canonicalSecretTypeName(metadata.CredentialType) != family {
		return false
	}
	// Expired material cannot satisfy a new connection setup.
	if metadata.ExpiresAt != nil && !metadata.ExpiresAt.After(now) {
		return false
	}
	suffixes := connectAuthKeySuffixes(family)
	for _, suffix := range suffixes {
		// Family metadata must contain both members before paired authentication counts as configured.
		if !slices.Contains(metadata.KeyNames, name+suffix) {
			return false
		}
	}
	return true
}

// connectAuthKeySuffixes mirrors the existing atomic credential-family contract for presence checks only.
func connectAuthKeySuffixes(family string) []string {
	switch family { // Single-token families need only their exact metadata row; paired families need both keys.
	case "basic":
		return []string{"_username", "_password"}
	case "mtls":
		return []string{"_cert", "_key"}
	case "oauth", "oidc":
		return []string{"_client_id", "_client_secret"}
	default:
		return nil
	}
}

// writeConnectResult reports only auth metadata, never the supplied credential or field values.
func writeConnectResult(cmd *cobra.Command, bucketID string, auth *api.AuthConfig, status string) error {
	result := map[string]string{"bucket_id": bucketID, "auth_type": canonicalSecretAuthType(auth), "auth_name": auth.Name, "status": status}
	// Structured output is suitable for automation regardless of whether a write was necessary.
	if wantsJSON(cmd) {
		return writeJSON(cmd, result)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s authentication (%s) in bucket %s.\n", strings.ToUpper(status[:1])+status[1:], result["auth_type"], auth.Name, bucketID)
	return err
}

// init exposes one direct credential-setup entry point without changing service OAuth consent commands.
func init() { RootCmd.AddCommand(newConnectCommand()) }
