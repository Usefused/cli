package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// TestScaffoldCommandCreatesRunnableConfigs proves service-bearing app init emits the bucket required by Engine plan.
func TestScaffoldCommandCreatesRunnableConfigs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		kind configfile.ConfigKind
	}{
		{
			name: "workspace",
			args: []string{"workspace", "--service", "jira=1001.0.0", "--json"},
			kind: configfile.KindWorkspace,
		},
		{
			name: "sdk",
			args: []string{"sdk", "google-workspace", "--service", "@google/drive=v3", "--operation", "@google/drive=listFiles", "--json"},
			kind: configfile.KindSDK,
		},
		{
			name: "mcp",
			args: []string{"mcp", "support-agent", "--description", "Find and manage customer support issues in Jira.", "--service", "jira=1001.0.0", "--select-all", "jira", "--json"},
			kind: configfile.KindMCP,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name+".yaml")
			output := runScaffoldCommandForTest(t, path, test.args...)
			parsed, err := configfile.ParseFile(path)
			if err != nil {
				t.Fatalf("generated config should validate: %v", err)
			}
			if parsed.Kind != test.kind {
				t.Fatalf("kind = %s, want %s", parsed.Kind, test.kind)
			}
			// Workspace owns bucket declarations separately, while SDK and MCP must bind an existing candidate.
			if test.kind == configfile.KindSDK && parsed.SDK.Bucket != "default" {
				t.Fatalf("SDK bucket = %q, want default", parsed.SDK.Bucket)
			}
			if test.kind == configfile.KindMCP && parsed.MCP.Bucket != "default" {
				t.Fatalf("MCP bucket = %q, want default", parsed.MCP.Bucket)
			}
			// MCP scaffolding must preserve the calling LLM's server-level prose exactly.
			if test.kind == configfile.KindMCP && parsed.MCP.Description != "Find and manage customer support issues in Jira." {
				t.Fatalf("MCP description = %q", parsed.MCP.Description)
			}
			var result scaffoldResult
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("decode result: %v\n%s", err, output)
			}
			if result.Action != "created" || !result.Changed || result.Path != path {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

// TestSelectScaffoldBucketPrefersNamedDefaultAndFallsBack proves init never creates a bucket while choosing a stable visible candidate.
func TestSelectScaffoldBucketPrefersNamedDefaultAndFallsBack(t *testing.T) {
	buckets := []api.BucketSummaryResponse{{Name: "production"}, {Name: "default"}}
	selected, err := selectScaffoldBucket(buckets)
	if err != nil || selected != "default" {
		t.Fatalf("default selection = %q, %v", selected, err)
	}
	selected, err = selectScaffoldBucket(buckets[:1])
	if err != nil || selected != "production" {
		t.Fatalf("fallback selection = %q, %v", selected, err)
	}
	// An empty visible set must fail rather than inventing or creating workspace state.
	if _, err := selectScaffoldBucket(nil); err == nil {
		t.Fatal("empty bucket selection succeeded")
	}
}

// TestEnsureScaffoldBucketPreservesExistingSelection proves automatic discovery cannot retarget authored config.
func TestEnsureScaffoldBucketPreservesExistingSelection(t *testing.T) {
	config := &configfile.AppConfig{Bucket: "production", Services: map[string]configfile.AppService{"jira": {Version: "3", SelectAll: true}}}
	resolverCalls := 0
	changed, err := ensureScaffoldBucket(config, func() (string, error) {
		resolverCalls++
		return "default", nil
	})
	// Existing selection is authoritative, so discovery must not even run.
	if err != nil || changed || resolverCalls != 0 || config.Bucket != "production" {
		t.Fatalf("existing bucket changed: config=%#v changed=%v calls=%d err=%v", config, changed, resolverCalls, err)
	}
}

// TestInitCommandsExposeOneRootCreationSurface proves compatibility scaffolds remain addressable but hidden behind unified init.
func TestInitCommandsExposeOneRootCreationSurface(t *testing.T) {
	for _, parent := range []*cobra.Command{workspaceCmd, sdkCmd, mcpCmd} {
		command, _, err := parent.Find([]string{"init"})
		// Workspace init remains public configuration authoring, while app-specific creators are compatibility-only.
		if err != nil || command.Name() != "init" {
			t.Fatalf("%s init is not registered: %v", parent.Name(), err)
		}
	}
	foundUnified := false
	for _, command := range RootCmd.Commands() {
		// Exactly one root init is now the advertised app creation path; the removed scaffold alias must not return.
		if command.Name() == "init" {
			foundUnified = true
		}
		if command.HasAlias("scaffold") {
			t.Fatalf("legacy top-level scaffold alias remains registered as %s", command.Name())
		}
	}
	if !foundUnified {
		t.Fatal("unified top-level init is not registered")
	}
}

func TestScaffoldCommandCreatesEditableEmptySkeletons(t *testing.T) {
	tests := []struct {
		name string
		args []string
		kind configfile.ConfigKind
	}{
		{name: "workspace", args: []string{"workspace"}, kind: configfile.KindWorkspace},
		{name: "sdk", args: []string{"sdk", "empty-sdk"}, kind: configfile.KindSDK},
		{name: "mcp", args: []string{"mcp", "empty-mcp"}, kind: configfile.KindMCP},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name+".yaml")
			runScaffoldCommandForTest(t, path, test.args...)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var base configfile.BaseConfig
			if err := yaml.Unmarshal(data, &base); err != nil {
				t.Fatalf("decode skeleton: %v", err)
			}
			if base.APIVersion != configfile.APIVersionV1 || base.Kind != test.kind {
				t.Fatalf("unexpected skeleton base: %+v", base)
			}
			if !bytes.Contains(data, []byte("services: {}")) {
				t.Fatalf("expected an explicit empty services map:\n%s", data)
			}
			if bytes.Contains(data, []byte("bucket:")) {
				t.Fatalf("skeleton should not invent a bucket:\n%s", data)
			}
		})
	}
}

