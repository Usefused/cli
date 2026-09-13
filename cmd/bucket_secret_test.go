package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestRunBucketSecretSetUsesExplicitBucketAndStdin verifies automation sends
// secret material only in the body and returns metadata-only JSON.
func TestRunBucketSecretSetUsesExplicitBucketAndStdin(t *testing.T) {
	const bucketID = "11111111-1111-4111-8111-111111111111"
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The mutation contract carries explicit bucket identity only in the path.
		if r.Method != http.MethodPut || r.URL.Path != "/workspace/buckets/"+bucketID+"/secrets" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		// Capture the body to prove the secret never entered a URL or output receipt.
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode bucket secret request: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	restore := configureBucketSecretTestGlobals(server.URL)
	t.Cleanup(restore)
	bucketSecretSetValueStdin = true
	NoInput = true
	command := &cobra.Command{Use: "set"}
	command.SetIn(strings.NewReader("secret-token\n"))
	output := &bytes.Buffer{}
	command.SetOut(output)
	addJSONOutputFlag(command)
	// JSON selection is explicit because mutation commands default to human output.
	if err := command.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}

	// Run the command core directly so this test isolates the secure input and HTTP contract.
	if err := runBucketSecretSet(command, bucketID, "webhook_signing"); err != nil {
		t.Fatalf("run bucket secret set: %v", err)
	}
	if requestBody["key_name"] != "webhook_signing" || requestBody["value"] != "secret-token" {
		t.Fatalf("bucket secret request = %#v", requestBody)
	}
	if _, found := requestBody["service_id"]; found {
		t.Fatalf("generic secret request unexpectedly included service identity: %#v", requestBody)
	}
	// The receipt can identify the mutation but must never repeat credential material.
	if !strings.Contains(output.String(), bucketID) || !strings.Contains(output.String(), "webhook_signing") || strings.Contains(output.String(), "secret-token") {
		t.Fatalf("unsafe or incomplete JSON receipt: %s", output.String())
	}
}

// TestRunBucketSecretSetPromptsForOmittedName verifies the ordinary terminal
// path can collect the public name while the bucket remains positional.
func TestRunBucketSecretSetPromptsForOmittedName(t *testing.T) {
	const bucketID = "22222222-2222-4222-8222-222222222222"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		// The server only needs to inspect the credential-safe request boundary for this prompt test.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode bucket secret request: %v", err)
		}
		// The injected prompt result must become the exact generic secret payload.
		if body["key_name"] != "shared_token" || body["value"] != "prompted-secret" {
			t.Fatalf("prompted request = %#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	restore := configureBucketSecretTestGlobals(server.URL)
	t.Cleanup(restore)
	t.Setenv("CI", "false")
	called := false
	collectBucketSecretInput = func(input bucketSecretInput) (bucketSecretInput, error) {
		called = true
		// The omitted secret name reaches the collector, while the explicit bucket never does.
		if input.KeyName != "" {
			t.Fatalf("prompt seed key = %q", input.KeyName)
		}
		return bucketSecretInput{KeyName: "shared_token", Value: "prompted-secret"}, nil
	}
	command := &cobra.Command{Use: "set"}
	output := &bytes.Buffer{}
	command.SetOut(output)

	// The omitted name is legal only because this test supplies an interactive collector.
	if err := runBucketSecretSet(command, bucketID, ""); err != nil {
		t.Fatalf("run interactive bucket secret set: %v", err)
	}
	if !called || !strings.Contains(output.String(), `Bucket secret "shared_token" set`) {
		t.Fatalf("interactive result called=%v output=%q", called, output.String())
	}
}

// TestValidateBucketSecretSetArgsRequiresDeterministicAutomation verifies the
// bucket is always positional and non-interactive input is fully explicit.
func TestValidateBucketSecretSetArgsRequiresDeterministicAutomation(t *testing.T) {
	restore := configureBucketSecretTestGlobals("http://unused.invalid")
	t.Cleanup(restore)
	command := &cobra.Command{Use: "set <bucket-name-or-id> [secret-name]"}
	NoInput = true

	// No invocation may infer a default bucket.
	if err := validateBucketSecretSetArgs(command, nil); err == nil {
		t.Fatal("missing bucket was accepted")
	}
	// Automation cannot defer the public secret identity to a prompt.
	if err := validateBucketSecretSetArgs(command, []string{"default"}); err == nil || !strings.Contains(err.Error(), "secret name") {
		t.Fatalf("missing automation name error = %v", err)
	}
	// A name alone is insufficient because credential values cannot enter argv.
	if err := validateBucketSecretSetArgs(command, []string{"default", "webhook_signing"}); err == nil || !strings.Contains(err.Error(), "--value-stdin") {
		t.Fatalf("missing stdin mode error = %v", err)
	}
	bucketSecretSetValueStdin = true
	// A third positional value would leak the secret through shell history and process listings.
	if err := validateBucketSecretSetArgs(command, []string{"default", "webhook_signing", "visible-secret"}); err == nil {
		t.Fatal("secret value in argv was accepted")
	}
}

// configureBucketSecretTestGlobals isolates mutable CLI flags and endpoint
// configuration for one command test.
func configureBucketSecretTestGlobals(engineURL string) func() {
	oldEngineURL, oldAPIKey := EngineURL, APIKey
	oldNoInput := NoInput
	oldInteractive, oldStdin, oldExpires := bucketSecretSetInteractive, bucketSecretSetValueStdin, bucketSecretSetExpiresAt
	oldCollector := collectBucketSecretInput
	EngineURL, APIKey = engineURL, "fsk_test"
	NoInput = false
	bucketSecretSetInteractive, bucketSecretSetValueStdin, bucketSecretSetExpiresAt = false, false, ""
	collectBucketSecretInput = promptBucketSecretInput
	return func() {
		EngineURL, APIKey = oldEngineURL, oldAPIKey
		NoInput = oldNoInput
		bucketSecretSetInteractive, bucketSecretSetValueStdin, bucketSecretSetExpiresAt = oldInteractive, oldStdin, oldExpires
		collectBucketSecretInput = oldCollector
	}
}
