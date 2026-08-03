package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. printAppliedWebhooks writes via fmt.Printf
// directly (matching the rest of this command's output), so a pipe swap
// is the only way to observe it from a unit test.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	fn()

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// ─── appliedWebhookURL / printAppliedWebhooks (pure unit tests) ───────────

func TestAppliedWebhookURL_BuildsSlugServiceKeyPath(t *testing.T) {
	got := appliedWebhookURL("https://engine.example.com", api.AppliedWebhookConfig{
		ServiceKey: "github",
		Label:      "repo-a",
		Slug:       "abc123XYZ",
	})
	want := "https://engine.example.com/webhook/abc123XYZ-github"
	if got != want {
		t.Fatalf("appliedWebhookURL: got %q, want %q", got, want)
	}
}

func TestAppliedWebhookURL_TrimsTrailingSlashOnBaseURL(t *testing.T) {
	got := appliedWebhookURL("https://engine.example.com/", api.AppliedWebhookConfig{
		ServiceKey: "github",
		Slug:       "abc123",
	})
	want := "https://engine.example.com/webhook/abc123-github"
	if got != want {
		t.Fatalf("expected trailing slash on baseURL trimmed, got %q", got)
	}
}

func TestPrintAppliedWebhooks_PrintsOneLinePerRegistration(t *testing.T) {
	out := captureStdout(t, func() {
		printAppliedWebhooks("https://engine.example.com", []api.AppliedWebhookConfig{
			{ServiceKey: "github", Label: "repo-a", Slug: "slug-a"},
			{ServiceKey: "github", Label: "repo-b", Slug: "slug-b"},
		})
	})
	if !strings.Contains(out, `"repo-a" -> https://engine.example.com/webhook/slug-a-github`) {
		t.Fatalf("expected repo-a line in output, got:\n%s", out)
	}
	if !strings.Contains(out, `"repo-b" -> https://engine.example.com/webhook/slug-b-github`) {
		t.Fatalf("expected repo-b line in output, got:\n%s", out)
	}
}

func TestPrintAppliedWebhooks_NoOutputWhenNoWebhooks(t *testing.T) {
	out := captureStdout(t, func() {
		printAppliedWebhooks("https://engine.example.com", nil)
	})
	if out != "" {
		t.Fatalf("expected no output for a service with no webhook registrations, got:\n%s", out)
	}
}

// TestWorkspaceApplyPrintsWebhookURLs was removed along with
// runtime_config.webhooks (no backward compatibility -- see
// plans/plan-webhook-kind.md): `workspace apply` no longer creates or
// reports webhook registrations at all, that's kind: webhook's job now.
// appliedWebhookURL/printAppliedWebhooks themselves stay (still exercised
// by the pure unit tests above, and reused by the new `fused-cli webhook
// apply` command).

// ─── Task 8: `workspace service webhooks <slug>` visibility command ───────

func TestWorkspaceServiceWebhooks_ListsRegistrationsWithReconstructedURL(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleWorkspaceWebhookListRequest(t, w, r)
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "webhooks", "github"})

	if !strings.Contains(out, "repo-a") || !strings.Contains(out, server.URL+"/webhook/slugaaaaaaaaaaaaaaaaa-github") {
		t.Fatalf("expected repo-a URL line, got:\n%s", out)
	}
	if !strings.Contains(out, "repo-b") || !strings.Contains(out, server.URL+"/webhook/slugbbbbbbbbbbbbbbbbb-github") {
		t.Fatalf("expected repo-b URL line, got:\n%s", out)
	}
	// The endpoint never sends a signing secret back, so there is nothing to
	// leak -- this assertion documents that expectation at the CLI boundary.
	if strings.Contains(out, "whsec") {
		t.Fatalf("output should never contain a signing secret, got:\n%s", out)
	}
}

func handleWorkspaceWebhookListRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	switch r.URL.Path {
	case "/graphql":
		writeServiceLookupForWebhookList(t, w, r)
	case "/engine/graphql":
		writeEngineWebhookListResponse(t, w, r)
	default:
		t.Fatalf("unexpected path %s", r.URL.Path)
	}
}

func writeServiceLookupForWebhookList(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode graphql body: %v", err)
	}
	if !strings.Contains(body.Query, "GetServiceInfo") {
		t.Fatalf("unexpected graphql query: %s", body.Query)
	}
	_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-github"}}}`))
}

func writeEngineWebhookListResponse(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	body := decodeTestGraphQLBody(t, r)
	if strings.Contains(body.Query, "workspaceServices") {
		_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-01"}]}}`))
		return
	}
	if strings.Contains(body.Query, "workspaceWebhooks") {
		_, _ = w.Write([]byte(`{"data":{"workspaceWebhooks":[
			{"label":"repo-a","slug":"slugaaaaaaaaaaaaaaaaa","created_at":"2026-07-18T00:00:00Z"},
			{"label":"repo-b","slug":"slugbbbbbbbbbbbbbbbbb","created_at":"2026-07-18T00:00:00Z"}
		]}}`))
		return
	}
	t.Fatalf("unexpected engine graphql query: %s", body.Query)
}

func TestWorkspaceServiceWebhooks_NoRegistrations_PrintsMessage(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-github"}}}`))
		case r.URL.Path == "/engine/graphql":
			body := decodeTestGraphQLBody(t, r)
			if strings.Contains(body.Query, "workspaceServices") {
				_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-01"}]}}`))
				return
			}
			if strings.Contains(body.Query, "workspaceWebhooks") {
				_, _ = w.Write([]byte(`{"data":{"workspaceWebhooks":[]}}`))
				return
			}
			t.Fatalf("unexpected engine graphql query: %s", body.Query)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "webhooks", "github"})
	if !strings.Contains(out, "No webhook registrations for service github.") {
		t.Fatalf("expected no-registrations message, got:\n%s", out)
	}
}
