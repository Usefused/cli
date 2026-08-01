package cmd

import (
	"errors"
	"fmt"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// connectCmd registers, rotates, or reads back a bucket's OAuth/OIDC app
// registration (client_id/client_secret/redirect_uri) directly against the
// Engine admin endpoint -- deliberately outside workspace.yaml/plan/apply,
// the same way bucket secrets already are, since these are
// credential-adjacent values rather than declarative service policy. See
// fused-bucket skill for how this fits alongside `secret set` and
// `workspace service <slug> connect`.
var connectCmd = &cobra.Command{
	Use:   "connect <service-slug> set|get [value]",
	Short: "Register, rotate, or check a bucket's OAuth/OIDC app registration for a service",
	Args:  validateConnectArgs,
	// Write to OTEL to audit user/agent-triggered mutative execution.
	// Why no ValidArgsFunction: secret.go's completeSecretArgs offers "set"
	// and "remove" as the action -- reusing it here would suggest a "remove"
	// action connect doesn't have. Add shell completion offering "set"/"get"
	// once this command's own completion is worth building.
	RunE: WithTelemetry("cli.connect", func(cmd *cobra.Command, args []string) error {
		return runConnectAction(cmd, args)
	}),
}

var connectSetInteractive bool
var connectSetBucketID string
var connectSetType string

func validateConnectArgs(cmd *cobra.Command, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("connect requires a service slug and an action (set or get)")
	}
	switch args[1] {
	case "set":
		if connectSetInteractive {
			if len(args) != 2 {
				return fmt.Errorf("accepts no value argument when using interactive mode")
			}
			return nil
		}
		if len(args) > 3 {
			return fmt.Errorf("set accepts exactly 1 arg after action (value)")
		}
		return nil
	case "get":
		// get only ever reads back the safe projection (see
		// printConnectConfigResult) -- there is no value to parse and no
		// -i/--type disambiguation, so any extra positional arg is a mistake
		// worth catching before a network round trip rather than silently
		// ignoring it.
		if len(args) != 2 {
			return fmt.Errorf("get accepts no arguments after the action")
		}
		return nil
	default:
		return fmt.Errorf("unknown connect action %q", args[1])
	}
}

func runConnectAction(cmd *cobra.Command, args []string) error {
	serviceSlug := args[0]
	if args[1] == "get" {
		return runConnectGet(serviceSlug)
	}
	var value string
	if len(args) > 2 {
		value = args[2]
	}
	return runConnectSet(serviceSlug, value)
}

