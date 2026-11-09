package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

// TestTemporaryOAuthClientCommandCreatesOneTimePair exercises explicit scopes without a saved CLI login.
func TestTemporaryOAuthClientCommandCreatesOneTimePair(t *testing.T) {
	command, remaining, err := RootCmd.Find([]string{"oauth", "register"})
	// The complete command path must be discoverable without a parent-command fallback.
	if err != nil || len(remaining) != 0 || command == nil || command.Name() != "register" {
		t.Fatalf("OAuth registration command = %v, remaining=%v, err=%v", command, remaining, err)
	}
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	calls := 0
	// The fake Engine inspects the actual public command's request and one-time response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Registration must stay anonymous even when command tests set an ambient control key.
		if r.URL.Path != "/oauth/register" || r.Method != http.MethodPost || r.Header.Get("X-API-Key") != "" {
			t.Errorf("unexpected OAuth request: %s %s", r.Method, r.URL.Path)
		}
		var input api.TemporaryOAuthClientRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Name != "Local app" || input.RedirectURI != "http://localhost:4321/callback" || len(input.Scopes) != 2 || input.Scopes[0] != "service.read" || input.Scopes[1] != "app.sdk.read" {
			t.Errorf("registration input = %#v, %v", input, err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"client_id":"foc_test","client_secret":"fos_once","client_id_expires_at":%q}`, expiry.Format(time.RFC3339))
	}))
	defer server.Close()
	output := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"oauth", "register", "--name", "Local app", "--redirect-uri", "http://localhost:4321/callback", "--scope", "service.read", "--scope", "app.sdk.read", "--json"})
	var result temporaryOAuthClientReceipt
	// Structured output must contain the pair and expiry exactly once for automation.
	if err := json.Unmarshal([]byte(output), &result); err != nil || calls != 1 || result.Name != "Local app" || result.ClientID != "foc_test" || result.ClientSecret != "fos_once" || !result.ClientIDExpiresAt.Equal(expiry) {
		t.Fatalf("registration output = %q, calls=%d, err=%v", output, calls, err)
	}
}

// TestTemporaryOAuthClientInteractiveScopesAndGeneratedName verifies discovery feeds the terminal picker before registration.
func TestTemporaryOAuthClientInteractiveScopesAndGeneratedName(t *testing.T) {
	oldURL, oldKey, oldNoInput := EngineURL, APIKey, NoInput
	oldPicker := selectTemporaryOAuthScopes
	// Restore shared command settings and the real terminal picker after this fixture.
	t.Cleanup(func() {
		EngineURL, APIKey, NoInput = oldURL, oldKey, oldNoInput
		selectTemporaryOAuthScopes = oldPicker
	})
	NoInput = false
	t.Setenv("CI", "false")
	selectedCatalog := false
	// The picker receives Engine's public catalog, without preselecting every permission.
	selectTemporaryOAuthScopes = func(_ *cobra.Command, values []string) ([]string, error) {
		selectedCatalog = len(values) == 2 && values[0] == "app.sdk.read" && values[1] == "service.read"
		return []string{"service.read"}, nil
	}
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	registeredName := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Discovery is public, while registration carries only the reviewed subset.
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			if r.Header.Get("X-API-Key") != "" {
				t.Error("scope discovery sent an API key")
			}
			_, _ = w.Write([]byte(`{"scopes_supported":["service.read","app.sdk.read"]}`))
		case "/oauth/register":
			var input api.TemporaryOAuthClientRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.Scopes) != 1 || input.Scopes[0] != "service.read" || !strings.HasPrefix(input.Name, "Local client ") {
				t.Errorf("registration input = %#v, %v", input, err)
			}
			registeredName = input.Name
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"client_id":"foc_test","client_secret":"fos_once","client_id_expires_at":%q}`, expiry.Format(time.RFC3339))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	}))
	defer server.Close()
	EngineURL, APIKey = server.URL, "ambient-control-key"
	command := newTemporaryOAuthClientCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--redirect-uri", "http://localhost:4321/callback"})
	// The human receipt must identify the generated label and the returned one-time pair.
	if err := command.Execute(); err != nil || !selectedCatalog || !strings.Contains(output.String(), "Name: "+registeredName) || !strings.Contains(output.String(), "Client secret: fos_once") {
		t.Fatalf("interactive registration = %v, catalog=%v, output=%q", err, selectedCatalog, output.String())
	}
}

// TestTemporaryOAuthClientNoScopeInJSONModeRejectsBeforeNetwork prevents unattended clients from silently choosing authority.
func TestTemporaryOAuthClientNoScopeInJSONModeRejectsBeforeNetwork(t *testing.T) {
	oldURL, oldNoInput := EngineURL, NoInput
	t.Cleanup(func() { EngineURL, NoInput = oldURL, oldNoInput })
	NoInput = false
	t.Setenv("CI", "false")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	EngineURL = server.URL
	command := newTemporaryOAuthClientCommand()
	command.SetArgs([]string{"--redirect-uri", "http://localhost:4321/callback", "--json"})
	err := command.Execute()
	// Machine-readable output cannot trigger an interactive scope picker or a mutation.
	if err == nil || !strings.Contains(err.Error(), "--scope is required") || calls != 0 {
		t.Fatalf("missing-scope result = %v, calls=%d", err, calls)
	}
}

// TestTemporaryOAuthClientCommandRejectsUnsafeInput proves local admission does not create registrations.
func TestTemporaryOAuthClientCommandRejectsUnsafeInput(t *testing.T) {
	for _, test := range []struct {
		name, redirect string
		scopes         []string
	}{
		{"remote callback", "https://example.com/callback", []string{"service.read"}},
		{"missing scope", "http://localhost:4321/callback", nil},
		{"duplicate scope", "http://localhost:4321/callback", []string{"service.read", "service.read"}},
	} {
		// Unsafe input must fail before an anonymous network mutation is attempted.
		t.Run(test.name, func(t *testing.T) {
			if err := validateTemporaryOAuthClientOptions(temporaryOAuthClientOptions{redirectURI: test.redirect, scopes: test.scopes}); err == nil {
				t.Fatal("unsafe registration input accepted")
			}
		})
	}
}

// TestTemporaryOAuthClientCommandKeepsSecretOutOfErrors protects server failures from leaking a credential-shaped body.
func TestTemporaryOAuthClientCommandKeepsSecretOutOfErrors(t *testing.T) {
	// An untrusted error body must not be projected into terminal output.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"client_secret":"fos_do_not_echo"}`))
	}))
	defer server.Close()
	oldURL, oldKey := EngineURL, APIKey
	// Restore global CLI options so the test cannot change later commands.
	t.Cleanup(func() { EngineURL, APIKey = oldURL, oldKey })
	EngineURL, APIKey = server.URL, "ambient-control-key"
	command := newTemporaryOAuthClientCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--redirect-uri", "http://127.0.0.1:4321/callback", "--scope", "service.read"})
	err := command.Execute()
	// Failure must not print the one-time pair or reflect an untrusted server body.
	if err == nil || strings.Contains(output.String(), "fos_do_not_echo") || strings.Contains(err.Error(), "fos_do_not_echo") {
		t.Fatalf("registration failure = %v, output=%q", err, output.String())
	}
}
