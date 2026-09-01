package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestUnifiedExtendInfersRuntimeModeFromExistingConfig proves YAML kind and generate policy replace public mode flags.
func TestUnifiedExtendInfersRuntimeModeFromExistingConfig(t *testing.T) {
	tests := []struct {
		name string
		body string
		mode unifiedInitMode
	}{
		{name: "sdk", body: unifiedExtendSDKFixture("support", ""), mode: unifiedInitModeSDK},
		{name: "api", body: unifiedExtendSDKFixture("support", "generate: false\n"), mode: unifiedInitModeAPI},
		{name: "mcp", body: unifiedExtendMCPFixture("support"), mode: unifiedInitModeMCP},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name+".yaml")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			target, err := parseUnifiedExtendTarget(path, "support")
			// Existing config is the sole mode authority and its exact path must survive inference.
			if err != nil || target.mode != test.mode || target.path != path {
				t.Fatalf("target=%#v err=%v, want mode=%q path=%q", target, err, test.mode, path)
			}
		})
	}
}

// TestUnifiedExtendDiscoveryRejectsAmbiguousNames proves same-name app kinds require an exact -f target.
func TestUnifiedExtendDiscoveryRejectsAmbiguousNames(t *testing.T) {
	dir := t.TempDir()
	writeUnifiedExtendFixture(t, filepath.Join(dir, ".fused", "sdks", "support.yaml"), unifiedExtendSDKFixture("support", ""))
	writeUnifiedExtendFixture(t, filepath.Join(dir, ".fused", "mcps", "support.yaml"), unifiedExtendMCPFixture("support"))
	withUnifiedExtendWorkingDirectory(t, dir)
	originalConfigFile := ConfigFile
	ConfigFile = ""
	t.Cleanup(func() { ConfigFile = originalConfigFile })
	_, err := resolveUnifiedExtendTarget("support")
	// The diagnostic identifies ambiguity and the exact escape hatch without choosing SDK or MCP by directory order.
	if err == nil || !strings.Contains(err.Error(), "multiple configs named") || !strings.Contains(err.Error(), "-f <exact-config-path>") {
		t.Fatalf("ambiguous target error=%v", err)
	}
	ConfigFile = filepath.Join(".fused", "sdks", "support.yaml")
	target, err := resolveUnifiedExtendTarget("support")
	// Exact -f must deterministically select the generated SDK candidate.
	if err != nil || target.mode != unifiedInitModeSDK {
		t.Fatalf("explicit target=%#v err=%v", target, err)
	}
}

// TestUnifiedExtendRequiresExistingTarget proves the additive surface never falls back to implicit creation.
func TestUnifiedExtendRequiresExistingTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".fused"), 0o755); err != nil {
		t.Fatal(err)
	}
	withUnifiedExtendWorkingDirectory(t, dir)
	originalConfigFile := ConfigFile
	ConfigFile = ""
	t.Cleanup(func() { ConfigFile = originalConfigFile })
	_, err := resolveUnifiedExtendTarget("missing")
	// Missing desired state must direct users to creation or an exact existing file.
	if err == nil || !strings.Contains(err.Error(), "no SDK, API, or MCP config") || !strings.Contains(err.Error(), "fused-cli init missing") {
		t.Fatalf("missing target error=%v", err)
	}
}

