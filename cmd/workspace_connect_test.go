package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWorkspaceServiceConnectStartsSession covers the CLI path users run,
// including service enablement and explicit bucket targeting before session start.
func TestWorkspaceServiceConnectStartsSession(t *testing.T) {
	dir := t.TempDir()
	var sawConnect bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-github"}}}`))
		case "/engine/graphql":
			body := decodeTestGraphQLBody(t, r)
			if strings.Contains(body.Query, "workspaceServices") {
				_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-01","enabled_versions":[{"version":"2026-07-01","service_version_id":"ver-1"}]}]}}`))
				return
			}
			if strings.Contains(body.Query, "startConnectSession") {
				sawConnect = true
				assertConnectSessionGraphQLRequest(t, body)
				_, _ = w.Write([]byte(`{"data":{"startConnectSession":{"authorize_url":"https://provider.example/authorize?state=abc","expires_at":"2026-07-20T23:00:00Z"}}}`))
				return
			}
			t.Fatalf("unexpected engine graphql query: %s", body.Query)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "connect", "github", "--bucket", "11111111-1111-4111-8111-111111111111", "--user-ref", "user_123", "--type", "oauth", "--auth-name", "targetOAuth", "--auth-ref", "${bucket.auth.gmail.oauth2}", "--resource-input", "subdomain=acme", "--scope", "repo:read", "--scope", "offline_access"})
	if !sawConnect {
		t.Fatal("expected connect session request")
	}
	if !strings.Contains(out, "https://provider.example/authorize?state=abc") {
		t.Fatalf("expected authorize URL output, got %q", out)
	}
}

// assertConnectSessionGraphQLRequest verifies the CLI sends only stable connect
// identifiers; provider token generation/storage remains Engine-owned.
func assertConnectSessionGraphQLRequest(t *testing.T, body testGraphQLBody) {
	t.Helper()
	assertStandaloneConnectIdentity(t, body.Variables)
	assertStandaloneConnectRouting(t, body.Variables)
	assertStandaloneConnectInputAndScopes(t, body.Variables)
}

// assertStandaloneConnectIdentity proves the CLI sends bucket/user identity without impersonating a generated app.
func assertStandaloneConnectIdentity(t *testing.T, variables map[string]interface{}) {
	t.Helper()
	if variables["bucketId"] != "11111111-1111-4111-8111-111111111111" || variables["serviceId"] != "svc-github" || variables["endUserRef"] != "user_123" {
		t.Fatalf("unexpected connect session variables: %#v", variables)
	}
	// Only generated clients may supply immutable app provenance.
	if createdBy, ok := variables["createdByAppId"]; ok && createdBy != "" {
		t.Fatalf("standalone connect must not impersonate an SDK: %#v", variables)
	}
}

// assertStandaloneConnectRouting checks the explicit app-agnostic source selector sent by initialization.
func assertStandaloneConnectRouting(t *testing.T, variables map[string]interface{}) {
	t.Helper()
	// Standalone source routing is explicit and never carries a generated-app identity.
	if variables["authType"] != "oauth" || variables["authName"] != "targetOAuth" || variables["authRef"] != "${bucket.auth.gmail.oauth2}" {
		t.Fatalf("unexpected connect auth routing: %#v", variables)
	}
}

// assertStandaloneConnectInputAndScopes verifies non-secret user input remains exact across the GraphQL adapter.
func assertStandaloneConnectInputAndScopes(t *testing.T, variables map[string]interface{}) {
	t.Helper()
	resourceInput, ok := variables["resourceInput"].(map[string]interface{})
	if !ok || resourceInput["subdomain"] != "acme" {
		t.Fatalf("unexpected resource input: %#v", variables["resourceInput"])
	}
	scopes, ok := variables["scopes"].([]interface{})
	if !ok || len(scopes) != 2 || scopes[0] != "repo:read" || scopes[1] != "offline_access" {
		t.Fatalf("unexpected connect scopes: %#v", variables["scopes"])
	}
}

// TestParseResourceInputFlags rejects malformed values before a connect session
// is created, so untrusted tenant input never becomes callback state.
func TestParseResourceInputFlags(t *testing.T) {
	values, err := parseResourceInputFlags([]string{"subdomain=acme", "region=eu"})
	if err != nil || values["subdomain"] != "acme" || values["region"] != "eu" {
		t.Fatalf("unexpected parsed inputs: values=%#v err=%v", values, err)
	}
	if _, err := parseResourceInputFlags([]string{"missing-separator"}); err == nil {
		t.Fatal("expected malformed resource input to fail")
	}
}

// TestWorkspaceServiceConnectCredentialFlagsKeepsAppIdentityOutOfRouting locks the public control-plane grammar.
func TestWorkspaceServiceConnectCredentialFlagsKeepsAppIdentityOutOfRouting(t *testing.T) {
	// Generated app identities stay on generated runtime clients, while auth-ref carries the complete standalone source selector.
	if workspaceServiceConnectCmd.Flags().Lookup("mcp") != nil || workspaceServiceConnectCmd.Flags().Lookup("sdk") != nil || workspaceServiceConnectCmd.Flags().Lookup("auth-ref") == nil {
		t.Fatal("workspace service connect flags do not preserve app-agnostic credential routing")
	}
}
