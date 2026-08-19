package configfile_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
	"gopkg.in/yaml.v3"
)

// TestSDKUnifiedOperationsParseCompactAndExpandedBindings covers both public binding forms.
func TestSDKUnifiedOperationsParseCompactAndExpandedBindings(t *testing.T) {
	parsed, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: sdk
name: engineering-sdk
version: 1.0.0
language: typescript
services:
  github: {operations: [createIssue, getIssue]}
  gitlab: {operations: [createIssue]}
unified_operations:
  issues.create:
    description: Create an issue
    input:
      type: object
      additionalProperties: false
      properties:
        title: {type: string}
    bindings:
      github:
        operation: createIssue
        input:
          title: ${input.title}
          labels: [bug, "${input.label?}"]
          draft: false
          attempts: 2
          note: null
      gitlab: createIssue
    output:
      schema:
        type: object
        properties:
          id: {type: string}
      mapping:
        id: ${response.github.id ?? response.gitlab.iid}
        provider: ${target}
  issues.get:
    input: {type: object}
    bindings:
      github:
        operation: getIssue
        output:
          schema: {type: object}
          mapping: {id: "${response.github.id}"}
`), "sdk.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	create := parsed.SDK.UnifiedOperations["issues.create"]
	if got := create.Bindings["gitlab"].Operation; got != "createIssue" {
		t.Fatalf("compact binding operation = %q", got)
	}
	input := create.Bindings["github"].Input
	if got, want := input["draft"].Raw, false; !reflect.DeepEqual(got, want) {
		t.Fatalf("boolean DynamicValue = %#v, want %#v", got, want)
	}
	if got, want := fmt.Sprint(input["attempts"].Raw), "2"; got != want {
		t.Fatalf("numeric DynamicValue = %q, want %q", got, want)
	}
	if value, ok := input["note"]; !ok || value.Raw != nil {
		t.Fatalf("null DynamicValue was not preserved: %#v", input)
	}
	if create.Output == nil || parsed.SDK.UnifiedOperations["issues.get"].Bindings["github"].Output == nil {
		t.Fatalf("root and binding output forms were not preserved: %#v", parsed.SDK.UnifiedOperations)
	}
}

// TestSDKUnifiedOperationsJSONAndYAMLRoundTripDynamicValues preserves exact JSON leaves.
func TestSDKUnifiedOperationsJSONAndYAMLRoundTripDynamicValues(t *testing.T) {
	document := `{"apiVersion":"fused/v1","kind":"sdk","name":"engineering-sdk","version":"1.0.0","language":"python","services":{"github":{"operations":["createIssue"]}},"unified_operations":{"issues.create":{"input":{"type":"object"},"bindings":{"github":{"operation":"createIssue","input":{"title":"${input.title}","enabled":true,"count":9007199254740993,"ratio":0.1234567890123456789,"nullable":null,"tags":["one",2]}}}}}}`
	parsed, err := configfile.Parse([]byte(document), "sdk.json")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	jsonValue, err := json.Marshal(parsed.SDK)
	if err != nil {
		t.Fatal(err)
	}
	var jsonRoundTrip map[string]any
	if err := json.Unmarshal(jsonValue, &jsonRoundTrip); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonValue), `"count":9007199254740993`) || !strings.Contains(string(jsonValue), `"ratio":0.1234567890123456789`) {
		t.Fatalf("exact JSON numbers changed during marshal: %s", jsonValue)
	}
	yamlValue, err := yaml.Marshal(parsed.SDK)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := configfile.Parse(yamlValue, "sdk.yaml")
	if err != nil {
		t.Fatalf("reparse marshaled YAML: %v\n%s", err, yamlValue)
	}
	want := parsed.SDK.UnifiedOperations["issues.create"].Bindings["github"].Input
	got := reparsed.SDK.UnifiedOperations["issues.create"].Bindings["github"].Input
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DynamicValues changed across YAML round trip\ngot:  %#v\nwant: %#v", got, want)
	}
}

// TestSDKUnifiedOperationsRejectsNonJSONYAMLValues excludes YAML-only value shapes.
func TestSDKUnifiedOperationsRejectsNonJSONYAMLValues(t *testing.T) {
	tests := map[string]string{
		"alias": `
unified_operations:
  issues.create:
    input: &schema {type: object}
    bindings:
      github:
        operation: createIssue
        input: {copy: *schema}
`,
		"merge": `
unified_operations:
  issues.create:
    input: {type: object}
    bindings:
      github:
        operation: createIssue
        input:
          value:
            <<: {enabled: true}
`,
		"timestamp": `
unified_operations:
  issues.create:
    input: {example: 2026-08-18T12:00:00Z}
    bindings: {github: createIssue}
`,
		"custom tag": `
unified_operations:
  issues.create:
    input: {example: !environment prod}
    bindings: {github: createIssue}
`,
		"non-string key": `
unified_operations:
  issues.create:
    input: {properties: {1: {type: string}}}
    bindings: {github: createIssue}
`,
	}
	for name, fragment := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := configfile.Parse([]byte(unifiedSDKDocument(fragment)), "sdk.yaml")
			if err == nil || !strings.Contains(err.Error(), "DynamicValue") {
				t.Fatalf("expected non-JSON DynamicValue error, got %v", err)
			}
		})
	}
}

// TestSDKUnifiedOperationsRejectsInvalidCompactBindingKinds limits shorthand to strings.
func TestSDKUnifiedOperationsRejectsInvalidCompactBindingKinds(t *testing.T) {
	for name, binding := range map[string]string{
		"number":   "42",
		"boolean":  "true",
		"sequence": "[createIssue]",
	} {
		t.Run(name, func(t *testing.T) {
			fragment := "unified_operations:\n  issues.create:\n    input: {}\n    bindings:\n      github: " + binding + "\n"
			_, err := configfile.Parse([]byte(unifiedSDKDocument(fragment)), "sdk.yaml")
			if err == nil || !strings.Contains(err.Error(), "binding") {
				t.Fatalf("expected compact binding type error, got %v", err)
			}
		})
	}
}

// TestSDKUnifiedOperationsPreservesCompactBindingWhenMarshaled keeps edits concise.
func TestSDKUnifiedOperationsPreservesCompactBindingWhenMarshaled(t *testing.T) {
	parsed := mustParseUnifiedSDK(t, `
unified_operations:
  issues.create:
    input: {type: object}
    bindings:
      github: createIssue
`)
	encoded, err := yaml.Marshal(parsed.SDK)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "github: createIssue") {
		t.Fatalf("compact binding was expanded during marshal:\n%s", encoded)
	}
}

// TestSDKUnifiedOperationsSupportsAliasedBindingsForOneService proves lookup
// steps can share one provider while independent services remain parallel roots.
func TestSDKUnifiedOperationsSupportsAliasedBindingsForOneService(t *testing.T) {
	document := `
apiVersion: fused/v1
kind: sdk
name: jira-sdk
version: 1.0.0
language: typescript
services:
  jira:
    operations: [listProjects, listCreateIssueTypes, createIssue]
  nimble:
    operations: [search]
unified_operations:
  issues.create:
    input: {type: object}
    bindings:
      findProjects:
        service: jira
        operation: listProjects
      findIssueTypes:
        service: jira
        operation: listCreateIssueTypes
        depends_on: [findProjects]
      research:
        service: nimble
        operation: search
      createTicket:
        service: jira
        operation: createIssue
        depends_on: [findProjects, findIssueTypes, research]
`
	parsed, err := configfile.Parse([]byte(document), "sdk.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	bindings := parsed.SDK.UnifiedOperations["issues.create"].Bindings
	if got := bindings["findProjects"].Service; got != "jira" {
		t.Fatalf("findProjects service = %q, want jira", got)
	}
	assertUnifiedDependencies(t, bindings, map[string][]string{
		// Both empty dependency sets keep provider research and project lookup runnable together.
		"findProjects": nil,
		"research":     nil,
		// Issue-type discovery needs the selected project, but it does not wait for research.
		"findIssueTypes": {"findProjects"},
		// The mutating step names every response it consumes so scheduling and dataflow agree.
		"createTicket": {"findProjects", "findIssueTypes", "research"},
	})

	yamlValue, err := yaml.Marshal(parsed.SDK)
	if err != nil {
		t.Fatal(err)
	}
	yamlRoundTrip, err := configfile.Parse(yamlValue, "sdk.yaml")
	if err != nil {
		t.Fatalf("reparse marshaled YAML: %v\n%s", err, yamlValue)
	}
	if got := yamlRoundTrip.SDK.UnifiedOperations["issues.create"].Bindings["createTicket"].Service; got != "jira" {
		t.Fatalf("YAML round-trip service = %q, want jira", got)
	}

	jsonValue, err := json.Marshal(parsed.SDK)
	if err != nil {
		t.Fatal(err)
	}
	jsonRoundTrip, err := configfile.Parse(jsonValue, "sdk.json")
	if err != nil {
		t.Fatalf("reparse marshaled JSON: %v\n%s", err, jsonValue)
	}
	if got := jsonRoundTrip.SDK.UnifiedOperations["issues.create"].Bindings["findIssueTypes"].Service; got != "jira" {
		t.Fatalf("JSON round-trip service = %q, want jira", got)
	}
}

// assertUnifiedDependencies compares the complete graph so a missing edge cannot
// silently serialize work that is intended to run concurrently.
func assertUnifiedDependencies(t *testing.T, bindings map[string]configfile.UnifiedOperationBinding, expected map[string][]string) {
	t.Helper()
	for name, want := range expected {
		if got := bindings[name].DependsOn; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s dependencies = %#v, want %#v", name, got, want)
		}
	}
}

// TestSDKUnifiedOperationsPreservesDependenciesAndRollback verifies the
// expanded workflow contract and rollback DynamicValue survive CLI rewrites.
func TestSDKUnifiedOperationsPreservesDependenciesAndRollback(t *testing.T) {
	parsed, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: sdk
name: provisioning-sdk
version: 1.0.0
language: python
services:
  stripe: {operations: [createCustomer, deleteCustomer]}
  jira: {operations: [createProject, deleteProject]}
  github: {operations: [createRepository]}
unified_operations:
  account.provision:
    input: {type: object}
    bindings:
      stripe:
        operation: createCustomer
        rollback:
          operation: deleteCustomer
          input: {customerId: "${response.stripe.id}"}
      jira:
        operation: createProject
        depends_on: [stripe]
        input: {customerId: "${response.stripe.id}"}
        rollback:
          operation: deleteProject
          input: {projectId: "${response.jira.id}"}
      github:
        operation: createRepository
        depends_on: [jira, stripe]
`), "sdk.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	bindings := parsed.SDK.UnifiedOperations["account.provision"].Bindings
	if got, want := bindings["github"].DependsOn, []string{"jira", "stripe"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("depends_on = %#v, want %#v", got, want)
	}
	stripeRollback := bindings["stripe"].Rollback
	if stripeRollback == nil || stripeRollback.Operation != "deleteCustomer" {
		t.Fatalf("stripe rollback = %#v", stripeRollback)
	}
	if got := stripeRollback.Input["customerId"].Raw; got != "${response.stripe.id}" {
		t.Fatalf("rollback DynamicValue = %#v", got)
	}

	encoded, err := yaml.Marshal(parsed.SDK)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := configfile.Parse(encoded, "sdk.yaml")
	if err != nil {
		t.Fatalf("reparse marshaled YAML: %v\n%s", err, encoded)
	}
	if got := reparsed.SDK.UnifiedOperations["account.provision"].Bindings["jira"].Rollback; !reflect.DeepEqual(got, bindings["jira"].Rollback) {
		t.Fatalf("rollback changed across YAML round trip: %#v", got)
	}
}

