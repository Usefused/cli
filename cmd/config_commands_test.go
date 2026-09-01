package cmd_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/cmd"
)

func TestConfigCommandSurface(t *testing.T) {
	tests := [][]string{
		{"workspace", "init", "-f", "workspace.yaml"},
		{"sdk", "init", "security", "-f", "sdk.yaml"},
		{"mcp", "init", "support", "-f", "mcp.yaml", "--extend"},
		{"plan", "-f", "fused.yaml"},
		{"apply", "-f", "fused.yaml"},
		{"validate", "-f", "fused.yaml"},
		{"workspace", "plan", "-f", "workspace.yaml"},
		{"workspace", "apply", "-f", "workspace.yaml"},
		{"workspace", "services", "list"},
		{"workspace", "services", "list", "--interactive"},
		{"workspace", "has", "okta"},
		{"workspace", "service", "add", "okta", "--version", "2026-07-01"},
		{"workspace", "service", "versions", "okta"},
		{"workspace", "service", "delete", "okta"},
		{"workspace", "service", "version", "add", "okta", "2026-07-01"},
		{"workspace", "service", "version", "delete", "okta", "2026-07-01", "--force"},
		{"service", "versions", "okta"},
		{"service", "show", "okta"},
		{"service", "operations", "okta", "--q", "users"},
		{"service", "webhooks", "okta"},
		{"sdk", "plan", "-f", "fused.yaml"},
		{"sdk", "apply", "-f", "fused.yaml"},
		{"sdk", "validate", "-f", "fused.yaml"},
		{"sdk", "download", "security@1.0.0"},
		{"sdk", "download", "-f", "fused.yaml"},
		{"sdk", "show", "security@1.0.0"},
		{"sdk", "services", "security@1.0.0"},
		{"sdk", "buckets", "security"},
		{"sdk", "deactivate", "security@1.0.0"},
		{"sdk", "token", "generate", "security", "external-test", "--expires-in", "4h"},
		{"sdk", "token", "list", "security"},
		{"sdk", "token", "revoke", "security", "local-dev"},
		{"sdk", "service", "add", "okta", "-f", "fused.yaml", "--version", "2026-07-01"},
		{"sdk", "service", "remove", "okta", "-f", "fused.yaml"},
		{"sdk", "operation", "add", "okta", "listLogEvents", "-f", "fused.yaml"},
		{"sdk", "operation", "remove", "okta", "listLogEvents", "-f", "fused.yaml"},
		{"sdk", "webhook", "add", "okta", "user.created", "-f", "fused.yaml"},
		{"sdk", "webhook", "remove", "okta", "user.created", "-f", "fused.yaml"},
		{"mcp", "plan", "-f", "mcp.yaml"},
		{"mcp", "apply", "-f", "mcp.yaml"},
		{"mcp", "validate", "-f", "mcp.yaml"},
		{"mcp", "deactivate", "support@1.0.0"},
		{"mcp", "token", "generate", "support", "agent", "--allow", "issues.list", "--expires-in", "15m"},
		{"mcp", "token", "list", "support"},
		{"mcp", "token", "revoke", "support", "agent"},
		{"webhook", "plan", "-f", "webhook.yaml"},
		{"webhook", "apply", "-f", "webhook.yaml"},
		{"webhook", "validate", "-f", "webhook.yaml"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command := cmd.NewRootCommand()
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})

			// Use the command's HelpFunc or call Help() directly rather than parsing --help
			// to avoid permanently mutating the global RootCmd state.
			command.SetArgs(args)
			if err := command.Help(); err != nil {
				t.Fatalf("expected command help to succeed: %v", err)
			}
		})
	}
}

func TestValidateCommandResolvesFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfigCommandFile(t, dir, "fused.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)

	command := cmd.NewRootCommand()
	out := &bytes.Buffer{}
	command.SetOut(out)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"validate", "-f", path})

	if err := command.Execute(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if !strings.Contains(out.String(), "validated 1 config") {
		t.Fatalf("expected validation summary, got %q", out.String())
	}
}

func TestPlanCommandDiscoversFusedFolder(t *testing.T) {
	dir := t.TempDir()
	writeConfigCommandFile(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspace/config/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"hash","base_generation":0,"summary":{}}`))
	}))
	defer server.Close()

	command := cmd.NewRootCommand()
	out := &bytes.Buffer{}
	command.SetOut(out)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"--engine-url", server.URL, "--key", "fsk_test", "plan"})

	if err := command.Execute(); err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".fused/.state/workspace.plan.json")); err != nil {
		t.Fatalf("expected plan receipt: %v", err)
	}
}

func TestJSONPlanStillWritesDefaultReceipt(t *testing.T) {
	dir := t.TempDir()
	writeConfigCommandFile(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"plan_id":"plan-json","config_key":"workspace","source_hash":"hash","base_generation":0,"summary":{}}`))
	}))
	defer server.Close()

	command := cmd.NewRootCommand()
	command.SetArgs([]string{"--engine-url", server.URL, "--key", "fsk_test", "plan", "--json"})
	if err := command.Execute(); err != nil {
		t.Fatalf("json plan failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".fused/.state/workspace.plan.json")); err != nil {
		t.Fatalf("expected json plan receipt: %v", err)
	}
}

func writeConfigCommandFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
