package configfile

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExecutionBundleDigestReachesPlanPayload proves a distinct App kind retains compiler identity.
func TestExecutionBundleDigestReachesPlanPayload(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	document := "apiVersion: fused/v1\nkind: execution\nname: capability-app\nversion: 1.0.0\nlanguage: typescript\ngenerate: false\nbucket: default\nbundle_digest: " + digest + "\nservices:\n  crm:\n    version: v1\n    operations: [customers.create]\n"
	parsed, err := Parse([]byte(document), "app.yaml")
	// The authored digest must enter the same typed config that the plan command marshals.
	if err != nil {
		t.Fatalf("parse digest: %v", err)
	}
	// The typed Execution shape must carry the value before plan serialization.
	if parsed.Kind != KindExecution || parsed.Execution.BundleDigest != digest {
		t.Fatalf("parse digest = %#v, want %q", parsed, digest)
	}
	raw, err := json.Marshal(parsed.Execution)
	if err != nil || !strings.Contains(string(raw), `"bundle_digest":"`+digest+`"`) {
		t.Fatalf("plan JSON omitted digest: %s, error = %v", raw, err)
	}
	changed, err := Parse([]byte(strings.Replace(document, digest, "sha256:"+strings.Repeat("b", 64), 1)), "app.yaml")
	// A compiler output change must also change the config source hash and require a new immutable version.
	if err != nil {
		t.Fatalf("parse changed compiler digest: %v", err)
	}
	// Source identity includes the authored digest, so changed compiled bytes cannot be a no-op.
	if changed.SourceHash == parsed.SourceHash {
		t.Fatalf("changed compiler digest kept source hash: %q", changed.SourceHash)
	}
}

// TestExecutionBundleDigestRejectsInvalidInputs denies malformed or unsupported compiler identities locally.
func TestExecutionBundleDigestRejectsInvalidInputs(t *testing.T) {
	valid := "sha256:" + strings.Repeat("a", 64)
	base := "apiVersion: fused/v1\nkind: execution\nname: capability-app\nversion: 1.0.0\nlanguage: typescript\ngenerate: false\nbucket: default\nbundle_digest: " + valid + "\nservices:\n  crm:\n    version: v1\n    operations: [customers.create]\n"
	for _, testCase := range []struct{ name, document string }{
		{name: "uppercase", document: strings.Replace(base, valid, strings.ToUpper(valid), 1)},
		{name: "wrong language", document: strings.Replace(base, "language: typescript", "language: python", 1)},
		{name: "implicit package", document: strings.Replace(base, "generate: false\n", "", 1)},
		{name: "explicit package", document: strings.Replace(base, "generate: false", "generate: true", 1)},
		{name: "competing graph", document: base + "unified_operations:\n  legacy.execute: {}\n"},
		{name: "SDK kind", document: strings.Replace(base, "kind: execution", "kind: sdk", 1)},
		{name: "raw execute collision", document: strings.Replace(base, "customers.create", "execute", 1)},
		{name: "unbounded selection", document: strings.Replace(base, "operations: [customers.create]", "select_all: true", 1)},
		{name: "too many operations", document: strings.Replace(base, "operations: [customers.create]", "operations: ["+strings.Repeat("greet,", 64)+"greet]", 1)},
	} {
		// Every unsupported input must fail before CLI writes a plan or sends it to Engine.
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := Parse([]byte(testCase.document), "app.yaml"); err == nil {
				t.Fatalf("invalid bundle digest accepted or misreported: %v", err)
			}
		})
	}
}

// TestExecutionBundleDigestRequiresServiceScope keeps authored code within reviewed provider authority.
func TestExecutionBundleDigestRequiresServiceScope(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	document := "apiVersion: fused/v1\nkind: execution\nname: greeting\nversion: 1.0.0\nlanguage: typescript\ngenerate: false\nbucket: default\nbundle_digest: " + digest + "\n"
	// A compiler digest cannot supply provider authority by itself.
	if _, err := Parse([]byte(document), "greeting.yaml"); err == nil || !strings.Contains(err.Error(), "1 to 64 selected operations") {
		t.Fatalf("service-free Execution App error = %v", err)
	}
}

// TestExecutionSourceReachesPlanAndHash verifies Engine compilation receives source bound to desired-state identity.
func TestExecutionSourceReachesPlanAndHash(t *testing.T) {
	base := "apiVersion: fused/v1\nkind: execution\nname: capability-app\nversion: 1.0.0\nlanguage: typescript\ngenerate: false\nbucket: default\nsource: |\n  export default buildExecutionApp({});\nservices:\n  crm:\n    version: v1\n    operations: [customers.create]\n"
	parsed, err := Parse([]byte(base), "app.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(parsed.Execution)
	// The Engine plan receives source in its cart and never a CLI-supplied digest.
	if err != nil || !strings.Contains(string(raw), `"source":"export default buildExecutionApp({});\n"`) || strings.Contains(string(raw), "bundle_digest") {
		t.Fatalf("source cart = %s, %v", raw, err)
	}
	changed, err := Parse([]byte(strings.Replace(base, "buildExecutionApp({})", "buildExecutionApp({input: 1})", 1)), "app.yaml")
	if err != nil || parsed.SourceHash == changed.SourceHash {
		t.Fatalf("source hash did not bind code: %v", err)
	}
}

// TestExecutionSourceRejectsCompetingAuthority prevents source and a caller digest from selecting different bundles.
func TestExecutionSourceRejectsCompetingAuthority(t *testing.T) {
	base := "apiVersion: fused/v1\nkind: execution\nname: capability-app\nversion: 1.0.0\nlanguage: typescript\ngenerate: false\nbucket: default\nsource: 'export default buildExecutionApp({})'\nservices:\n  crm:\n    version: v1\n    operations: [customers.create]\n"
	for _, document := range []string{
		strings.Replace(base, "source: 'export default buildExecutionApp({})'", "source: '   '", 1),
		base + "bundle_digest: sha256:" + strings.Repeat("a", 64) + "\n",
		strings.Replace(base, "kind: execution", "kind: sdk", 1),
		strings.Replace(base, "kind: execution", "kind: mcp", 1),
	} {
		// Malformed source authority must fail before an Engine plan is requested.
		if _, err := Parse([]byte(document), "app.yaml"); err == nil {
			t.Fatalf("accepted invalid source document: %s", document)
		}
	}
}