// resolveConnectTarget resolves the client/bucket/service triple both set
// and get need, so the two actions can never disagree about which
// bucket+service a slug refers to -- one resolution path, not two that could
// drift.
func resolveConnectTarget(action, serviceSlug string) (client *api.Client, bucketID, serviceID string, err error) {
	client, err = getAPIClient()
	if err != nil {
		return nil, "", "", err
	}
	bucketID, err = resolveBucketIDPrompt(client, connectSetBucketID)
	if err != nil {
		return nil, "", "", err
	}
	if bucketID == "" {
		return nil, "", "", fmt.Errorf("connect %s requires --bucket", action)
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
func runConnectSet(serviceSlug, value string) error {
	client, bucketID, serviceID, err := resolveConnectTarget("set", serviceSlug)
	if err != nil {
		return err
	}
	info, err := client.GetServiceInfo(serviceSlug)
	if err != nil {
		return err
	}
	authType, err := selectConnectAuthType(info, serviceSlug)
	if err != nil {
		return err
	}
	fields, err := connectSetFields(authType, value)
	if err != nil {
		return err
	}
	saved, err := client.UpsertConnectConfig(bucketID, serviceID, fields)
	if err != nil {
		return err
	}
	printConnectConfigResult(saved)
	return nil
}

// runConnectGet reads back whatever `set` last saved, if anything. Unlike
// set, it needs no auth-type selection -- it just asks Engine for whatever
// is on record for this bucket+service, or reports that nothing is.
func runConnectGet(serviceSlug string) error {
	client, bucketID, serviceID, err := resolveConnectTarget("get", serviceSlug)
	if err != nil {
		return err
	}
	cfg, err := client.GetConnectConfig(bucketID, serviceID)
	if err != nil {
		if errors.Is(err, api.ErrConnectConfigNotFound) {
			return fmt.Errorf("no connect config registered for service %s in this bucket -- see `fused-cli connect %s set`", serviceSlug, serviceSlug)
		}
		return err
	}
	printConnectConfigResult(cfg)
	return nil
}

// selectConnectAuthType narrows a service's declared auth schemes to the two
// interactive-flow-capable types -- connect only supports oauth/oidc
// (basic/api_key/bearer/mtls have no browser consent step, see
// fused-config's auth/connect/profile split). Reuses the same --type
// disambiguation secret.go's static-credential path already has, so a
// service declaring both oauth and oidc is resolved the same familiar way.
func selectConnectAuthType(info *api.ServiceInfo, serviceSlug string) (string, error) {
	var candidates []string
	for i := range info.AuthConfigs {
		if t := canonicalSecretAuthType(&info.AuthConfigs[i]); t == "oauth" || t == "oidc" {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("service %s does not declare an oauth or oidc auth scheme -- connect only supports interactive OAuth/OIDC flows", serviceSlug)
	}
	if connectSetType != "" {
		for _, t := range candidates {
			if t == connectSetType {
				return t, nil
			}
		}
		return "", fmt.Errorf("service %s does not support connect auth type %q", serviceSlug, connectSetType)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", fmt.Errorf("service %s supports multiple connect-capable auth types; specify --type oauth or --type oidc", serviceSlug)
}

// connectSetFields resolves which fields to send: parsed from the inline
// "key=value;..." argument, or prompted interactively. An inline key that is
// present but blank (e.g. "client_secret=") and a blank interactive prompt
// both become an explicit empty string, which Engine rejects as "you tried
// to blank this out" -- only a key that never appears at all means "leave
// unchanged". That distinction is what makes a single-field rotation
// possible without resending the other two.
func connectSetFields(authType, value string) (api.ConnectConfigUpsertRequest, error) {
	if value != "" {
		return connectFieldsFromInline(authType, value), nil
	}
	if err := requireInteractive("provide connect fields in the value argument"); err != nil {
		return api.ConnectConfigUpsertRequest{}, err
	}
	return connectFieldsFromPrompts(authType)
}

func connectFieldsFromInline(authType, value string) api.ConnectConfigUpsertRequest {
	pairs := parseInlineKeyValuePairs(value)
	req := api.ConnectConfigUpsertRequest{AuthType: &authType}
	if v, ok := pairs["client_id"]; ok {
		req.ClientID = &v
	}
	if v, ok := pairs["client_secret"]; ok {
		req.ClientSecret = &v
	}
	if v, ok := pairs["redirect_uri"]; ok {
		req.RedirectURI = &v
	}
	return req
}

func connectFieldsFromPrompts(authType string) (api.ConnectConfigUpsertRequest, error) {
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
	req := api.ConnectConfigUpsertRequest{AuthType: &authType}
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

// printConnectConfigResult renders the same safe projection whether it just
// came from a set (freshly saved) or a get (already on record) -- neither
// caller needs a different message, only the fields themselves.
func printConnectConfigResult(cfg *api.ConnectConfigResponse) {
	fmt.Printf(
		"Connect config for service %s (bucket %s): auth_type=%s enabled=%t redirect_uri=%s has_client_id=%t has_client_secret=%t\n",
		cfg.ServiceID, cfg.BucketID, cfg.AuthType, cfg.Enabled, cfg.RedirectURI, cfg.HasClientID, cfg.HasClientSecret,
	)
}

func init() {
	RootCmd.AddCommand(connectCmd)
	connectCmd.Flags().StringVar(&connectSetBucketID, "bucket", "", "Bucket (name or ID) to register this connect config against")
	connectCmd.Flags().StringVar(&connectSetType, "type", "", "Disambiguate when a service declares both oauth and oidc")
	connectCmd.Flags().BoolVarP(&connectSetInteractive, "interactive", "i", false, "Interactive mode, prompting per field (blank keeps it unchanged)")
}
