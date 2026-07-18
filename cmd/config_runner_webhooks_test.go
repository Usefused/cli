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
	"github.com/Usefused/cli/internal/configfile"
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

// ─── workspace apply end-to-end: response webhooks reach the terminal ─────

func TestWorkspaceApplyPrintsWebhookURLs(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    runtime_config:
      webhooks:
        repo-a:
          signing_secret: "whsec_a"
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
			return
		}
		if r.URL.Path != "/workspace/config/apply" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace","webhooks":[{"service_key":"github","label":"repo-a","slug":"abc123"}]}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "apply", "-f", path})
	wantLine := server.URL + "/webhook/abc123-github"
	if !strings.Contains(out, wantLine) {
		t.Fatalf("expected apply output to include webhook URL %q, got:\n%s", wantLine, out)
	}
	if !strings.Contains(out, `"repo-a"`) {
		t.Fatalf("expected apply output to include the webhook label, got:\n%s", out)
	}
}

// ─── Task 8: `workspace service <slug> webhooks` visibility command ───────

func TestWorkspaceServiceWebhooks_ListsRegistrationsWithReconstructedURL(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			var body struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql body: %v", err)
			}
			if !strings.Contains(body.Query, "serviceVersions") {
				t.Fatalf("unexpected graphql query: %s", body.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-latest","service_id":"svc-github","name":"2026-07-01","status":"public","created_at":"2026-07-01T00:00:00Z"}]}}`))
		case r.URL.Path == "/workspace/services":
			_, _ = w.Write([]byte(`[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-01"}]`))
		case r.URL.Path == "/workspace/services/svc-github/webhooks":
			_, _ = w.Write([]byte(`[
				{"label":"repo-a","slug":"slugaaaaaaaaaaaaaaaaa","created_at":"2026-07-18T00:00:00Z"},
				{"label":"repo-b","slug":"slugbbbbbbbbbbbbbbbbb","created_at":"2026-07-18T00:00:00Z"}
			]`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "github", "webhooks"})

	if !strings.Contains(out, "repo-a\t"+server.URL+"/webhook/slugaaaaaaaaaaaaaaaaa-github") {
		t.Fatalf("expected repo-a URL line, got:\n%s", out)
	}
	if !strings.Contains(out, "repo-b\t"+server.URL+"/webhook/slugbbbbbbbbbbbbbbbbb-github") {
		t.Fatalf("expected repo-b URL line, got:\n%s", out)
	}
	// The endpoint never sends a signing secret back, so there is nothing to
	// leak -- this assertion documents that expectation at the CLI boundary.
	if strings.Contains(out, "whsec") {
		t.Fatalf("output should never contain a signing secret, got:\n%s", out)
	}
}

func TestWorkspaceServiceWebhooks_NoRegistrations_PrintsMessage(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-latest","service_id":"svc-github","name":"2026-07-01","status":"public","created_at":"2026-07-01T00:00:00Z"}]}}`))
		case r.URL.Path == "/workspace/services":
			_, _ = w.Write([]byte(`[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-01"}]`))
		case r.URL.Path == "/workspace/services/svc-github/webhooks":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "github", "webhooks"})
	if !strings.Contains(out, "No webhook registrations for service github.") {
		t.Fatalf("expected no-registrations message, got:\n%s", out)
	}
}
