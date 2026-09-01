package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestUnifiedInitRoutesExplicitModes proves one command maps user outcomes onto the existing SDK and MCP config kinds.
func TestUnifiedInitRoutesExplicitModes(t *testing.T) {
	tests := []struct {
		name     string
		modeFlag string
		extra    []string
		wantMode unifiedInitMode
		wantKind configfile.ConfigKind
	}{
		{name: "sdk", modeFlag: "--sdk", wantMode: unifiedInitModeSDK, wantKind: configfile.KindSDK},
		{name: "api", modeFlag: "--api", wantMode: unifiedInitModeAPI, wantKind: configfile.KindSDK},
		{name: "mcp", modeFlag: "--mcp", extra: []string{"--description", "Manage support issues."}, wantMode: unifiedInitModeMCP, wantKind: configfile.KindMCP},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotMode unifiedInitMode
			var gotRequest scaffoldRequest
			runner := func(_ *cobra.Command, mode unifiedInitMode, request scaffoldRequest) error {
				gotMode, gotRequest = mode, request
				return nil
			}
			args := []string{"support", test.modeFlag, "--service", "linear", "--select-all", "linear"}
			args = append(args, test.extra...)
			executeUnifiedInitForTest(t, runner, args...)
			// Routing must preserve API as kind:sdk while keeping MCP's hosted kind distinct.
			if gotMode != test.wantMode || gotRequest.kind != test.wantKind {
				t.Fatalf("mode=%q kind=%q, want mode=%q kind=%q", gotMode, gotRequest.kind, test.wantMode, test.wantKind)
			}
		})
	}
}

// TestUnifiedInitModeValidationRejectsAmbiguousAutomation proves scripts cannot depend on a prompt or select competing outcomes.
func TestUnifiedInitModeValidationRejectsAmbiguousAutomation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing mode", args: []string{"support", "--service", "linear", "--select-all", "linear"}, want: "--no-input requires exactly one"},
		{name: "competing modes", args: []string{"support", "--sdk", "--api", "--service", "linear", "--select-all", "linear"}, want: "choose exactly one"},
		{name: "missing service", args: []string{"support", "--sdk"}, want: "at least one --service"},
		{name: "missing mcp description", args: []string{"support", "--mcp", "--service", "linear", "--select-all", "linear"}, want: "requires --description"},
		{name: "API language", args: []string{"support", "--api", "--language", "python", "--service", "linear", "--select-all", "linear"}, want: "--language can only be used with --sdk"},
		{name: "MCP language", args: []string{"support", "--mcp", "--description", "Support users.", "--language", "python", "--service", "linear", "--select-all", "linear"}, want: "--language can only be used with --sdk"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := executeUnifiedInitForTestWithError(t, func(*cobra.Command, unifiedInitMode, scaffoldRequest) error {
				return errors.New("runner must not be reached")
			}, test.args...)
			// Every validation error must be actionable and occur before the injected lifecycle runner.
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

// TestUnifiedInitPromptsForModeAndMCPDescription proves the guided path fills only omitted terminal decisions.
func TestUnifiedInitPromptsForModeAndMCPDescription(t *testing.T) {
	originalModePrompt := selectUnifiedInitMode
	originalDescriptionPrompt := requestUnifiedInitMCPDescription
	originalNoInput := NoInput
	t.Cleanup(func() {
		selectUnifiedInitMode = originalModePrompt
		requestUnifiedInitMCPDescription = originalDescriptionPrompt
		NoInput = originalNoInput
	})
	NoInput = false
	selectUnifiedInitMode = func() (unifiedInitMode, error) { return unifiedInitModeMCP, nil }
	requestUnifiedInitMCPDescription = func() (string, error) { return " Search and update incidents. ", nil }
	var got scaffoldRequest
	command := newUnifiedInitCommandWithRunner(func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error {
		got = request
		return nil
	})
	command.SetArgs([]string{"incident-agent", "--service", "linear", "--select-all", "linear"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute guided init: %v", err)
	}
	// Prompted description is trimmed once and persisted as immutable MCP identity prose.
	if got.kind != configfile.KindMCP || got.description != "Search and update incidents." || !got.descriptionSet {
		t.Fatalf("guided request=%#v", got)
	}
}

// TestUnifiedInitMCPExtendPreservesExistingDescription proves additive automation does not require immutable prose a second time.
func TestUnifiedInitMCPExtendPreservesExistingDescription(t *testing.T) {
	var got scaffoldRequest
	executeUnifiedInitForTest(t, func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error {
		got = request
		return nil
	}, "incident-agent", "--mcp", "--extend", "--service", "linear", "--select-all", "linear")
	// Omitting description on extension leaves the existing field authoritative instead of clearing or conflicting with it.
	if !got.extend || got.descriptionSet || got.description != "" {
		t.Fatalf("MCP extension request=%#v", got)
	}
}

// TestUnifiedInitAPIModeWritesSDKWithoutGeneration proves direct API stays inside the SDK resource contract with codegen explicitly disabled.
func TestUnifiedInitAPIModeWritesSDKWithoutGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct-api.yaml")
	request := scaffoldRequest{
		kind: configfile.KindSDK, name: "direct-api", path: path,
		services:   []scaffoldService{{name: "linear", version: "v1"}},
		operations: []scaffoldOperation{{service: "linear", operation: "issueUpdate"}},
		version:    defaultScaffoldVersion, language: defaultScaffoldLanguage,
		generate: false, generateSet: true,
	}
	_, err := writeUnifiedInitScaffold(unifiedInitModeAPI, request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatalf("write API scaffold: %v", err)
	}
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("parse API scaffold: %v", err)
	}
	// Explicit false is the durable API-mode signal; absence would accidentally request a package.
	if parsed.Kind != configfile.KindSDK || parsed.SDK.Generate == nil || *parsed.SDK.Generate {
		t.Fatalf("API scaffold kind=%q generate=%v", parsed.Kind, parsed.SDK.Generate)
	}
}

