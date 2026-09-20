package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

// defaultWebhookName is the reserved kind: webhook config name used when
// `secret set --webhook generate` creates a service's default inbound
// registration. `(service, "default")` is globally unique per account, so a
// repeat generate reuses the existing registration rather than applying a
// conflicting duplicate.
const defaultWebhookName = "default"

// Inbound webhook selector tokens. The same tri-state flag is shared by any
// command that registers an OAuth/OIDC app, so the choice is expressed in one
// place instead of as a growing set of per-command booleans.
const (
	inboundWebhookGenerate = "generate"
	inboundWebhookNone     = "none"
)

// inboundWebhookFlag is the single, reusable inbound selector: its value is a
// reserved token ("generate"/"none") or an existing kind: webhook config name
// to reuse. An empty value means "no explicit choice" (prompt on a TTY, skip
// in automation).
type inboundWebhookFlag struct {
	value string
}

// addFlag attaches the shared selector to a command.
func (f *inboundWebhookFlag) addFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.value, "webhook", "", `Inbound webhook after storing the OAuth/OIDC app: "generate" (default registration), "none" (skip), or an existing kind:webhook config name to reuse`)
}

// offerWebhookAfterSecretSet runs after an OAuth/OIDC app pair is committed.
// It is deliberately scoped to OAuth/OIDC families: those are the only
// credential types whose provider-side app settings also register an inbound
// webhook URL. Automation only acts when an explicit flag was passed, so
// `--json`/`--no-input`/CI never prompt and never mutate beyond the pair.
func offerWebhookAfterSecretSet(cmd *cobra.Command, serviceSlug string) error {
	choice := strings.TrimSpace(secretSetWebhook.value)
	// Non-OAuth/OIDC families have no app-registration webhook surface to offer.
	family := canonicalSecretTypeName(secretSetType)
	if family != "oauth" && family != "oidc" {
		// An explicit webhook flag on the wrong family is a usage error, not silence.
		if choice != "" {
			return fmt.Errorf("--webhook applies only to OAuth/OIDC app registrations (pass --type oauth or --type oidc)")
		}
		return nil
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	switch choice {
	case inboundWebhookNone:
		// Explicit skip overrides the interactive prompt.
		return nil
	case inboundWebhookGenerate:
		if err := generateDefaultWebhook(cmd, client, serviceSlug); err != nil {
			return fmt.Errorf("OAuth app credentials were stored, but generating the webhook failed: %w", err)
		}
		return nil
	case "":
		// No explicit choice: only an interactive terminal is offered the question.
		if !secretSetUsesInteractiveInput() {
			return nil
		}
		return promptWebhookAfterSecretSet(cmd, client, serviceSlug)
	default:
		// Any other value is an existing kind: webhook config name to reuse.
		if err := reuseWebhookRegistration(cmd, client, serviceSlug, choice); err != nil {
			return fmt.Errorf("OAuth app credentials were stored, but reusing webhook %q failed: %w", choice, err)
		}
		return nil
	}
}

// webhookChoice is one interactive inbound option presented after the pair is stored.
type webhookChoice struct {
	label    string
	reuse    *api.WorkspaceWebhook
	generate bool
}

// promptWebhookAfterSecretSet asks whether to generate a default registration
// or reuse an existing one already attached to the service. Discovery failures
// here never fail the command: the OAuth pair is already committed.
func promptWebhookAfterSecretSet(cmd *cobra.Command, client *api.Client, serviceSlug string) error {
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Skipping webhook offer: %v\n", err)
		return nil
	}
	existing, err := client.ListWorkspaceWebhooks(serviceID)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Skipping webhook offer: %v\n", err)
		return nil
	}
	choices := []webhookChoice{{label: "Generate a new webhook URL", generate: true}}
	for _, w := range existing {
		// A present default registration is exactly what "generate" would no-op
		// into, so it is surfaced through the reuse list path instead.
		if w.Label == defaultWebhookName {
			continue
		}
		wv := w
		choices = append(choices, webhookChoice{label: fmt.Sprintf("Use existing webhook %q", w.Label), reuse: &wv})
	}
	choices = append(choices, webhookChoice{label: "Skip for now"})
	options := make([]huh.Option[int], 0, len(choices))
	for i, c := range choices {
		options = append(options, huh.NewOption(c.label, i))
	}
	selected := 0
	if err := huh.NewSelect[int]().Title(fmt.Sprintf("Inbound webhook for %s?", serviceSlug)).Options(options...).Value(&selected).Run(); err != nil {
		return fmt.Errorf("reading webhook choice: %w", err)
	}
	chosen := choices[selected]
	if chosen.reuse != nil {
		printReusedWebhookURL(client, serviceSlug, *chosen.reuse)
		return nil
	}
	if chosen.generate {
		return generateDefaultWebhook(cmd, client, serviceSlug)
	}
	return nil
}

