package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
)

// TestMaterializeUnifiedAppSourcePreservesConfig checks inline migration and later plan hashing.
func TestMaterializeUnifiedAppSourcePreservesConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	path := filepath.Join(".fused", "unified_app", "customer.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := "export default buildUnifiedApp({});\n"
	document := "# reviewed app\napiVersion: fused/v1\nkind: unified_app\nname: customer\nversion: 1.0.0\nbucket: default\nservices:\n  crm:\n    version: v1\n    operations: [createCustomer]\nsource: |\n  export default buildUnifiedApp({});\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := materializeUnifiedAppSource(path, false)
	if err != nil || !changed {
		t.Fatalf("materialize = %v, %v", changed, err)
	}
	saved, err := os.ReadFile(filepath.Join(".fused", "unified_app", "customer.tsx"))
	if err != nil || string(saved) != source {
		t.Fatalf("source = %q, %v", saved, err)
	}
	config, err := os.ReadFile(path)
	// YAML retains the reviewed comment and service scope while dropping embedded code.
	if err != nil || !strings.Contains(string(config), "# reviewed app") || !strings.Contains(string(config), "source_path: customer.tsx") || strings.Contains(string(config), "source: |") {
		t.Fatalf("config = %s, %v", config, err)
	}
	parsed, err := configfile.ParseFile(path)
	if err != nil || parsed.UnifiedApp.Source != source {
		t.Fatalf("resolved source = %#v, %v", parsed, err)
	}
	changed, err = materializeUnifiedAppSource(path, false)
	if err != nil || changed {
		t.Fatalf("repeat materialize = %v, %v", changed, err)
	}
}

// TestMaterializeUnifiedAppSourceRejectsConflict protects another version's canonical source.
func TestMaterializeUnifiedAppSourceRejectsConflict(t *testing.T) {
	t.Chdir(t.TempDir())
	path := filepath.Join(".fused", "unified_app", "customer.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	document := "apiVersion: fused/v1\nkind: unified_app\nname: customer\nversion: 1.0.0\nbucket: default\nservices:\n  crm:\n    version: v1\n    operations: [createCustomer]\nsource: 'export default buildUnifiedApp({});'\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(filepath.Dir(path), "customer.tsx")
	if err := os.WriteFile(sourcePath, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := materializeUnifiedAppSource(path, true)
	if err == nil || changed {
		t.Fatalf("conflicting source = %v, %v", changed, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != document {
		t.Fatalf("config changed after conflict: %s, %v", after, err)
	}
}
