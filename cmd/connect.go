package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// connectCmd registers, rotates, or reads back a bucket's OAuth/OIDC app
// registration (client_id/client_secret/redirect_uri) directly against the
// Engine admin endpoint. The values stay outside declarative config; explicit
// interactive SDK planning may call this same mutation path after readiness
// reports them missing. See fused-bucket for how registration differs from
// `secret set` and an end-user `workspace service connect <slug>` flow.
var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Register, rotate, or check a bucket's OAuth/OIDC app registration for a service",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var connectSetInteractive bool
var connectSetValueStdin bool
var connectSetBucketID string
var connectGetBucketID string
var connectSetType string
var connectSetAuthName string

var connectSetCmd = &cobra.Command{
	Use:   "set <service-slug>",
	Short: "Register or rotate an OAuth/OIDC app",
	Args:  validateConnectSetArgs,
	RunE: WithTelemetry("cli.connect.set", func(cmd *cobra.Command, args []string) error {
		value, err := readConnectValue(cmd)
		if err != nil {
			return err
		}
		return runConnectSet(cmd, args[0], value)
	}),
}

var connectGetCmd = &cobra.Command{
	Use:   "get <service-slug>",
	Short: "Read the safe OAuth/OIDC app projection",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.connect.get", func(cmd *cobra.Command, args []string) error {
		return runConnectGet(cmd, args[0])
	}),
}

func validateConnectSetArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	if connectSetInteractive == connectSetValueStdin {
		return fmt.Errorf("choose exactly one credential input: --interactive or --value-stdin")
	}
	if strings.TrimSpace(connectSetAuthName) != "" && strings.TrimSpace(connectSetType) == "" {
		return fmt.Errorf("--auth-name requires --type")
	}
	return nil
}

func readConnectValue(cmd *cobra.Command) (string, error) {
	if connectSetInteractive {
		return "", nil
	}
	return readSensitiveValue(cmd, "connect config")
}

// resolveConnectTarget resolves the client/bucket/service triple both set
// and get need, so the two actions can never disagree about which
// bucket+service a slug refers to -- one resolution path, not two that could
// drift.
func resolveConnectTarget(action, serviceSlug, bucketValue string) (client *api.Client, bucketID, serviceID string, err error) {
	client, err = getAPIClient()
	if err != nil {
		return nil, "", "", err
	}
	if strings.TrimSpace(bucketValue) == "" {
		return nil, "", "", fmt.Errorf("connect %s requires --bucket", action)
	}
	bucketID, err = resolveExplicitBucketID(bucketValue)
	if err != nil {
		return nil, "", "", err
	}
	serviceID, err = resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return nil, "", "", err
	}
	return client, bucketID, serviceID, nil
}

// runConnectSet resolves the bucket, service, and this service's connect-
// capable auth scheme before building the request, then hands off to
// connectSetFields for the inline-vs-interactive split -- one field can be
// rotated (e.g. redirect_uri) without resupplying the others; Engine's
// partial-update merge (see UpsertConnectConfigHandler) carries forward
// whatever this call omits.
func runConnectSet(cmd *cobra.Command, serviceSlug, value string) error {
	// Validate stdin before resolving remote metadata so malformed credentials never trigger a request.
	if value != "" {
		// Stop immediately on unknown as well as malformed assignments so a typo cannot become a no-op mutation.
		if _, err := parseConnectInlineFields(value); err != nil {
			return err
		}
	}
	client, bucketID, serviceID, err := resolveConnectTarget("set", serviceSlug, connectSetBucketID)
	if err != nil {
		return err
	}
	info, err := client.GetServiceInfo(serviceSlug)
	if err != nil {
		return err
	}
	authType, authName, err := selectConnectAuthType(info, serviceSlug)
	if err != nil {
		return err
	}
	return setConnectConfig(client, bucketID, serviceID, authType, authName, value, cmd.OutOrStdout(), credentialMutationOptions{
		auditCtx: cmd.Context(), auditAction: cmd.CommandPath(), resourceKind: "connect_config",
	})
}

var collectConnectSetFields = connectSetFields

// setConnectConfig is shared by the explicit connect command and SDK plan
// remediation so both paths preserve partial-update rules, masked client-secret
// input, the safe response projection, and mutation audit semantics.
func setConnectConfig(client *api.Client, bucketID, serviceID, authType, authName, value string, out io.Writer, mutation credentialMutationOptions) error {
	fields, err := collectConnectSetFields(authType, authName, value)
	if err != nil {
		return err
	}
	if err := authorizeCredentialMutation(mutation); err != nil {
		return err
	}
	saved, err := client.UpsertConnectConfig(bucketID, serviceID, fields)
	if err != nil {
		return err
	}
	recordCredentialMutation(mutation)
	printConnectConfigResult(out, saved)
	return nil
}