// TestSDKUnifiedOperationsRejectsInvalidDependencyGraphs covers exact direct
// targets, duplicate edges, self-dependencies, and cycles.
func TestSDKUnifiedOperationsRejectsInvalidDependencyGraphs(t *testing.T) {
	tests := map[string]struct {
		bindings string
		want     string
	}{
		"unknown target":       {"github: {operation: createIssue, depends_on: [jira]}", "must match another binding key"},
		"self dependency":      {"github: {operation: createIssue, depends_on: [github]}", "cannot depend on itself"},
		"duplicate dependency": {"github: {operation: createIssue, depends_on: [gitlab, gitlab]}\n      gitlab: createIssue", "repeats depends_on target"},
		"cycle":                {"github: {operation: createIssue, depends_on: [gitlab]}\n      gitlab: {operation: createIssue, depends_on: [github]}", "contains a depends_on cycle"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			document := unifiedSDKWithGitLab("unified_operations:\n  issues.create:\n    input: {}\n    bindings:\n      " + test.bindings + "\n")
			assertUnifiedConfigError(t, document, test.want)
		})
	}
}

// TestSDKUnifiedOperationsRejectsUnavailableResponseDataflow ensures response
// mappings cannot bypass the direct dependency edges that schedule them.
func TestSDKUnifiedOperationsRejectsUnavailableResponseDataflow(t *testing.T) {
	tests := map[string]struct {
		bindings string
		want     string
	}{
		"forward sibling": {
			"github: {operation: createIssue, input: {id: \"${response.gitlab.id}\"}}\n      gitlab: createIssue",
			"response target \"gitlab\" is not available",
		},
		"rollback sibling": {
			"github: {operation: createIssue, rollback: {operation: createIssue, input: {id: \"${response.gitlab.id}\"}}}\n      gitlab: createIssue",
			"binding \"github\" rollback input",
		},
		"unknown response": {
			"github: {operation: createIssue, input: {id: \"${response.jira.id}\"}}\n      gitlab: createIssue",
			"response reference must name a binding target",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			document := unifiedSDKWithGitLab("unified_operations:\n  issues.create:\n    input: {}\n    bindings:\n      " + test.bindings + "\n")
			assertUnifiedConfigError(t, document, test.want)
		})
	}
}