// TestUnifiedInitAPIExtendIsAtomicAndPreservesMode proves additive API edits retain no-codegen intent and existing file permissions.
func TestUnifiedInitAPIExtendIsAtomicAndPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct-api.yaml")
	request := scaffoldRequest{
		kind: configfile.KindSDK, name: "direct-api", path: path,
		services:   []scaffoldService{{name: "linear", version: "v1"}},
		operations: []scaffoldOperation{{service: "linear", operation: "issueUpdate"}},
		version:    defaultScaffoldVersion, language: defaultScaffoldLanguage,
		generate: false, generateSet: true,
	}
	if _, err := writeUnifiedInitScaffold(unifiedInitModeAPI, request, noOpScaffoldRequirements, defaultTestScaffoldBucket); err != nil {
		t.Fatalf("create API scaffold: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod API scaffold: %v", err)
	}
	request.extend = true
	request.operations = []scaffoldOperation{{service: "linear", operation: "issueCreate"}}
	if _, err := writeUnifiedInitScaffold(unifiedInitModeAPI, request, noOpScaffoldRequirements, defaultTestScaffoldBucket); err != nil {
		t.Fatalf("extend API scaffold: %v", err)
	}
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("parse extended API scaffold: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat extended API scaffold: %v", err)
	}
	// One atomic merge preserves both the immutable mode and the original private file permission.
	if parsed.SDK.Generate == nil || *parsed.SDK.Generate || len(parsed.SDK.Services["linear"].Operations) != 2 || info.Mode().Perm() != 0o600 {
		t.Fatalf("generate=%v operations=%v mode=%o", parsed.SDK.Generate, parsed.SDK.Services["linear"].Operations, info.Mode().Perm())
	}
}

// TestUnifiedInitAPINextStepPrintsResolvedRESTCall proves API onboarding ends with a copy-ready central execution request.
func TestUnifiedInitAPINextStepPrintsResolvedRESTCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode app resolution: %v", err)
		}
		// The next step must resolve the exact immutable version created by init.
		if body.Variables["reference"] != "direct-api" || body.Variables["version"] != defaultScaffoldVersion {
			t.Fatalf("resolution variables=%#v", body.Variables)
		}
		_, _ = writer.Write([]byte(`{"data":{"appReference":{"id":"app-version-1","kind":"app"}}}`))
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "test-key")
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	parsed := &configfile.ParsedConfig{Kind: configfile.KindSDK, SDK: &configfile.SDKConfig{
		Name: "direct-api", Version: defaultScaffoldVersion,
		Services: map[string]configfile.AppService{"linear": {Operations: []string{"issueUpdate"}}},
	}}
	printUnifiedInitAPINextStep(command, client, parsed)
	text := output.String()
	// The result names the immutable app route, token environment variable, and one selected operation.
	if !strings.Contains(text, server.URL+"/v1/apps/app-version-1/executions") || !strings.Contains(text, "FUSED_SDK_TOKEN") || !strings.Contains(text, "REST request template") || !strings.Contains(text, `"operation":"issueUpdate"`) || !strings.Contains(text, `"input":{}`) || strings.Contains(text, `"params"`) {
		t.Fatalf("API next step=%q", text)
	}
}