func TestScaffoldCommandRejectsKindSpecificFlags(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"workspace", "--operation", "jira=listProjects"}, want: "unknown flag: --operation"},
		{args: []string{"mcp", "support", "--language", "python"}, want: "unknown flag: --language"},
	}
	for _, test := range tests {
		err := executeScaffoldCommandForTest(filepath.Join(t.TempDir(), "config.yaml"), test.args...)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("expected %q, got %v", test.want, err)
		}
	}
}

func TestScaffoldCommandExtendsSDKWithoutReplacingSelections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sdk.yaml")
	original := `apiVersion: fused/v1
kind: sdk
name: existing-sdk
version: 1.0.0
language: python
services:
  jira:
    version: 1001.0.0
    operations: [listProjects]
`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	runScaffoldCommandForTest(t, path,
		"sdk", "--extend",
		"--service", "@google/drive=v3",
		"--operation", "@google/drive=listFiles",
		"--operation", "jira=createIssue",
	)

	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("extended config should validate: %v", err)
	}
	assertExtendedSDKSelections(t, parsed.SDK)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

// TestExtendAppScaffoldExplicitVersionBumpRetargetsTheSameFile proves a deliberate successor version preserves and expands one config document.
func TestExtendAppScaffoldExplicitVersionBumpRetargetsTheSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sdk.yaml")
	original := `apiVersion: fused/v1
kind: sdk
name: existing-sdk
version: 1.0.0
language: typescript
bucket: default
services:
  linear:
    version: v1
    operations: [issueGet]
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	request := scaffoldRequest{
		kind: configfile.KindSDK, name: "existing-sdk", path: path, extend: true,
		services:   []scaffoldService{{name: "linear", version: "v1"}},
		operations: []scaffoldOperation{{service: "linear", operation: "issueUpdate"}},
		version:    "1.1.0", versionSet: true,
	}
	result, err := writeScaffold(request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatalf("extend app version in place: %v", err)
	}
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("parse bumped config: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The same private file now describes the successor while retaining the previous operation as its starting scope.
	if result.Action != "extended" || parsed.SDK.Version != "1.1.0" || len(parsed.SDK.Services["linear"].Operations) != 2 || info.Mode().Perm() != 0o600 {
		t.Fatalf("result=%#v version=%q operations=%#v mode=%o", result, parsed.SDK.Version, parsed.SDK.Services["linear"].Operations, info.Mode().Perm())
	}
}

// TestExtendAppScaffoldSemanticFailuresPreserveOriginalBytes proves validation precedes every atomic replacement.
func TestExtendAppScaffoldSemanticFailuresPreserveOriginalBytes(t *testing.T) {
	tests := []struct {
		name    string
		request func(string) scaffoldRequest
	}{
		{
			name: "invalid successor version",
			request: func(path string) scaffoldRequest {
				return scaffoldRequest{
					kind: configfile.KindSDK, name: "existing-sdk", path: path, extend: true,
					services:   []scaffoldService{{name: "linear", version: "v1"}},
					operations: []scaffoldOperation{{service: "linear", operation: "issueUpdate"}},
					version:    "next", versionSet: true,
				}
			},
		},
		{
			name: "service without operation scope",
			request: func(path string) scaffoldRequest {
				return scaffoldRequest{
					kind: configfile.KindSDK, name: "existing-sdk", path: path, extend: true,
					services: []scaffoldService{{name: "github", version: "v1"}},
					version:  "1.1.0", versionSet: true,
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sdk.yaml")
			original := []byte(`apiVersion: fused/v1
