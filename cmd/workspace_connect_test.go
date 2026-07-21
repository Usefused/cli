package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWorkspaceServiceConnectStartsSession covers the CLI path users run,
// including service enablement and bucket-name resolution before session start.
func TestWorkspaceServiceConnectStartsSession(t *testing.T) {
	dir := t.TempDir()
	var sawConnect bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-1","service_id":"svc-github","name":"2026-07-01","status":"public","created_at":"2026-07-16T00:00:00Z"}]}}`))
		case "/engine/graphql":
			body := decodeTestGraphQLBody(t, r)
			if strings.Contains(body.Query, "workspaceServices") {
				_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-01","enabled_versions":[{"version":"2026-07-01","service_version_id":"ver-1"}]}]}}`))
				return
			}
			if strings.Contains(body.Query, "buckets") {
				_, _ = w.Write([]byte(`{"data":{"buckets":[{"id":"bucket-prod","name":"prod","is_default":false,"created_at":"2026-07-21T00:00:00Z"}]}}`))
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

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "github", "connect", "--bucket", "prod", "--user-ref", "user_123"})
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
	if body.Variables["bucketId"] != "bucket-prod" || body.Variables["serviceId"] != "svc-github" || body.Variables["endUserRef"] != "user_123" {
		t.Fatalf("unexpected connect session variables: %#v", body.Variables)
	}
}