// TestSDKUnifiedOperationsRejectsTransitiveResponseDataflow keeps declared
// scheduling and response access direct rather than recursively inherited.
func TestSDKUnifiedOperationsRejectsTransitiveResponseDataflow(t *testing.T) {
	document := `
apiVersion: fused/v1
kind: sdk
name: provisioning-sdk
version: 1.0.0
language: typescript
services:
  stripe: {operations: [createCustomer]}
  jira: {operations: [createProject]}
  github: {operations: [createRepository]}
unified_operations:
  account.provision:
    input: {type: object}
    bindings:
      stripe: createCustomer
      jira: {operation: createProject, depends_on: [stripe]}
      github:
        operation: createRepository
        depends_on: [jira]
        input: {customerId: "${response.stripe.id}"}
`
	assertUnifiedConfigError(t, document, "response target \"stripe\" is not available")
}

// TestSDKUnifiedOperationsValidatesRollbackShapeAndSelection keeps rollback
// execution inside the binding's immutable selected operation surface.
func TestSDKUnifiedOperationsValidatesRollbackShapeAndSelection(t *testing.T) {
	tests := map[string]struct {
		rollback string
		want     string
	}{
		"mapping required":   {"deleteIssue", "rollback must be a mapping"},
		"operation required": {"{input: {id: \"${response.github.id}\"}}", "rollback requires an exact operationId"},
		"selected operation": {"{operation: deleteIssue}", "rollback operationId \"deleteIssue\" is not selected"},
		"service override":   {"{operation: createIssue, service: gitlab}", "rollback contains unknown field \"service\""},
		"unknown field":      {"{operation: createIssue, retry: true}", "rollback contains unknown field \"retry\""},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fragment := "unified_operations:\n  issues.create:\n    input: {}\n    bindings:\n      github:\n        operation: createIssue\n        rollback: " + test.rollback + "\n"
			assertUnifiedConfigError(t, unifiedSDKDocument(fragment), test.want)
		})
	}
}

