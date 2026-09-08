package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestMCPTokenGenerateRequestDefaultsToFullAccessWithoutExpiry(t *testing.T) {
	command := newMCPTokenGenerateTestCommand()
	request, err := mcpTokenGenerateRequest(command, "agent")
	if err != nil {
		t.Fatalf("mcpTokenGenerateRequest: %v", err)
	}
	if request.Name != "agent" || len(request.Allow) != 1 || request.Allow[0] != "*" {
		t.Fatalf("request = %#v", request)
	}
	if request.ExpiresIn != nil {
		t.Fatalf("default expiry = %#v, want nil", request.ExpiresIn)
	}
}

func TestMCPTokenGenerateRequestParsesScopeAndExpiry(t *testing.T) {
	command := newMCPTokenGenerateTestCommand()
	if err := command.Flags().Set("allow", "issues.list,issues.create"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("expires-in", "15m"); err != nil {
		t.Fatal(err)
	}

	request, err := mcpTokenGenerateRequest(command, "support-agent")
	if err != nil {
		t.Fatalf("mcpTokenGenerateRequest: %v", err)
	}
	if strings.Join(request.Allow, ",") != "issues.list,issues.create" {
		t.Fatalf("allow = %#v", request.Allow)
	}
	if request.ExpiresIn == nil || *request.ExpiresIn != 900 {
		t.Fatalf("expires_in = %#v, want 900", request.ExpiresIn)
	}
}

func TestMCPTokenGenerateRequestParsesPerServiceFixedBindings(t *testing.T) {
	command := newMCPTokenGenerateTestCommand()
	values := []string{
		"@google/gmail,gmail,customer-mail",
		"google-drive,drive,customer-drive,33333333-3333-4333-8333-333333333333",
	}
	for _, value := range values {
		if err := command.Flags().Set("fixed-binding", value); err != nil {
			t.Fatal(err)
		}
	}

	request, err := mcpTokenGenerateRequest(command, "customer-agent")

	if err != nil {
		t.Fatalf("fixed request = %#v/%v", request, err)
	}
	if request.BindingMode != "fixed" || len(request.Bindings) != 2 {
		t.Fatalf("fixed bindings = %#v", request.Bindings)
	}
	assertPerServiceFixedBindings(t, request.Bindings)
}

// TestMCPTokenGenerateJSONReturnsOneTimeCredential verifies the public command emits the complete issuance result without human prose.
func TestMCPTokenGenerateJSONReturnsOneTimeCredential(t *testing.T) {
	// The fixture exposes only family resolution and issuance so any extra request fails the test.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Family resolution must precede the token mutation and retain the MCP kind boundary.
		switch r.URL.Path {
		case "/engine/graphql":
			_, _ = w.Write([]byte(`{"data":{"appFamilyReference":{"id":"family-1","kind":"mcp"}}}`))
		case "/workspace/app-tokens":
			// The issuance endpoint must be called exactly as a token-creation mutation.
			if r.Method != http.MethodPost {
				t.Fatalf("token request method = %s", r.Method)
			}
			_, _ = w.Write([]byte(`{"id":"token-1","app_family_id":"family-1","name":"agent","allow":["*"],"binding_mode":"","binding_count":0,"token":"shown-once","created_at":"2026-09-08T12:00:00Z"}`))
		default:
			// An unexpected path would indicate an unreviewed fallback or extra mutation boundary.
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	output := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"mcp", "token", "generate", "support-mcp", "agent", "--json"})
	var result api.AppTokenGenerateResponse
	// Structured output must be valid JSON containing the one-time credential and family metadata.
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode token JSON %q: %v", output, err)
	}
	// Human copy instructions would corrupt the JSON envelope used by automation.
	if result.Token != "shown-once" || result.AppFamilyID != "family-1" || strings.Contains(output, "Token generated") {
		t.Fatalf("token JSON = %#v, output = %q", result, output)
	}
}

func assertPerServiceFixedBindings(t *testing.T, bindings []api.AppTokenBindingRequest) {
	t.Helper()
	if bindings[0].ServiceSlug != "@google/gmail" || bindings[0].EndUserRef != "customer-mail" {
		t.Fatalf("first fixed binding = %#v", bindings[0])
	}
	if bindings[1].ServiceSlug != "google-drive" || bindings[1].EndUserRef != "customer-drive" || bindings[1].ResourceID == nil {
		t.Fatalf("second fixed binding = %#v", bindings[1])
	}
}

func TestMCPTokenFixedBindingRejectsInvalidShape(t *testing.T) {
	for _, value := range []string{
		"missing", ",gmail,customer", "jira,,customer", "@provider/,gmail,customer",
		"de305d54-75b4-431b-adb2-eb6b9e546014,gmail,customer",
	} {
		if _, err := parseMCPTokenBinding(value); err == nil {
			t.Fatalf("accepted invalid fixed binding %q", value)
		}
	}
}

// TestAppTokenExpiryRejectsInvalidDurations protects the shared SDK and MCP duration contract.
func TestAppTokenExpiryRejectsInvalidDurations(t *testing.T) {
	// Representative invalid values protect positivity, precision, and syntax rules.
	for _, value := range []string{"0s", "-1s", "1500ms", "tomorrow"} {
		t.Run(value, func(t *testing.T) {
			command := newMCPTokenGenerateTestCommand()
			// Each case must reach the shared parser through the public flag value.
			if err := command.Flags().Set("expires-in", value); err != nil {
				t.Fatal(err)
			}
			// Invalid lifetimes must fail locally instead of being rounded or sent to Engine.
			if _, err := appTokenExpirySeconds(command); err == nil || !strings.Contains(err.Error(), "positive whole-second") {
				t.Fatalf("expiry error = %v", err)
			}
		})
	}
}

// TestAppTokenCommandSurface keeps structured issuance and shared expiry separate from MCP-only scope and binding controls.
func TestAppTokenCommandSurface(t *testing.T) {
	// All MCP token actions must remain executable while their generate flags evolve.
	for _, command := range []*cobra.Command{mcpTokenGenerateCmd, mcpTokenListCmd, mcpTokenRevokeCmd} {
		// Missing Cobra contracts would leave a visible but unusable command.
		if command.Args == nil || command.RunE == nil {
			t.Fatalf("%s is not an executable command", command.CommandPath())
		}
	}
	// MCP generation exposes structured one-time output alongside operation scope, expiry, and fixed connected-user bindings.
	if mcpTokenGenerateCmd.Flags().Lookup(jsonOutputFlag) == nil || mcpTokenGenerateCmd.Flags().Lookup("allow") == nil || mcpTokenGenerateCmd.Flags().Lookup("expires-in") == nil || mcpTokenGenerateCmd.Flags().Lookup("fixed-binding") == nil {
		t.Fatal("MCP token generation must expose JSON, scope, expiry, and binding flags")
	}
	// SDK tokens expose only expiry because trial access still needs the application's full operation surface.
	if sdkTokenGenerateCmd.Flags().Lookup("expires-in") == nil || sdkTokenGenerateCmd.Flags().Lookup("allow") != nil || sdkTokenGenerateCmd.Flags().Lookup("fixed-binding") != nil {
		t.Fatal("SDK token generation must expose expiry without MCP scope or binding flags")
	}
}

func newMCPTokenGenerateTestCommand() *cobra.Command {
	command := &cobra.Command{Use: "generate"}
	command.Flags().StringSlice("allow", []string{"*"}, "")
	command.Flags().String("expires-in", "", "")
	command.Flags().StringArray("fixed-binding", nil, "")
	return command
}