// reuseWebhookRegistration prints the URL of an existing registration owned by
// the named kind: webhook config, without mutating any state.
func reuseWebhookRegistration(cmd *cobra.Command, client *api.Client, serviceSlug, name string) error {
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}
	existing, err := client.ListWorkspaceWebhooks(serviceID)
	if err != nil {
		return err
	}
	for _, w := range existing {
		if w.Label == name {
			printReusedWebhookURL(client, serviceSlug, w)
			return nil
		}
	}
	return fmt.Errorf("no webhook registration for %q owned by %q exists; run `fused-cli webhook apply` for that config first", serviceSlug, name)
}

// printReusedWebhookURL reconstructs the full display URL from the opaque slug,
// matching `workspace service <slug> webhooks` and apply output.
func printReusedWebhookURL(client *api.Client, serviceSlug string, w api.WorkspaceWebhook) {
	fmt.Printf("Using webhook %q: %s\n", w.Label, appliedWebhookURL(client.BaseURL, api.AppliedWebhookConfig{ServiceKey: serviceSlug, Label: w.Label, Slug: w.Slug}))
}

// generateDefaultWebhook materializes the service's default inbound webhook
// registration (reserved name "default"), provisioning a random signing secret
// in the default bucket only when the service's contract declares signed
// webhooks.
func generateDefaultWebhook(cmd *cobra.Command, client *api.Client, serviceSlug string) error {
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}
	// Idempotency: a generated default registration already exists, so surface
	// its URL instead of applying a duplicate (which would conflict on
	// (service, "default") uniqueness).
	existing, err := client.ListWorkspaceWebhooks(serviceID)
	if err != nil {
		return err
	}
	for _, w := range existing {
		if w.Label == defaultWebhookName {
			printReusedWebhookURL(client, serviceSlug, w)
			return printRotateSigningSecretHint(client, serviceID, serviceSlug)
		}
	}
	signed, err := serviceSupportsSignedWebhooks(client, serviceID)
	if err != nil {
		return err
	}
	// The default URL is predictable and therefore guessable, so it must always
	// be signature-verified. A service without a signed-webhook contract has no
	// signing secret to verify with, so point the user at a named opaque URL.
	if !signed {
		return fmt.Errorf("%q declares no inbound webhook signature, so a predictable default URL cannot be verified; use `fused-cli webhook apply` with a named config for an opaque URL instead", serviceSlug)
	}
	secretRef, signingSecret, err := provisionWebhookSigningSecret(client, serviceSlug)
	if err != nil {
		return err
	}
	if err := applyGeneratedWebhookConfig(client, serviceSlug, secretRef); err != nil {
		return err
	}
	// The signing secret is shown exactly once because Engine cannot recover the
	// plaintext later.
	printSigningSecretOnce(serviceSlug, signingSecret)
	return nil
}

// printRotateSigningSecretHint reminds a user how to rotate the generated
// registration's signing secret without regenerating the registration itself.
func printRotateSigningSecretHint(client *api.Client, serviceID, serviceSlug string) error {
	signed, err := serviceSupportsSignedWebhooks(client, serviceID)
	if err != nil {
		return err
	}
	if signed {
		printRotateSigningSecretCommand(serviceSlug)
	}
	return nil
}