// TestSDKUnifiedOperationsRejectsInvalidRollbackDynamicValue applies the same
// portable expression rules to compensation inputs as forward inputs.
func TestSDKUnifiedOperationsRejectsInvalidRollbackDynamicValue(t *testing.T) {
	document := strings.Replace(unifiedSDKDocument(`
unified_operations:
  issues.create:
    input: {}
    bindings:
      github:
        operation: createIssue
        rollback:
          operation: deleteIssue
          input: {issueId: "delete-${response.github.id}"}
`), "operations: [createIssue]", "operations: [createIssue, deleteIssue]", 1)
	assertUnifiedConfigError(t, document, "must occupy the complete scalar")
}

// TestSDKUnifiedOperationsAllowsUnusedRollback confirms compensation remains
// declarative and is not required to have a downstream dependent.
func TestSDKUnifiedOperationsAllowsUnusedRollback(t *testing.T) {
	document := strings.Replace(unifiedSDKDocument(`
unified_operations:
  issues.create:
    input: {}
    bindings:
      github:
        operation: createIssue
        rollback: {operation: deleteIssue}
`), "operations: [createIssue]", "operations: [createIssue, deleteIssue]", 1)
	if _, err := configfile.Parse([]byte(document), "sdk.yaml"); err != nil {
		t.Fatalf("unused rollback was rejected: %v", err)
	}
}