// TestUnifiedExtendCommandBuildsSuccessorRequest proves the root wrapper preserves inferred identity and explicit immutable successor intent.
func TestUnifiedExtendCommandBuildsSuccessorRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support.yaml")
	if err := os.WriteFile(path, []byte(unifiedExtendSDKFixture("support", "generate: false\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	originalConfigFile, originalNoInput := ConfigFile, NoInput
	ConfigFile, NoInput = path, true
	t.Cleanup(func() {
		ConfigFile, NoInput = originalConfigFile, originalNoInput
	})
	var gotMode unifiedInitMode
	var gotRequest scaffoldRequest
	command := newUnifiedExtendCommandWithRunner(func(_ *cobra.Command, mode unifiedInitMode, request scaffoldRequest) error {
		gotMode, gotRequest = mode, request
		return nil
	})
	command.SetArgs([]string{"support", "--operation", "linear=issueUpdate", "--version", "1.1.0"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute extend: %v", err)
	}
	// API inference keeps no-codegen, exact file identity, and the explicit successor inside the shared lifecycle request.
	if gotMode != unifiedInitModeAPI || gotRequest.kind != configfile.KindSDK || gotRequest.path != path || !gotRequest.extend || gotRequest.version != "1.1.0" || !gotRequest.versionSet || !gotRequest.generateSet || gotRequest.generate {
		t.Fatalf("mode=%q request=%#v", gotMode, gotRequest)
	}
}

// TestUnifiedExtendBareTerminalUsesExistingServices proves operation search opens without requiring users to repeat service flags.
func TestUnifiedExtendBareTerminalUsesExistingServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support.yaml")
	if err := os.WriteFile(path, []byte(unifiedExtendSDKFixture("support", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := parseUnifiedExtendTarget(path, "support")
	if err != nil {
		t.Fatal(err)
	}
	originalNoInput := NoInput
	NoInput = false
	t.Setenv("CI", "")
	t.Cleanup(func() { NoInput = originalNoInput })
	command := newUnifiedExtendCommandWithRunner(nil)
	request, err := buildUnifiedExtendRequest(command, target, &unifiedExtendOptions{})
	// Existing explicitly scoped services seed the same searchable operation selector used by init.
	if err != nil || len(request.services) != 1 || request.services[0].name != "linear" || request.services[0].version != "v1" {
		t.Fatalf("request=%#v err=%v", request, err)
	}
}

// TestUnifiedExtendInheritsPinnedServiceVersion proves a repeated configured service does not drift to Registry latest.
func TestUnifiedExtendInheritsPinnedServiceVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support.yaml")
	if err := os.WriteFile(path, []byte(unifiedExtendSDKFixture("support", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := parseUnifiedExtendTarget(path, "support")
	if err != nil {
		t.Fatal(err)
	}
	command := newUnifiedExtendCommandWithRunner(nil)
	request, err := buildUnifiedExtendRequest(command, target, &unifiedExtendOptions{services: []string{"linear"}})
	// The existing v1 pin remains authoritative when the additive flag omits a provider version.
	if err != nil || len(request.services) != 1 || request.services[0].version != "v1" {
		t.Fatalf("request=%#v err=%v", request, err)
	}
}

// TestUnifiedExtendNoInputRequiresExactChange proves automation cannot trigger an interactive operation search.
func TestUnifiedExtendNoInputRequiresExactChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support.yaml")
	if err := os.WriteFile(path, []byte(unifiedExtendSDKFixture("support", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := parseUnifiedExtendTarget(path, "support")
	if err != nil {
		t.Fatal(err)
	}
	originalNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = originalNoInput })
	command := newUnifiedExtendCommandWithRunner(nil)
	_, err = buildUnifiedExtendRequest(command, target, &unifiedExtendOptions{})
	// The remediation lists every deterministic selection or successor flag accepted by the wrapper.
	if err == nil || !strings.Contains(err.Error(), "--no-input extend requires --service, --operation, --select-all, or --version") {
		t.Fatalf("no-input error=%v", err)
	}
}

// TestUnifiedInitExtendFlagRemainsHiddenCompatibility proves scripts retain the old spelling without advertising it beside root extend.
func TestUnifiedInitExtendFlagRemainsHiddenCompatibility(t *testing.T) {
	command := newUnifiedInitCommandWithRunner(nil)
	flag := command.Flags().Lookup("extend")
	// Hidden affects help and completion only; Cobra continues parsing the compatibility flag.
	if flag == nil || !flag.Hidden {
		t.Fatalf("init --extend flag=%#v", flag)
	}
}

// writeUnifiedExtendFixture creates one parent directory and exact YAML file for target-discovery tests.
func writeUnifiedExtendFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withUnifiedExtendWorkingDirectory scopes process-wide config discovery to one isolated test workspace.
func withUnifiedExtendWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
}

// unifiedExtendSDKFixture returns one valid SDK or API document with an optional generate field.
func unifiedExtendSDKFixture(name, generate string) string {
	return `apiVersion: fused/v1
kind: sdk
name: ` + name + `
version: 1.0.0
language: typescript
bucket: default
` + generate + `services:
  linear:
    version: v1
    operations: [issueGet]
`
}

// unifiedExtendMCPFixture returns one valid hosted MCP document for mode and ambiguity tests.
func unifiedExtendMCPFixture(name string) string {
	return `apiVersion: fused/v1
kind: mcp
name: ` + name + `
version: 1.0.0
description: Search and update support issues.
bucket: default
services:
  linear:
    version: v1
    operations: [issueGet]
`
}
