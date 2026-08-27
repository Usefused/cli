package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestMCPTokenGenerationErrorsReachCommand exercises the reported fixed-binding
// command without live mutations and preserves both human and JSON diagnostics.
func TestMCPTokenGenerationErrorsReachCommand(t *testing.T) {
	cases := []struct {
		name      string
		missing   bool
		code      string
		want      string
		mutations int32
	}{
		{name: "missing MCP", missing: true, code: "resource_not_found", want: `MCP "google-workspace-mcp" was not found`, mutations: 0},
		{name: "existing token", code: "app_token_name_conflict", want: "A token with this name already exists", mutations: 1},
	}
	// Lookup rejection and token conflict must remain distinguishable at the real command boundary.
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) { // Keep flags, transport, and output isolated per scenario.
			var mutations atomic.Int32
			server := httptest.NewServer(appTokenErrorFixture(t, test.missing, &mutations))
			defer server.Close()
			oldURL, oldKey := EngineURL, APIKey
			EngineURL, APIKey = server.URL, "fixture-key"
			t.Cleanup(func() { EngineURL, APIKey = oldURL, oldKey }) // Never leave test credentials in CLI globals.
			command := newMCPTokenGenerateTestCommand()
			addJSONOutputFlag(command)
			// These are the same two independent bindings supplied in the reported command.
			for _, binding := range []string{"gmail,oauth2,google-user-a", "googledrive,oauth2,google-user-a"} {
				if err := command.Flags().Set("fixed-binding", binding); err != nil { // Invalid fixture flags must not hide an HTTP regression.
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			command.SetOut(&output)
			err := runMCPTokenGenerate(command, "google-workspace-mcp", "agent-token")
			// Rejected lookups must never reach issuance, and neither failure may print a token.
			if err == nil || mutations.Load() != test.mutations || output.Len() != 0 {
				t.Fatalf("result = %v, mutation count = %d, output = %q", err, mutations.Load(), output.String())
			}
			assertAppTokenErrorOutput(t, err, test.code, test.want)
		})
	}
}

// appTokenErrorFixture supplies only safe resolver and token-conflict contracts.
func appTokenErrorFixture(t *testing.T, missing bool, mutations *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { // No fixture request can reach a real Engine.
		w.Header().Set("Content-Type", "application/json")
		// A successful name resolution is required before the mutation endpoint is reached.
		if r.URL.Path == "/engine/graphql" {
			if missing { // Permission-filtered absence uses the Engine's stable resolver code.
				_, _ = w.Write([]byte(`{"data":{"appFamilyReference":null},"errors":[{"message":"resource was not found","extensions":{"code":"FUSED_RESOURCE_NOT_FOUND"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"appFamilyReference":{"id":"1ebd6ae0-883d-44c4-af06-b1708f754969","kind":"app_family"}}}`))
			return
		}
		// Unexpected requests indicate a fallback or a second mutation path.
		if r.Method != http.MethodPost || r.URL.Path != "/workspace/app-tokens" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		mutations.Add(1)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"app_token_name_conflict","message":"A token with this name already exists for this app.","category":"conflict","retryable":false,"remediation":"Choose a different token name, or explicitly revoke the existing token before reusing its name."}}`))
	})
}

// assertAppTokenErrorOutput proves shared command rendering preserves structured errors without a generic 500.
func assertAppTokenErrorOutput(t *testing.T, err error, code, want string) {
	t.Helper()
	command := newMCPTokenGenerateTestCommand()
	addJSONOutputFlag(command)
	var output bytes.Buffer
	// Human output must retain the specific missing-app or duplicate-token explanation.
	if writeErr := writeCommandError(&output, command, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	if !strings.Contains(output.String(), want) || strings.Contains(output.String(), "engine_request_failed") { // A generic outage would recreate the reported failure.
		t.Fatalf("human output = %q", output.String())
	}
	// Automation receives the same stable category rather than parsing display text.
	if setErr := command.Flags().Set(jsonOutputFlag, "true"); setErr != nil {
		t.Fatal(setErr)
	}
	output.Reset()
	if writeErr := writeCommandError(&output, command, err); writeErr != nil { // Reject incomplete JSON output.
		t.Fatal(writeErr)
	}
	var envelope jsonErrorEnvelope
	if decodeErr := json.Unmarshal(output.Bytes(), &envelope); decodeErr != nil { // Require actual serialized command output.
		t.Fatal(decodeErr)
	}
	if envelope.Error.Code != code { // Typed errors must survive the command wrapper.
		t.Fatalf("JSON error = %#v", envelope.Error)
	}
}
