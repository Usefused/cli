package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
)

// TestWorkspaceApply_WarnsWhenEngineIsProduction is Task 8's CLI-side AC
// (engine_workspace_registration_plan.md): before a destructive `workspace
// apply`, the CLI checks the Engine's /health echo of its --environment
// label and warns when it's "production".
func TestWorkspaceApply_WarnsWhenEngineIsProduction(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok","plane":"engine","environment":"production"}`))
		case "/workspace/config/apply":
			w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "apply", "-f", path})

	if !strings.Contains(out, "production") {
		t.Fatalf("expected a production warning in output, got %q", out)
	}
}

// TestWorkspaceApply_NoWarningWhenEngineIsNotProduction confirms the warning
// is scoped to "production" specifically -- a labeled non-production Engine
// shouldn't nag the operator on every apply.
func TestWorkspaceApply_NoWarningWhenEngineIsNotProduction(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
		case "/workspace/config/apply":
			w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "apply", "-f", path})

	if strings.Contains(out, "production") {
		t.Fatalf("expected no production warning for a staging engine, got %q", out)
	}
}

// TestWorkspaceApply_HealthCheckFailureDoesNotBlockApply is the fail-open
// AC: the production warning is advisory only, so a health-check failure
// (offline Engine, 404 from an older build without /health, etc.) must never
// prevent the apply itself from proceeding.
func TestWorkspaceApply_HealthCheckFailureDoesNotBlockApply(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash})

	var sawApply bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusNotFound)
		case "/workspace/config/apply":
			sawApply = true
			w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "apply", "-f", path})

	if !sawApply {
		t.Fatal("expected apply to proceed even when the health check fails")
	}
}