kind: sdk
name: existing-sdk
version: 1.0.0
language: typescript
bucket: default
services:
  linear:
    version: v1
    operations: [issueGet]
`)
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := writeScaffold(test.request(path), noOpScaffoldRequirements, defaultTestScaffoldBucket)
			data, readErr := os.ReadFile(path)
			info, statErr := os.Stat(path)
			// Semantic rejection must leave both content and private mode byte-for-byte unchanged.
			if err == nil || readErr != nil || statErr != nil || !bytes.Equal(data, original) || info.Mode().Perm() != 0o600 {
				t.Fatalf("error=%v readErr=%v statErr=%v mode=%o file=%q", err, readErr, statErr, info.Mode().Perm(), data)
			}
		})
	}
}

// assertExtendedSDKSelections keeps additive identity and operation checks
// focused without widening the command orchestration test.
func assertExtendedSDKSelections(t *testing.T, config *configfile.SDKConfig) {
	t.Helper()
	// Identity fields are immutable unless their matching flag was explicitly supplied.
	if config.Name != "existing-sdk" || config.Language != "python" {
		t.Fatalf("identity was replaced: %+v", config)
	}
	// Existing and requested operations must coexist after additive merge.
	if got := config.Services["jira"].Operations; !containsString(got, "listProjects") || !containsString(got, "createIssue") {
		t.Fatalf("jira operations were not merged: %v", got)
	}
	// A newly selected service retains its exact provider-qualified key.
	if got := config.Services["@google/drive"].Operations; len(got) != 1 || got[0] != "listFiles" {
		t.Fatalf("drive selection missing: %v", got)
	}
}

// TestScaffoldCommandAddsSDKServerVariableBindings proves SDK init enriches
// the complete selection through the same shared app scaffold path.
func TestScaffoldCommandAddsSDKServerVariableBindings(t *testing.T) {
	assertScaffoldCommandAddsServerVariableBindings(t, configfile.KindSDK)
}

// TestScaffoldCommandAddsMCPServerVariableBindings proves MCP init receives
// the same Engine-backed routing parity as SDK init.
func TestScaffoldCommandAddsMCPServerVariableBindings(t *testing.T) {
	assertScaffoldCommandAddsServerVariableBindings(t, configfile.KindMCP)
}

// assertScaffoldCommandAddsServerVariableBindings verifies one batched lookup,
// deterministic key derivation, response deduplication, and safe result counts.
func assertScaffoldCommandAddsServerVariableBindings(t *testing.T, kind configfile.ConfigKind) {
	t.Helper()
	requests := 0
	resolver := func(selections []api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
		requests++
		assertSingleScaffoldSelection(t, selections)
		return []api.AppScaffoldRequirement{
			{Service: "send bird", Variable: "region"},
			{Service: "send bird", Variable: "app_id"},
			{Service: "send bird", Variable: "app_id"},
		}, nil
	}
	path := filepath.Join(t.TempDir(), string(kind)+".yaml")
	args := []string{string(kind), "sendbird-app", "--service", "send bird=v3", "--operation", "send bird=listUsers", "--json"}
	// MCP configs require server-level prose while SDK scaffolds have no protocol identity description.
	if kind == configfile.KindMCP {
		args = append(args, "--description", "Manage users through the connected Sendbird service.")
	}
	output := runScaffoldCommandWithResolverForTest(t, path, resolver, args...)
	parsed, err := configfile.ParseFile(path)
	// Generated server-variable injections must remain valid app config.
	if err != nil {
		t.Fatalf("parse generated config: %v", err)
	}
	assertGeneratedScaffoldApp(t, parsed, kind, requests)
	assertScaffoldBindingResult(t, output, 2)
}

// assertSingleScaffoldSelection verifies the resolver sees the complete app
// selection rather than one lookup per operation.
func assertSingleScaffoldSelection(t *testing.T, selections []api.AppScaffoldSelection) {
	t.Helper()
	// Exact selection metadata is required before the Engine can resolve routing targets.
	if len(selections) != 1 || selections[0].Service != "send bird" || selections[0].Version != "v3" || len(selections[0].Operations) != 1 {
		t.Fatalf("selections = %#v", selections)
	}
}

// assertGeneratedScaffoldApp selects the kind-specific parsed view and checks
// stable injection order without duplicating the SDK/MCP test body.
func assertGeneratedScaffoldApp(t *testing.T, parsed *configfile.ParsedConfig, kind configfile.ConfigKind, requests int) {
	t.Helper()
	app := parsed.SDK
	// MCP and SDK share AppConfig but ParseFile exposes the matching typed field.
	if kind == configfile.KindMCP {
		app = parsed.MCP
	}
	injections := app.Services["send bird"].Injections
	// Sorted requirements produce stable injection order and collapse duplicate rows.
	if requests != 1 || len(injections) != 2 || injections[0].Value != "${bucket.env.SENDBIRD_APP_ID}" || injections[1].Value != "${bucket.env.SENDBIRD_REGION}" {
		t.Fatalf("requests=%d injections=%#v", requests, injections)
	}
}

// assertScaffoldBindingResult verifies the public result contains only the
// generated binding cardinality required by callers and telemetry.
func assertScaffoldBindingResult(t *testing.T, output []byte, want int) {
	t.Helper()
	var result scaffoldResult
	if err := json.Unmarshal(output, &result); err != nil || result.GeneratedBindingCount != want {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

// TestScaffoldCommandExtendResolvesMergedConfigAndPreservesUserInjection proves
// enrichment runs after additive merge and never replaces an existing target.
func TestScaffoldCommandExtendResolvesMergedConfigAndPreservesUserInjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sdk.yaml")
	original := `apiVersion: fused/v1
