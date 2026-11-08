package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceOwnedAppFixture supplies one valid declaration with a code body that changes independently.
func sourceOwnedAppFixture(body string) string {
	return "export const fusedConfig = {\n" +
		`  "name": "customer", "version": "1.0.0", "bucket": "default",` + "\n" +
		`  "services": {"crm": {"version": "v1", "operations": ["createCustomer"]}}` + "\n" +
		"};\nexport default buildUnifiedApp({ input: z.object({}), output: z.object({}), execute() { " + body + " } });\n"
}

// TestSourceOwnedUnifiedAppPlansFromOneFile checks Engine transport and receipt identity from TypeScript alone.
func TestSourceOwnedUnifiedAppPlansFromOneFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".fused", "unified_app")
	// Discovery should work without creating a sibling YAML declaration.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "customer.ts")
	firstSource := sourceOwnedAppFixture("return {}; /* first */")
	if err := os.WriteFile(path, []byte(firstSource), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	run := &Run{}
	if err := appendDesiredConfigs(run, dir, KindUnifiedApp); err != nil {
		t.Fatal(err)
	}
	// Default plan discovery must find the TypeScript-owned version without a YAML file.
	if len(run.Configs) != 1 || run.Configs[0].ConfigKey != first.ConfigKey {
		t.Fatalf("source-owned discovery = %#v", run.Configs)
	}
	wire, err := json.Marshal(first.UnifiedApp)
	// The generated cart carries all selected scope and source bytes, never the local path.
	if err != nil || first.ConfigKey != "unified_app:customer:1.0.0" || first.UnifiedApp.Source != firstSource || strings.Contains(string(wire), "source_path") || !strings.Contains(string(wire), `"apiVersion":"fused/v1"`) {
		t.Fatalf("source-owned cart = %#v, wire = %s, error = %v", first, wire, err)
	}
	secondSource := sourceOwnedAppFixture("return { changed: true }; /* second */")
	if err := os.WriteFile(path, []byte(secondSource), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := ParseFile(path)
	// Any code edit must invalidate an earlier plan receipt.
	if err != nil || second.SourceHash == first.SourceHash {
		t.Fatalf("source edit kept plan identity: %v", err)
	}
}

// TestSourceOwnedUnifiedAppRejectsCompetingAuthority checks local admission before Engine planning.
func TestSourceOwnedUnifiedAppRejectsCompetingAuthority(t *testing.T) {
	for _, field := range []string{`"source": "other",`, `"source_path": "other.ts",`, `"bundle_digest": "sha256:abc",`, `"kind": "sdk",`, `"unexpected": true,`} {
		// Authored metadata must never override source ownership or the fixed app kind.
		source := strings.Replace(sourceOwnedAppFixture("return {};"), `"name":`, field+` "name":`, 1)
		if _, err := ParseUnifiedAppSource([]byte(source), "app.ts"); err == nil {
			t.Fatalf("accepted conflicting field %s", field)
		}
	}
}

// TestSourceOwnedUnifiedAppRejectsAmbiguousMetadata prevents comments or duplicates from selecting deployment scope.
func TestSourceOwnedUnifiedAppRejectsAmbiguousMetadata(t *testing.T) {
	commented := "/*\n" + sourceOwnedAppFixture("return {};") + "\n*/\nexport default buildUnifiedApp({});"
	// A block comment is not an exported declaration even when a line matches the authoring spelling.
	if _, err := ParseUnifiedAppSource([]byte(commented), "app.ts"); err == nil {
		t.Fatal("commented metadata was accepted")
	}
	duplicate := sourceOwnedAppFixture("return {};") + "\n" + sourceOwnedAppFixture("return {};")
	// Repeated declarations must fail before one of them can silently choose the deployed scope.
	if _, err := ParseUnifiedAppSource([]byte(duplicate), "app.ts"); err == nil {
		t.Fatal("duplicate metadata was accepted")
	}
}

// TestUnifiedAppYAMLRejected requires a single TypeScript authority for both explicit and discovered files.
func TestUnifiedAppYAMLRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".fused", "unified_app")
	// Discovery must report an obsolete declaration rather than silently omit it.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyYAML := "apiVersion: fused/v1\nkind: unified_app\nname: legacy\nversion: 1.0.0\nbucket: default\nsource: 'export default buildUnifiedApp({});'\n"
	path := filepath.Join(dir, "legacy.yaml")
	if err := os.WriteFile(path, []byte(legacyYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	// Explicit selection and directory planning must give the same migration instruction.
	if _, err := ParseFile(path); err == nil || !strings.Contains(err.Error(), "fusedConfig") {
		t.Fatalf("explicit YAML result = %v", err)
	}
	run := &Run{}
	if err := appendDesiredConfigs(run, dir, KindUnifiedApp); err == nil || !strings.Contains(err.Error(), "fusedConfig") {
		t.Fatalf("discovered YAML result = %v", err)
	}
}