// runConnectGet reads back whatever `set` last saved, if anything. Unlike
// set, it needs no auth-type selection -- it just asks Engine for whatever
// is on record for this bucket+service, or reports that nothing is.
func runConnectGet(cmd *cobra.Command, serviceSlug string) error {
	client, bucketID, serviceID, err := resolveConnectTarget("get", serviceSlug, connectGetBucketID)
	if err != nil {
		return err
	}
	cfg, err := client.GetConnectConfig(bucketID, serviceID)
	if err != nil {
		if errors.Is(err, api.ErrConnectConfigNotFound) {
			return fmt.Errorf("no connect config registered for service %s in this bucket -- see `fused-cli connect set %s`", serviceSlug, serviceSlug)
		}
		return err
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, cfg)
	}
	printConnectConfigResult(cmd.OutOrStdout(), cfg)
	return nil
}

// selectConnectAuthType narrows a service's declared auth schemes to the two
// interactive-flow-capable types -- connect only supports oauth/oidc
// (basic/api_key/bearer/mtls have no browser consent step, see
// fused-config's auth/connect/profile split). Reuses the same --type
// disambiguation secret.go's static-credential path already has, so a
// service declaring both oauth and oidc is resolved the same familiar way.
func selectConnectAuthType(info *api.ServiceInfo, serviceSlug string) (string, string, error) {
	candidates := make([]*api.AuthConfig, 0, len(info.AuthConfigs))
	for i := range info.AuthConfigs {
		if t := canonicalSecretAuthType(&info.AuthConfigs[i]); t == "oauth" || t == "oidc" {
			candidates = append(candidates, &info.AuthConfigs[i])
		}
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("service %s does not declare an oauth or oidc auth scheme -- connect only supports interactive OAuth/OIDC flows", serviceSlug)
	}
	if strings.TrimSpace(connectSetType) == "" {
		if len(candidates) == 1 {
			return connectAuthIdentity(candidates[0])
		}
		return "", "", fmt.Errorf("service %s supports multiple connect-capable auth schemes; specify --type and, for repeated types, --auth-name", serviceSlug)
	}
	matches := matchingConnectAuths(candidates, canonicalSecretTypeName(connectSetType))
	if len(matches) == 0 {
		return "", "", fmt.Errorf("service %s does not support connect auth type %q", serviceSlug, connectSetType)
	}
	return selectNamedConnectAuth(matches, serviceSlug)
}

func matchingConnectAuths(candidates []*api.AuthConfig, authType string) []*api.AuthConfig {
	matches := make([]*api.AuthConfig, 0, len(candidates))
	for _, auth := range candidates {
		if canonicalSecretAuthType(auth) == authType {
			matches = append(matches, auth)
		}
	}
	return matches
}

func selectNamedConnectAuth(matches []*api.AuthConfig, serviceSlug string) (string, string, error) {
	name := strings.TrimSpace(connectSetAuthName)
	if name != "" {
		for _, auth := range matches {
			if auth.Name == name {
				return connectAuthIdentity(auth)
			}
		}
		return "", "", fmt.Errorf("service %s does not declare connect auth_name %q for auth type %q", serviceSlug, name, connectSetType)
	}
	if len(matches) == 1 {
		return connectAuthIdentity(matches[0])
	}
	return "", "", fmt.Errorf("service %s declares multiple %s connect schemes; pass --auth-name with one of: %s", serviceSlug, connectSetType, strings.Join(secretAuthNames(matches), ", "))
}

func connectAuthIdentity(auth *api.AuthConfig) (string, string, error) {
	return canonicalSecretAuthType(auth), strings.TrimSpace(auth.Name), nil
}

// connectSetFields resolves either validated inline assignments or interactive
// prompts while preserving omitted fields for partial credential rotation.
func connectSetFields(authType, authName, value string) (api.ConnectConfigUpsertRequest, error) {
	// Non-empty input is an explicit inline mode and must satisfy the shared assignment grammar.
	if value != "" {
		return connectFieldsFromInline(authType, authName, value)
	}
	// Interactive collection is only safe when a terminal is available.
	if err := requireInteractive("provide connect fields in the value argument"); err != nil {
		return api.ConnectConfigUpsertRequest{}, err
	}
	return connectFieldsFromPrompts(authType, authName)
}