// TestUnifiedExtendPlanFailurePreservesOriginalBytes proves remote preflight cannot publish an unaccepted successor locally.
func TestUnifiedExtendPlanFailurePreservesOriginalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support-sdk.yaml")
	original := []byte(`apiVersion: fused/v1
kind: sdk
name: support-sdk
version: 1.0.0
language: typescript
bucket: default
services:
  linear:
    version: v1
    operations: [issueGet]
`)
	// A private authored file makes both content and mode preservation observable.
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// The fixture fails only the exact read-only app plan boundary exercised before replacement.
		if request.URL.Path != "/sdk-config/plan" {
			t.Fatalf("unexpected plan path %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"error":"successor unavailable"}`))
	}))
	defer server.Close()
	request := scaffoldRequest{
		kind: configfile.KindSDK, name: "support-sdk", path: path, extend: true,
		services:   []scaffoldService{{name: "linear", version: "v1"}},
		operations: []scaffoldOperation{{service: "linear", operation: "issueUpdate"}},
		version:    "1.1.0", versionSet: true, generate: true, generateSet: true,
	}
	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, request, false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	data, readErr := os.ReadFile(path)
	info, statErr := os.Stat(path)
	// Any plan failure must leave the last accepted desired state byte-identical and retain its original permissions.
	if err == nil || readErr != nil || statErr != nil || !bytes.Equal(data, original) || info.Mode().Perm() != 0o600 {
		t.Fatalf("error=%v readErr=%v statErr=%v mode=%o file=%q", err, readErr, statErr, info.Mode().Perm(), data)
	}
}

// TestUnifiedInitPinRepairResolutionFailureLeavesNoConfig proves an unresolvable exact snapshot cannot publish the candidate.
func TestUnifiedInitPinRepairResolutionFailureLeavesNoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support-sdk.yaml")
	planCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Repair may read workspace identity after the typed plan failure but must not mutate without one exact match.
		if request.URL.Path == "/engine/graphql" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":{"workspaceServices":[]}}`))
			return
		}
		// No other route besides the initial plan and read-only workspace lookup is permitted.
		if request.URL.Path != "/sdk-config/plan" {
			t.Fatalf("unexpected repair path %q", request.URL.Path)
		}
		planCalls++
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"error":{"code":"generation_contract_pin_unavailable","message":"generation contract pin is unavailable"}}`))
	}))
	defer server.Close()
	request := unifiedInitFailureTestRequest(path)
	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, request, true, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	_, statErr := os.Stat(path)
	message := fmt.Sprint(err)
	// Failure context must identify the outcome, exact selection, unchanged local state, separate workspace receipt, and failed repair stage.
	if err == nil || !errors.Is(statErr, os.ErrNotExist) || planCalls != 1 || !strings.Contains(message, "SDK initialization") || !strings.Contains(message, "linear@v1") || !strings.Contains(message, "no SDK version was created and no config file was written") || !strings.Contains(message, "workspace activation was already applied under its separate receipt") || !strings.Contains(message, "generation snapshot refresh") || !strings.Contains(message, "0 enabled workspace services") {
		t.Fatalf("error=%v statErr=%v planCalls=%d", err, statErr, planCalls)
	}
}

// TestUnifiedInitExistingTargetPrecheckSkipsRemotePlan proves create-only collision protection runs before Engine access.
func TestUnifiedInitExistingTargetPrecheckSkipsRemotePlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support-sdk.yaml")
	original := []byte("existing config must survive\n")
	// A pre-existing target must be preserved even when it is not valid app YAML.
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	planCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		planCalls++
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(path), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	data, readErr := os.ReadFile(path)
	// Collision failure must make no remote call and leave the existing bytes untouched.
	if err == nil || planCalls != 0 || readErr != nil || !bytes.Equal(data, original) || !strings.Contains(err.Error(), "pre-existing config is unchanged") {
		t.Fatalf("error=%v planCalls=%d readErr=%v file=%q", err, planCalls, readErr, data)
	}
}

// TestUnifiedInitDependencyRecoveryNamesEngineLogs covers the second targeted onboarding dependency failure.
func TestUnifiedInitDependencyRecoveryNamesEngineLogs(t *testing.T) {
	cause := &api.APIError{Code: "graphql_dependency_failed", Message: "Engine could not complete the GraphQL request"}
	err := contextualizeUnifiedInitPrecommitFailure("app plan", unifiedInitModeMCP, scaffoldRequest{
		name: "support-mcp", services: []scaffoldService{{name: "linear", version: "v1"}},
	}, false, cause)
	message := err.Error()
	// MCP outcome, selected immutable service, and mono-workspace Engine diagnostics must appear together.
	if !strings.Contains(message, "MCP initialization") || !strings.Contains(message, "linear@v1") || !strings.Contains(message, "Engine logs") || !strings.Contains(message, "Engine can reach Registry") {
		t.Fatalf("dependency recovery=%q", message)
	}
	var apiErr *api.APIError
	// Recovery prose must not replace the typed dependency code.
	if !errors.As(err, &apiErr) || apiErr.Code != "graphql_dependency_failed" {
		t.Fatalf("wrapped API error=%#v", apiErr)
	}
}

// TestSDKInitCompatibilityPlanFailureLeavesNoConfig proves the hidden alias shares the deferred-write primitive.
func TestSDKInitCompatibilityPlanFailureLeavesNoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support-sdk.yaml")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Compatibility init must reach the same SDK plan boundary before publishing its file.
		if request.URL.Path != "/sdk-config/plan" {
			t.Fatalf("unexpected compatibility path %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"error":{"code":"graphql_dependency_failed","message":"Registry dependency failed"}}`))
	}))
	defer server.Close()
	request := unifiedInitFailureTestRequest(path)
	// The compatibility alias preserves absent-means-generate instead of authoring a new explicit field.
	request.generate, request.generateSet = false, false
	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	err := createAndApplySDKInit(command, api.NewClient(server.URL, "test-key"), request, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	_, statErr := os.Stat(path)
	// Dependency preflight failure must preserve absence and retain Engine log recovery guidance.
	if err == nil || !errors.Is(statErr, os.ErrNotExist) || !strings.Contains(err.Error(), "no SDK version was created and no config file was written") || !strings.Contains(err.Error(), "Engine logs") {
		t.Fatalf("error=%v statErr=%v", err, statErr)
	}
}

