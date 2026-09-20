package cmd

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// configureSecretWebhookTestGlobals isolates the mutable flags and endpoint
// globals the webhook offer touches so one test cannot leak into another.
func configureSecretWebhookTestGlobals(engineURL string) func() {
	oldEngineURL, oldAPIKey := EngineURL, APIKey
	oldNoInput := NoInput
	oldType, oldStdin := secretSetType, secretSetValueStdin
	oldWebhook := secretSetWebhook.value
	EngineURL, APIKey = engineURL, "fsk_test"
	NoInput = false
	secretSetType, secretSetValueStdin = "", false
	secretSetWebhook.value = ""
	return func() {
		EngineURL, APIKey = oldEngineURL, oldAPIKey
		NoInput = oldNoInput
		secretSetType, secretSetValueStdin = oldType, oldStdin
		secretSetWebhook.value = oldWebhook
	}
}

// TestGenerateWebhookSigningSecretIsRandomHex verifies the generated signing
// secret is cryptographically shaped for safe copy-paste into provider settings.
func TestGenerateWebhookSigningSecretIsRandomHex(t *testing.T) {
	first, err := generateWebhookSigningSecret()
	if err != nil {
		t.Fatalf("first secret: %v", err)
	}
	second, err := generateWebhookSigningSecret()
	if err != nil {
		t.Fatalf("second secret: %v", err)
	}
	if len(first) != 64 || len(second) != 64 {
		t.Fatalf("expected 64-char hex secrets, got %d and %d", len(first), len(second))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("secret is not hex: %v", err)
	}
	// Two independent calls must never collide in a way a test could pass accidentally.
	if first == second {
		t.Fatal("two generated secrets must differ")
	}
}

// TestWebhookSigningSecretKey verifies the default-bucket key is reference-safe
// and deterministic for a given service slug.
func TestWebhookSigningSecretKey(t *testing.T) {
	if got := webhookSigningSecretKey("github"); got != "github_signing" {
		t.Fatalf("key = %q", got)
	}
}

// TestWebhookRegistrationURL verifies the reserved default label maps to the
// predictable /webhook/svc/{service} URL while other labels keep the token form.
func TestWebhookRegistrationURL(t *testing.T) {
	if got := webhookRegistrationURL("https://engine.example.com", defaultWebhookName, "github", "AbC123"); got != "https://engine.example.com/webhook/svc/github" {
		t.Fatalf("default URL = %q", got)
	}
	if got := webhookRegistrationURL("https://engine.example.com", "team-x-webhooks", "github", "AbC123"); got != "https://engine.example.com/webhook/AbC123-github" {
		t.Fatalf("token URL = %q", got)
	}
}

// TestOfferWebhookAfterSecretSetGatesFamilyAndFlags verifies the post-write
// offer stays deterministic and never prompts for non-OAuth/OIDC families or
// automation, and honors the shared tri-state selector.
func TestOfferWebhookAfterSecretSetGatesFamilyAndFlags(t *testing.T) {
	cases := []struct {
		name            string
		typ             string
		webhook         string
		noInput         bool
		wantErrContains string
		wantNil         bool
	}{
		{name: "generate on basic", typ: "basic", webhook: "generate", wantErrContains: "only to OAuth/OIDC"},
		{name: "reuse name on api_key", typ: "api_key", webhook: "team-x-webhooks", wantErrContains: "only to OAuth/OIDC"},
		{name: "no flag on basic skips", typ: "basic", wantNil: true},
		{name: "no flag non-interactive oauth skips", typ: "oauth", noInput: true, wantNil: true},
		{name: "explicit none skips even interactively", typ: "oauth", webhook: "none", wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := configureSecretWebhookTestGlobals("http://unused.invalid")
			t.Cleanup(restore)
			secretSetType = tc.typ
			secretSetWebhook.value = tc.webhook
			if tc.noInput {
				NoInput = true
			}
			err := offerWebhookAfterSecretSet(&cobra.Command{Use: "set"}, "github")
			if tc.wantNil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("error = %v, want contains %q", err, tc.wantErrContains)
			}
		})
	}
}