// connectFieldsFromInline maps validated inline fields to pointer-valued patch fields.
func connectFieldsFromInline(authType, authName, value string) (api.ConnectConfigUpsertRequest, error) {
	pairs, err := parseConnectInlineFields(value)
	// Parser errors must stop request construction rather than producing an empty partial update.
	if err != nil {
		return api.ConnectConfigUpsertRequest{}, err
	}
	req := connectAuthRequest(authType, authName)
	// Omitted client IDs remain nil so partial rotations leave stored values unchanged.
	if v, ok := pairs["client_id"]; ok {
		req.ClientID = &v
	}
	// Omitted client secrets remain nil so rotating another field cannot erase them.
	if v, ok := pairs["client_secret"]; ok {
		req.ClientSecret = &v
	}
	// Omitted redirect URIs remain nil so callers can rotate only sensitive fields.
	if v, ok := pairs["redirect_uri"]; ok {
		req.RedirectURI = &v
	}
	return req, nil
}

// parseConnectInlineFields admits only fields implemented by the connect
// patch contract so misspellings cannot be silently discarded.
func parseConnectInlineFields(value string) (map[string]string, error) {
	pairs, err := parseInlineKeyValuePairs(value)
	// Shared grammar errors remain the most specific recovery for malformed input.
	if err != nil {
		return nil, err
	}
	for key := range pairs {
		// The mutation maps exactly these three public fields; accepting anything else would report success without applying the caller's value.
		switch key {
		case "client_id", "client_secret", "redirect_uri":
		default:
			return nil, fmt.Errorf("invalid connect field %q; expected client_id, client_secret, or redirect_uri", key)
		}
	}
	return pairs, nil
}

func connectFieldsFromPrompts(authType, authName string) (api.ConnectConfigUpsertRequest, error) {
	var clientID, clientSecret, redirectURI string
	if err := huh.NewInput().Title("Client ID (blank to leave unchanged):").Value(&clientID).Run(); err != nil {
		return api.ConnectConfigUpsertRequest{}, err
	}
	if err := huh.NewInput().Title("Client Secret (blank to leave unchanged):").EchoMode(huh.EchoModePassword).Value(&clientSecret).Run(); err != nil {
		return api.ConnectConfigUpsertRequest{}, err
	}
	if err := huh.NewInput().Title("Redirect URI (blank to leave unchanged):").Value(&redirectURI).Run(); err != nil {
		return api.ConnectConfigUpsertRequest{}, err
	}
	req := connectAuthRequest(authType, authName)
	if clientID != "" {
		req.ClientID = &clientID
	}
	if clientSecret != "" {
		req.ClientSecret = &clientSecret
	}
	if redirectURI != "" {
		req.RedirectURI = &redirectURI
	}
	return req, nil
}

func connectAuthRequest(authType, authName string) api.ConnectConfigUpsertRequest {
	req := api.ConnectConfigUpsertRequest{AuthType: &authType}
	if authName = strings.TrimSpace(authName); authName != "" {
		req.AuthName = &authName
	}
	return req
}

// printConnectConfigResult renders the same safe projection whether it just
// came from a set (freshly saved) or a get (already on record) -- neither
// caller needs a different message, only the fields themselves.
func printConnectConfigResult(out io.Writer, cfg *api.ConnectConfigResponse) {
	if out == nil {
		out = io.Discard
	}
	fmt.Fprintf(out,
		"Connect config for service %s (bucket %s): auth_type=%s auth_name=%s enabled=%t redirect_uri=%s has_client_id=%t has_client_secret=%t\n",
		cfg.ServiceID, cfg.BucketID, cfg.AuthType, cfg.AuthName, cfg.Enabled, cfg.RedirectURI, cfg.HasClientID, cfg.HasClientSecret,
	)
}

func init() {
	RootCmd.AddCommand(connectCmd)
	connectCmd.AddCommand(connectSetCmd, connectGetCmd)
	addJSONOutputFlag(connectGetCmd)
	connectSetCmd.Flags().StringVar(&connectSetBucketID, "bucket", "", "Bucket name or UUID (required)")
	connectSetCmd.Flags().StringVar(&connectSetType, "type", "", "Connect authentication type (oauth or oidc)")
	connectSetCmd.Flags().StringVar(&connectSetAuthName, "auth-name", "", "Exact provider auth scheme name; required when --type matches multiple schemes")
	connectSetCmd.Flags().BoolVarP(&connectSetInteractive, "interactive", "i", false, "Prompt per field (blank keeps it unchanged)")
	connectSetCmd.Flags().BoolVar(&connectSetValueStdin, "value-stdin", false, "Read the registration fields from stdin")
	connectGetCmd.Flags().StringVar(&connectGetBucketID, "bucket", "", "Bucket name or UUID (required)")
}
