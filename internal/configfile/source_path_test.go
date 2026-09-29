package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnifiedAppSourcePathBindsFileBytes verifies the local file enters the plan cart and receipt identity.
func TestUnifiedAppSourcePathBindsFileBytes(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".fused", "unified_app")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "customer.yaml")
	sourcePath := filepath.Join(configDir, "customer.tsx")
	document := "apiVersion: fused/v1\nkind: unified_app\nname: customer\nversion: 1.0.0\nbucket: default\nsource_path: customer.tsx\nservices:\n  crm:\n    version: v1\n    operations: [createCustomer]\n"
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	firstSource := "export default buildUnifiedApp({});\n"
	if err := os.WriteFile(sourcePath, []byte(firstSource), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(first.UnifiedApp)
	// The Engine receives only source bytes, never a local filesystem path.
	if err != nil || first.UnifiedApp.Source != firstSource || strings.Contains(string(serialized), "source_path") || !strings.Contains(string(serialized), "buildUnifiedApp") {
		t.Fatalf("resolved source = %#v, JSON = %s, error = %v", first.UnifiedApp, serialized, err)
	}
	if err := os.WriteFile(sourcePath, []byte(firstSource+"// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := ParseFile(path)
	// A source edit must invalidate the previous plan without changing the YAML.
	if err != nil || second.SourceHash == first.SourceHash {
		t.Fatalf("source edit kept plan identity: %v", err)
	}
}

// TestUnifiedAppSourcePathRejectsCompetingInput keeps each plan tied to one executable authority.
func TestUnifiedAppSourcePathRejectsCompetingInput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.yaml")
	if err := os.WriteFile(filepath.Join(root, "app.tsx"), []byte("export default buildUnifiedApp({});"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := "apiVersion: fused/v1\nkind: unified_app\nname: app\nversion: 1.0.0\nbucket: default\nsource_path: app.tsx\nservices:\n  crm:\n    version: v1\n    operations: [createCustomer]\n"
	for _, item := range []struct{ name, document string }{
		{name: "inline source", document: base + "source: 'other'\n"},
		{name: "digest", document: base + "bundle_digest: sha256:" + strings.Repeat("a", 64) + "\n"},
		{name: "absolute path", document: strings.Replace(base, "app.tsx", filepath.Join(root, "app.tsx"), 1)},
		{name: "unsupported extension", document: strings.Replace(base, "app.tsx", "app.py", 1)},
	} {
		t.Run(item.name, func(t *testing.T) {
			// Invalid source authority must fail before a request reaches Engine.
			if _, err := Parse([]byte(item.document), path); err == nil {
				t.Fatal("accepted invalid source path")
			}
		})
	}
}