// printRotateSigningSecretCommand prints the masked-prompt command that
// replaces the generated default signing secret in place.
func printRotateSigningSecretCommand(serviceSlug string) {
	fmt.Printf("\nTo rotate the signing secret later:\n  fused-cli bucket secret set default %s --interactive\n", webhookSigningSecretKey(serviceSlug))
}

// printSigningSecretOnce reveals the just-generated signing secret and the
// command that can later rotate it.
func printSigningSecretOnce(serviceSlug, secret string) {
	fmt.Printf("  Signing secret (shown once): %s   (saved to bucket default key %s)\n", secret, webhookSigningSecretKey(serviceSlug))
	printRotateSigningSecretCommand(serviceSlug)
}

// webhookSigningSecretKey names the default-bucket signing secret for a
// service's generated default registration. It must stay reference-safe
// (single segment, no dots/braces) because it is embedded in a
// ${bucket.default.secret.<key>} reference.
func webhookSigningSecretKey(serviceSlug string) string {
	return serviceSlug + "_signing"
}

// serviceSupportsSignedWebhooks reports whether the service's imported
// contract declares inbound signature verification -- the only case where a
// signing secret exists to generate or rotate.
func serviceSupportsSignedWebhooks(client *api.Client, serviceID string) (bool, error) {
	vis, err := client.ServiceVisibilities([]string{serviceID})
	if err != nil {
		return false, err
	}
	v, ok := vis[serviceID]
	if !ok {
		// A missing visibility entry means no signed-webhook contract was declared.
		return false, nil
	}
	return v.IncomingWebhookConfig != nil && strings.TrimSpace(v.IncomingWebhookConfig.AuthType) != "", nil
}

// provisionWebhookSigningSecret stores a fresh random signing secret in the
// default bucket and returns the reference string the generated webhook config
// uses plus the plaintext value for one-time display.
func provisionWebhookSigningSecret(client *api.Client, serviceSlug string) (string, string, error) {
	// Resolve the default bucket's ID because bucket secrets are stored keyed
	// by bucket identity, while the config reference uses its name.
	bucketID, err := client.ResolveBucketReference("default")
	if err != nil {
		return "", "", err
	}
	key := webhookSigningSecretKey(serviceSlug)
	secret, err := generateWebhookSigningSecret()
	if err != nil {
		return "", "", err
	}
	if err := client.UpsertBucketSecret(bucketID, key, secret, nil); err != nil {
		return "", "", err
	}
	return fmt.Sprintf("${bucket.default.secret.%s}", key), secret, nil
}

// generateWebhookSigningSecret produces a cryptographically random,
// hex-encoded signing secret that survives copy-paste into any provider's
// webhook settings without shell or encoding surprises.
func generateWebhookSigningSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating webhook signing secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// applyGeneratedWebhookConfig builds an in-memory kind: webhook config for the
// reserved default name and runs the same plan/apply path `fused-cli webhook
// apply` uses, so the registration passes the exact same validation and
// uniqueness rules as a hand-written file.
func applyGeneratedWebhookConfig(client *api.Client, serviceSlug, secretRef string) error {
	cfg := configfile.WebhookConfig{
		BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: configfile.KindWebhook},
		Name:       defaultWebhookName,
		Services:   map[string]configfile.WebhookService{serviceSlug: {Secret: secretRef}},
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding generated webhook config: %w", err)
	}
	// Parse (rather than constructing ParsedConfig by hand) so ConfigKey and
	// SourceHash are computed exactly as LoadRun would for a real file.
	parsed, err := configfile.Parse(raw, "<generated-webhook>")
	if err != nil {
		return fmt.Errorf("validating generated webhook config: %w", err)
	}
	engineURL, _ := GetEngineURL()
	planned, err := planOneConfig(client, parsed, engineURL, "")
	if err != nil {
		return err
	}
	return applyPreparedWebhook(client, parsed, planned.receipt)
}