kind: sdk
name: existing
version: 1.0.0
language: typescript
services:
  send bird:
    version: v3
    operations: [listUsers]
    injections:
      - location: " SERVER_VARIABLE "
        name: " app_id "
        value: ${bucket.env.CUSTOM_APP}
        mode: default
`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	resolver := func(selections []api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
		requests++
		assertMergedScaffoldSelections(t, selections)
		return []api.AppScaffoldRequirement{
			{Service: "send bird", Variable: "app_id"},
			{Service: "jira", Variable: "region"},
		}, nil
	}
	output := runScaffoldCommandWithResolverForTest(t, path, resolver,
		"sdk", "existing", "--extend", "--service", "jira=v1", "--operation", "jira=listProjects", "--json",
	)
	parsed, err := configfile.ParseFile(path)
	// The final atomic file must validate after enrichment.
	if err != nil {
		t.Fatalf("parse extended config: %v", err)
	}
	assertPreservedScaffoldInjections(t, parsed.SDK, requests)
	assertScaffoldBindingResult(t, output, 1)
}

// assertMergedScaffoldSelections proves --extend resolves old and new service
// selections together through one sorted Engine request.
func assertMergedScaffoldSelections(t *testing.T, selections []api.AppScaffoldSelection) {
	t.Helper()
	// Both services must be visible after merge and before enrichment.
	if len(selections) != 2 || selections[0].Service != "jira" || selections[1].Service != "send bird" {
		t.Fatalf("merged selections = %#v", selections)
	}
}

// assertPreservedScaffoldInjections checks additive behavior independently
// from command orchestration complexity.
func assertPreservedScaffoldInjections(t *testing.T, config *configfile.SDKConfig, requests int) {
	t.Helper()
	preserved := config.Services["send bird"].Injections
	generated := config.Services["jira"].Injections
	// User spelling and value remain untouched while only the missing service target is added.
	if requests != 1 || len(preserved) != 1 || preserved[0].Value != "${bucket.env.CUSTOM_APP}" || len(generated) != 1 || generated[0].Value != "${bucket.env.JIRA_REGION}" {
		t.Fatalf("requests=%d preserved=%#v generated=%#v", requests, preserved, generated)
	}
}

// TestScaffoldBucketValueKeyUsesProviderSlugTail keeps provider-qualified and
// plain service keys on the same predictable non-secret bucket convention.
func TestScaffoldBucketValueKeyUsesProviderSlugTail(t *testing.T) {
	tests := []struct {
		service  string
		variable string
		want     string
	}{
		{service: "send bird", variable: "app_id", want: "SENDBIRD_APP_ID"},
		{service: "@sendbird/sendbird", variable: "app_id", want: "SENDBIRD_APP_ID"},
		{service: "sendbird", variable: "sendbird_app_id", want: "SENDBIRD_APP_ID"},
	}
	// Each spelling must derive independently rather than relying on shared state.
	for _, test := range tests {
		if got := scaffoldBucketValueKey(test.service, test.variable); got != test.want {
			t.Errorf("key = %q, want %q", got, test.want)
		}
	}
}

// TestEnrichAppScaffoldRejectsNormalizedKeyCollision prevents two distinct
// provider variables from silently reading the same bucket value.
func TestEnrichAppScaffoldRejectsNormalizedKeyCollision(t *testing.T) {
	config := &configfile.AppConfig{Services: map[string]configfile.AppService{
		"sendbird": {Version: "v3", Operations: []string{"listUsers"}},
	}}
	resolver := func([]api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
		return []api.AppScaffoldRequirement{
			{Service: "sendbird", Variable: "app-id"},
			{Service: "sendbird", Variable: "app_id"},
		}, nil
	}
	generated, err := enrichAppScaffold(config, resolver)
	// Collision failure must happen before either ambiguous injection is added.
	if err == nil || !strings.Contains(err.Error(), "colliding generated keys") || generated != 0 || len(config.Services["sendbird"].Injections) != 0 {
		t.Fatalf("generated=%d injections=%#v err=%v", generated, config.Services["sendbird"].Injections, err)
	}
}

func TestScaffoldCommandExtensionIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	runScaffoldCommandForTest(t, path,
		"mcp", "support", "--service", "jira=1001.0.0", "--operation", "jira=listProjects",
	)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	output := runScaffoldCommandForTest(t, path,
		"mcp", "support", "--extend", "--service", "jira=1001.0.0", "--operation", "jira=listProjects", "--json",
	)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("idempotent extension rewrote the file:\n%s", after)
	}
	var result scaffoldResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if result.Action != "unchanged" || result.Changed {
		t.Fatalf("unexpected idempotent result: %+v", result)
	}
}

func TestScaffoldCommandRejectsConflictingExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sdk.yaml")
	runScaffoldCommandForTest(t, path,
		"sdk", "payments", "--service", "stripe=2026-08-01", "--operation", "stripe=createPayment",
	)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = executeScaffoldCommandForTest(path,
		"sdk", "payments", "--extend", "--service", "stripe=2026-09-01",
	)
	if err == nil || !strings.Contains(err.Error(), "already uses version") {
		t.Fatalf("expected version conflict, got %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("conflicting extension changed the file")
	}
}

func TestScaffoldCommandRejectsWrongKindAndMultipleDocuments(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "wrong kind",
			body: "apiVersion: fused/v1\nkind: workspace\nservices: {}\n",
			want: "expected sdk config",
		},
		{
			name: "multiple documents",
			body: "apiVersion: fused/v1\nkind: sdk\nname: one\nversion: 1.0.0\nlanguage: typescript\nservices: {}\n---\nkind: sdk\n",
			want: "exactly one YAML document",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.body), 0644); err != nil {
				t.Fatal(err)
			}
			err := executeScaffoldCommandForTest(path, "sdk", "--extend", "--service", "jira=v1")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestScaffoldTargetPathUsesKindDirectory(t *testing.T) {
	tests := []struct {
		kind configfile.ConfigKind
		name string
		want string
	}{
		{kind: configfile.KindWorkspace, want: filepath.Join(".fused", "workspace.yaml")},
		{kind: configfile.KindSDK, name: "Google Workspace", want: filepath.Join(".fused", "sdks", "Google-Workspace.yaml")},
		{kind: configfile.KindMCP, name: "support/server", want: filepath.Join(".fused", "mcps", "support-server.yaml")},
	}
	for _, test := range tests {
		got, err := scaffoldTargetPath(test.kind, test.name, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Errorf("target path = %q, want %q", got, test.want)
		}
	}
}

func runScaffoldCommandForTest(t *testing.T, path string, args ...string) []byte {
	return runScaffoldCommandWithResolverForTest(t, path, noOpScaffoldRequirements, args...)
}

// runScaffoldCommandWithResolverForTest executes one isolated command with deterministic discovery dependencies.
func runScaffoldCommandWithResolverForTest(t *testing.T, path string, resolver scaffoldRequirementsResolver, args ...string) []byte {
	t.Helper()
	command := newScaffoldCommandWithResolver(configfile.ConfigKind(args[0]), resolver)
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(&bytes.Buffer{})
	oldConfigFile := ConfigFile
	ConfigFile = path
	t.Cleanup(func() { ConfigFile = oldConfigFile })
	command.SetArgs(args[1:])
	if err := command.Execute(); err != nil {
		t.Fatalf("execute scaffold: %v", err)
	}
	return output.Bytes()
}

func executeScaffoldCommandForTest(path string, args ...string) error {
	command := newScaffoldCommandWithResolver(configfile.ConfigKind(args[0]), noOpScaffoldRequirements)
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	oldConfigFile := ConfigFile
	ConfigFile = path
	defer func() { ConfigFile = oldConfigFile }()
	command.SetArgs(args[1:])
	return command.Execute()
}

// noOpScaffoldRequirements preserves offline scaffold coverage while
// production service-bearing commands use Engine-owned requirements.
func noOpScaffoldRequirements([]api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
	return []api.AppScaffoldRequirement{}, nil
}
