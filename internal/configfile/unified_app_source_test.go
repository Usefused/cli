package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceOwnedAppFixture supplies current inline metadata with an independently editable code body.
func sourceOwnedAppFixture(body string) string {
	return "export default buildUnifiedApp({ config: {\n" +
		`  "name": "customer", "version": "1.0.0", "bucket": "default",` + "\n" +
		`  "services": {"crm": {"version": "v1", "operations": ["createCustomer"]}}` + "\n" +
		"}, input: z.object({}), output: z.object({}), execute() { " + body + " } });\n"
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
	if _, err := ParseFile(path); err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("explicit YAML result = %v", err)
	}
	run := &Run{}
	if err := appendDesiredConfigs(run, dir, KindUnifiedApp); err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("discovered YAML result = %v", err)
	}
}

// TestInlineUnifiedAppConfig proves canonical metadata is parsed without executing the implementation or confusing nested settings.
func TestInlineUnifiedAppConfig(t *testing.T) {
	metadata := []byte(`{"name":"customer","version":"1.0.0","bucket":"default","runtime_buckets":["default","deployment"],"services":{"crm":{"version":"v1","operations":["createCustomer"]}}}`)
	source, err := EmbedUnifiedAppConfig([]byte(`import {buildUnifiedApp} from "@fused/unified-app";
export default buildUnifiedApp({ input: z.object({config:z.string()}), execute() { throw new Error("must not run"); } });`), metadata)
	// Metadata extraction must not evaluate the deliberately throwing app body.
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseUnifiedAppSource(source, "app.ts")
	if err != nil {
		t.Fatal(err)
	}
	// Inline and legacy documents share the same portable cart and source-bound receipt.
	if parsed.UnifiedApp.Name != "customer" || parsed.UnifiedApp.Source != string(source) || len(parsed.UnifiedApp.RuntimeBuckets) != 2 {
		t.Fatalf("inline config = %#v", parsed)
	}
	// Strings and comments containing default-app examples must not acquire metadata authority.
	examples := "/* export default buildUnifiedApp({config:{}}); */\nconst example = `export const fusedConfig = {};`;\n"
	if _, err := ParseUnifiedAppSource(append([]byte(examples), source...), "app.ts"); err != nil {
		t.Fatal(err)
	}
}

// TestInlineUnifiedAppRejectsCompetingMetadata prevents property ordering and spreads from choosing another deployment scope.
func TestInlineUnifiedAppRejectsCompetingMetadata(t *testing.T) {
	for _, source := range []string{
		`export default buildUnifiedApp({config:{},config:{}});`,
		`export const fusedConfig = {}; export default buildUnifiedApp({config:{}});`,
		`export default buildUnifiedApp({config:{},...options});`,
		`export default buildUnifiedApp({input:z.object({config:{}})});`,
		`export default buildUnifiedApp({config:loadConfig()});`,
	} {
		// Every ambiguous source must fail before any Engine request or local authored-code execution.
		if _, err := ParseUnifiedAppSource([]byte(source), "app.ts"); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
}