// TestSDKUnifiedOperationsRejectsRootAndBindingOutputs enforces one output model.
func TestSDKUnifiedOperationsRejectsRootAndBindingOutputs(t *testing.T) {
	_, err := configfile.Parse([]byte(unifiedSDKDocument(`
unified_operations:
  issues.create:
    input: {type: object}
    bindings:
      github:
        operation: createIssue
        output:
          schema: {type: object}
          mapping: {id: "${response.github.id}"}
    output:
      schema: {type: object}
      mapping: {id: "${response.github.id}"}
`)), "sdk.yaml")
	if err == nil || !strings.Contains(err.Error(), "cannot combine root output") {
		t.Fatalf("expected mutually exclusive output error, got %v", err)
	}
}

// TestSDKUnifiedOperationsRejectsUnknownBindingAndOutputFields checks strict nested decoding.
func TestSDKUnifiedOperationsRejectsUnknownBindingAndOutputFields(t *testing.T) {
	for name, fragment := range map[string]string{
		"binding": `
unified_operations:
  issues.create:
    input: {type: object}
    bindings:
      github: {operation: createIssue, fallback: gitlab}
`,
		"output": `
unified_operations:
  issues.create:
    input: {type: object}
    bindings: {github: createIssue}
    output: {schema: {type: object}, mapping: {}, transform: script}
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := configfile.Parse([]byte(unifiedSDKDocument(fragment)), "sdk.yaml")
			if err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("expected closed-object error, got %v", err)
			}
		})
	}
}

// TestSDKUnifiedOperationsEnforcesServiceKeyAndOperationIDSemantics checks exact selection.
func TestSDKUnifiedOperationsEnforcesServiceKeyAndOperationIDSemantics(t *testing.T) {
	tests := map[string]struct {
		fragment string
		want     string
	}{
		"default service key": {`
unified_operations:
  issues.create:
    input: {type: object}
    bindings: {gitlab: createIssue}
`, `service "gitlab" must match a configured service key`},
		"named service key": {`
unified_operations:
  issues.create:
    input: {type: object}
    bindings:
      createTicket: {service: gitlab, operation: createIssue}
`, `service "gitlab" must match a configured service key`},
		"selected operationId": {`
unified_operations:
  issues.create:
    input: {type: object}
    bindings: {github: deleteIssue}
`, "operationId \"deleteIssue\" is not selected"},
		"qualified operationId": {`
unified_operations:
  issues.create:
    input: {type: object}
    bindings: {github: github.createIssue}
`, "operationId \"github.createIssue\" is not selected"},
		"named service selected operationId": {`
unified_operations:
  issues.create:
    input: {type: object}
    bindings:
      createTicket: {service: github, operation: deleteIssue}
`, "operationId \"deleteIssue\" is not selected"},
		"named service rollback operationId": {`
unified_operations:
  issues.create:
    input: {type: object}
    bindings:
      createTicket:
        service: github
        operation: createIssue
        rollback: {operation: deleteIssue}
`, "rollback operationId \"deleteIssue\" is not selected"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := configfile.Parse([]byte(unifiedSDKDocument(test.fragment)), "sdk.yaml")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

// TestSDKUnifiedOperationsAllowsSelectedOperationWithProviderQualifiedKey preserves opaque slugs.
func TestSDKUnifiedOperationsAllowsSelectedOperationWithProviderQualifiedKey(t *testing.T) {
	document := strings.Replace(unifiedSDKDocument(`
unified_operations:
  issues.create:
    input: {type: object}
    bindings: {"@acme/github": createIssue}
`), "github:\n    operations", "\"@acme/github\":\n    operations", 1)
	if _, err := configfile.Parse([]byte(document), "sdk.yaml"); err != nil {
		t.Fatalf("provider-qualified service slug was rejected: %v", err)
	}
}

// TestSDKUnifiedOperationsAllowsPunctuatedOperationIDAndSelectAll covers opaque operation IDs.
func TestSDKUnifiedOperationsAllowsPunctuatedOperationIDAndSelectAll(t *testing.T) {
	punctuated := strings.Replace(unifiedSDKDocument(`
unified_operations:
  metadata.get:
    input: {type: object}
    bindings: {github: meta/get}
`), "operations: [createIssue]", "operations: [meta/get]", 1)
	if _, err := configfile.Parse([]byte(punctuated), "sdk.yaml"); err != nil {
		t.Fatalf("punctuated exact operationId was rejected: %v", err)
	}

	selectAll := strings.Replace(unifiedSDKDocument(`
unified_operations:
  metadata.get:
    input: {type: object}
    bindings: {github: not-locally-enumerated}
`), "operations: [createIssue]", "operations: []\n    select_all: true", 1)
	if _, err := configfile.Parse([]byte(selectAll), "sdk.yaml"); err != nil {
		t.Fatalf("select_all binding was rejected before Engine resolution: %v", err)
	}
}

// TestSDKUnifiedOperationsRejectsInvalidShapeAndUnsupportedAppKinds covers top-level constraints.
func TestSDKUnifiedOperationsRejectsInvalidShapeAndUnsupportedAppKinds(t *testing.T) {
	tests := map[string]struct {
		document string
		want     string
	}{
		"empty set":                  {unifiedSDKDocument("unified_operations: {}\n"), "requires at least one operation"},
		"invalid name":               {unifiedSDKDocument("unified_operations:\n  issues-create:\n    input: {}\n    bindings: {github: createIssue}\n"), "dot-separated identifier"},
		"namespace clash":            {unifiedSDKDocument("unified_operations:\n  issues:\n    input: {}\n    bindings: {github: createIssue}\n  issues.create:\n    input: {}\n    bindings: {github: createIssue}\n"), "collide as generated namespace"},
		"normalized type clash":      {unifiedSDKDocument("unified_operations:\n  foo_bar.one:\n    input: {}\n    bindings: {github: createIssue}\n  foo.bar_one:\n    input: {}\n    bindings: {github: createIssue}\n"), "collide as generated type names"},
		"normalized namespace clash": {unifiedSDKDocument("unified_operations:\n  foo_bar.x:\n    input: {}\n    bindings: {github: createIssue}\n  foo.bar.y:\n    input: {}\n    bindings: {github: createIssue}\n"), "collide after code generation"},
		"Python keyword":             {strings.Replace(unifiedSDKDocument("unified_operations:\n  issues.class:\n    input: {}\n    bindings: {github: createIssue}\n"), "language: typescript", "language: python", 1), "Python keyword"},
		"missing input":              {unifiedSDKDocument("unified_operations:\n  issues.create:\n    bindings: {github: createIssue}\n"), "requires input schema"},
		"missing binding":            {unifiedSDKDocument("unified_operations:\n  issues.create:\n    input: {}\n    bindings: {}\n"), "requires at least one binding"},
		"go SDK":                     {strings.Replace(unifiedSDKDocument("unified_operations:\n  issues.create:\n    input: {}\n    bindings: {github: createIssue}\n"), "language: typescript", "language: go", 1), "require language typescript or python"},
		"MCP":                        {strings.Replace(strings.Replace(unifiedSDKDocument("unified_operations:\n  issues.create:\n    input: {}\n    bindings: {github: createIssue}\n"), "kind: sdk", "kind: mcp", 1), "language: typescript\n", "", 1), "mcp config must not set unified_operations"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := configfile.Parse([]byte(test.document), "config.yaml")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

// TestSDKUnifiedOperationsEnforcesStructuralBounds covers source resource limits.
func TestSDKUnifiedOperationsEnforcesStructuralBounds(t *testing.T) {
	t.Run("operation count", func(t *testing.T) {
		var operations strings.Builder
		operations.WriteString("unified_operations:\n")
		for i := 0; i <= configfile.MaxUnifiedOperations; i++ {
			fmt.Fprintf(&operations, "  operation%d:\n    input: {}\n    bindings: {github: createIssue}\n", i)
		}
		assertUnifiedConfigError(t, unifiedSDKDocument(operations.String()), "exceeds 64 operations")
	})

	t.Run("binding count", func(t *testing.T) {
		var document strings.Builder
		document.WriteString("apiVersion: fused/v1\nkind: sdk\nname: bounded\nversion: 1.0.0\nlanguage: python\nservices:\n")
		for i := 0; i <= configfile.MaxUnifiedBindings; i++ {
			fmt.Fprintf(&document, "  service%d: {operations: [run]}\n", i)
		}
		document.WriteString("unified_operations:\n  bounded.run:\n    input: {}\n    bindings:\n")
		for i := 0; i <= configfile.MaxUnifiedBindings; i++ {
			fmt.Fprintf(&document, "      service%d: run\n", i)
		}
		assertUnifiedConfigError(t, document.String(), "exceeds 16 bindings")
	})

	t.Run("expression interpolation", func(t *testing.T) {
		assertUnifiedConfigError(t, unifiedSDKDocument(`
unified_operations:
  issues.create:
    input: {}
    bindings:
      github:
        operation: createIssue
        input: {title: "prefix ${input.title}"}
`), "must occupy the complete scalar")
	})

	t.Run("expression count", func(t *testing.T) {
		var fragment strings.Builder
		fragment.WriteString("unified_operations:\n  issues.create:\n    input: {}\n    bindings:\n      github:\n        operation: createIssue\n        input:\n")
		for i := 0; i <= configfile.MaxUnifiedExpressions; i++ {
			fmt.Fprintf(&fragment, "          value%d: \"${input.value}\"\n", i)
		}
		assertUnifiedConfigError(t, unifiedSDKDocument(fragment.String()), "exceed 512 expressions")
	})

	t.Run("value depth", func(t *testing.T) {
		depth := configfile.MaxUnifiedValueDepth + 2
		value := strings.Repeat("[", depth) + "true" + strings.Repeat("]", depth)
		fragment := "unified_operations:\n  issues.create:\n    input:\n      example: " + value + "\n    bindings: {github: createIssue}\n"
		assertUnifiedConfigError(t, unifiedSDKDocument(fragment), "maximum depth 32")
	})

	t.Run("encoded bytes", func(t *testing.T) {
		fragment := "unified_operations:\n  issues.create:\n    input:\n      example: " + strings.Repeat("x", configfile.MaxUnifiedEncodedBytes) + "\n    bindings: {github: createIssue}\n"
		assertUnifiedConfigError(t, unifiedSDKDocument(fragment), "encoded bytes")
	})
}

// mustParseUnifiedSDK parses a valid fragment or stops its test immediately.
func mustParseUnifiedSDK(t *testing.T, fragment string) *configfile.ParsedConfig {
	t.Helper()
	parsed, err := configfile.Parse([]byte(unifiedSDKDocument(fragment)), "sdk.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return parsed
}

// assertUnifiedConfigError checks that a document fails with an actionable reason.
func assertUnifiedConfigError(t *testing.T, document, want string) {
	t.Helper()
	_, err := configfile.Parse([]byte(document), "sdk.yaml")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}

// unifiedSDKDocument wraps a fragment in the smallest valid TypeScript SDK.
func unifiedSDKDocument(fragment string) string {
	return `apiVersion: fused/v1
kind: sdk
name: engineering-sdk
version: 1.0.0
language: typescript
services:
  github:
    operations: [createIssue]
` + fragment
}

// unifiedSDKWithGitLab adds a second selected service for dependency graph tests.
func unifiedSDKWithGitLab(fragment string) string {
	return strings.Replace(unifiedSDKDocument(fragment), "  github:\n    operations: [createIssue]\n", "  github:\n    operations: [createIssue]\n  gitlab:\n    operations: [createIssue]\n", 1)
}
