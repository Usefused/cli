package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceServiceVersionAddLatest(t *testing.T) {
	dir := t.TempDir()

	// Create a dummy workspace.yaml
	workspaceYAML := `kind: workspace
api_version: v1
services:
  plunk:
    service_id: svc-plunk
    versions:
      - version: 1.0.0
`
	fusedDir := filepath.Join(dir, ".fused")
	if err := os.Mkdir(fusedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fusedDir, "workspace.yaml"), []byte(workspaceYAML), 0644); err != nil {
		t.Fatal(err)
	}

	var sawLatestQuery bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			body := decodeTestGraphQLBody(t, r)
			if strings.Contains(body.Query, "GetServiceLatestVersion") {
				sawLatestQuery = true
				if body.Variables["id"] != "plunk" {
					t.Fatalf("expected id variable plunk, got %v", body.Variables["id"])
				}
				_, _ = w.Write([]byte(`{"data":{"service":{"service_versions":[{"name":"1.1.0"}]}}}`))
				return
			}
			t.Fatalf("unexpected graphql query: %s", body.Query)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "version", "add", "plunk", "latest", "-f", filepath.Join(dir, ".fused", "workspace.yaml")})

	if !sawLatestQuery {
		t.Fatal("expected GetServiceLatestVersion request")
	}
	if !strings.Contains(out, "Resolved 'latest' to version 1.1.0 for service plunk") {
		t.Fatalf("expected resolution output, got %q", out)
	}
	if !strings.Contains(out, "Added version 1.1.0 to service plunk") {
		t.Fatalf("expected added version output, got %q", out)
	}

	// Verify workspace.yaml has the resolved version
	data, err := os.ReadFile(filepath.Join(fusedDir, "workspace.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "version: 1.0.0") || !strings.Contains(content, "version: 1.1.0") {
		t.Fatalf("expected both 1.0.0 and 1.1.0 in workspace.yaml, got:\n%s", content)
	}
}
