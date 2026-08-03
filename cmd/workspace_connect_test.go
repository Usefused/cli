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

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "connect", "github", "--bucket", "11111111-1111-4111-8111-111111111111", "--user-ref", "user_123", "--resource-input", "subdomain=acme", "--scope", "repo:read", "--scope", "offline_access"})
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
	if body.Variables["bucketId"] != "11111111-1111-4111-8111-111111111111" || body.Variables["serviceId"] != "svc-github" || body.Variables["endUserRef"] != "user_123" {
		t.Fatalf("unexpected connect session variables: %#v", body.Variables)
	}
	resourceInput, ok := body.Variables["resourceInput"].(map[string]interface{})
	if !ok || resourceInput["subdomain"] != "acme" {
		t.Fatalf("unexpected resource input: %#v", body.Variables["resourceInput"])
	}
	scopes, ok := body.Variables["scopes"].([]interface{})
	if !ok || len(scopes) != 2 || scopes[0] != "repo:read" || scopes[1] != "offline_access" {
		t.Fatalf("unexpected connect scopes: %#v", body.Variables["scopes"])
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
