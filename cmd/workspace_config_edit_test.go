package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
)

// TestWorkspaceConfigEditPathDefaultsToScaffold verifies standalone edits find the workspace created by init.
func TestWorkspaceConfigEditPathDefaultsToScaffold(t *testing.T) {
	if got := workspaceConfigEditPath(""); got != filepath.Join(".fused", "workspace.yaml") {
		t.Fatalf("default workspace edit path = %q", got)
	}
	// Explicit configuration remains authoritative over project-local discovery.
	if got := workspaceConfigEditPath("custom.yaml"); got != "custom.yaml" {
		t.Fatalf("explicit workspace edit path = %q", got)
	}
}

// TestWorkspaceServiceMatchesReferenceAcceptsSlug verifies force removal uses the same identifier shown by workspace list.
func TestWorkspaceServiceMatchesReferenceAcceptsSlug(t *testing.T) {
	service := cliapi.WorkspaceService{ServiceName: "Gmail API", ServiceSlug: "gmail"}
	if !workspaceServiceMatchesReference(service, " GMAIL ") {
		t.Fatal("expected Registry slug to identify the enabled workspace service")
	}
	// Unrelated text must not become an accidental destructive match.
	if workspaceServiceMatchesReference(service, "google") {
		t.Fatal("unexpected partial service match")
	}
}

// TestRemoveWorkspaceFinalVersionDropsService verifies the resulting YAML remains valid for an Engine removal plan.
func TestRemoveWorkspaceFinalVersionDropsService(t *testing.T) {
	path := writeSprintConfig(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  gmail:
    versions: [{version: v1}]
`)
	if err := removeWorkspaceVersion(path, "gmail", "v1"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// An empty services map is valid; a retained gmail entry with no version is not.
	if strings.Contains(string(after), "gmail:") {
		t.Fatalf("final-version removal retained an invalid empty service:\n%s", after)
	}
}

// TestRemoveWorkspaceUnknownVersionPreservesFile rejects typos without silently changing local intent.
func TestRemoveWorkspaceUnknownVersionPreservesFile(t *testing.T) {
	path := writeSprintConfig(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  gmail:
    versions: [{version: v1}]
`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeWorkspaceVersion(path, "gmail", "v2"); err == nil {
		t.Fatal("expected unknown version removal to fail")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Failure must be non-destructive so the user can correct the version and retry.
	if string(after) != string(before) {
		t.Fatalf("unknown version removal changed file:\n%s", after)
	}
}