// unifiedInitFailureTestRequest returns one complete generated SDK candidate for precommit failure tests.
func unifiedInitFailureTestRequest(path string) scaffoldRequest {
	return scaffoldRequest{
		kind: configfile.KindSDK, name: "support-sdk", path: path,
		services:   []scaffoldService{{name: "linear", version: "v1"}},
		operations: []scaffoldOperation{{service: "linear", operation: "issueGet"}},
		version:    "1.0.0", versionSet: true, language: "typescript", languageSet: true,
		bucket: "default", bucketSet: true, generate: true, generateSet: true,
	}
}

// TestResourceInitCommandsRemainHiddenCompatibilityAliases keeps old invocations and JSON clean while removing them from normal discovery.
func TestResourceInitCommandsRemainHiddenCompatibilityAliases(t *testing.T) {
	tests := []struct {
		parent *cobra.Command
		mode   string
	}{
		{parent: sdkCmd, mode: "sdk"},
		{parent: mcpCmd, mode: "mcp"},
	}
	for _, test := range tests {
		command, _, err := test.parent.Find([]string{"init"})
		if err != nil {
			t.Fatalf("find %s init: %v", test.mode, err)
		}
		// Hidden compatibility commands carry migration help without Cobra's deprecation prefix contaminating --json output.
		if command == nil || !command.Hidden || command.Deprecated != "" || !strings.Contains(command.Long, "fused-cli init <app-name> --"+test.mode) {
			t.Fatalf("%s init compatibility metadata: %#v", test.mode, command)
		}
	}
}

// executeUnifiedInitForTest runs one deterministic non-interactive command and fails the test on error.
func executeUnifiedInitForTest(t *testing.T, runner unifiedInitRunner, args ...string) {
	t.Helper()
	if err := executeUnifiedInitForTestWithError(t, runner, args...); err != nil {
		t.Fatalf("execute unified init: %v", err)
	}
}

// executeUnifiedInitForTestWithError isolates global CLI flags while returning the command result for validation tests.
func executeUnifiedInitForTestWithError(t *testing.T, runner unifiedInitRunner, args ...string) error {
	t.Helper()
	originalNoInput := NoInput
	originalConfigFile := ConfigFile
	t.Cleanup(func() {
		NoInput = originalNoInput
		ConfigFile = originalConfigFile
	})
	NoInput = true
	ConfigFile = ""
	command := newUnifiedInitCommandWithRunner(runner)
	command.SetArgs(args)
	return command.Execute()
}
