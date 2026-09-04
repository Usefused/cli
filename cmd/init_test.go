package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// TestUnifiedInitNoApplyFlagReachesLifecycleRequest proves deferred initialization is an explicit root-command intent.
func TestUnifiedInitNoApplyFlagReachesLifecycleRequest(t *testing.T) {
	var got scaffoldRequest
	executeUnifiedInitForTest(t, func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error {
		got = request
		return nil
	}, "support", "--sdk", "--service", "linear", "--select-all", "linear", "--no-apply")
	// The lifecycle runner, rather than Cobra presentation code, owns the no-mutation boundary.
	if !got.noApply {
		t.Fatal("--no-apply was not preserved in the lifecycle request")
	}
}

// TestUnifiedInitNoApplyPlansWorkspaceWithoutApplying proves missing services retain a workspace receipt while app planning waits for activation.
func TestUnifiedInitNoApplyPlansWorkspaceWithoutApplying(t *testing.T) {
	directory := t.TempDir()
	server, lifecycleCalls := newSDKInitLifecycleServer(t)
	defer server.Close()
	oldWorkingDirectory, err := os.Getwd()
	// The test must restore the caller's directory after inspecting default .fused paths.
	if err != nil {
		t.Fatal(err)
	}
	// Running from an isolated directory makes every deferred artifact observable.
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	oldEngineURL, oldAPIKey, oldConfigFile, oldNoInput := EngineURL, APIKey, ConfigFile, NoInput
	t.Cleanup(func() {
		_ = os.Chdir(oldWorkingDirectory)
		EngineURL, APIKey, ConfigFile, NoInput = oldEngineURL, oldAPIKey, oldConfigFile, oldNoInput
	})
	EngineURL, APIKey, ConfigFile, NoInput = server.URL, "fsk_test", "", true
	var output bytes.Buffer
	command := newUnifiedInitCommand()
	command.SetOut(&output)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"deferred-sdk", "--sdk", "--service", "linear", "--operation", "linear=issueUpdate", "--no-apply"})
	// The public command must complete without a lifecycle mutation endpoint.
	if err := command.Execute(); err != nil {
		t.Fatalf("execute deferred init: %v", err)
	}
	workspacePath := filepath.Join(directory, ".fused", "workspace.yaml")
	appPath := filepath.Join(directory, ".fused", "sdks", "deferred-sdk.yaml")
	workspace, workspaceErr := configfile.ParseFile(workspacePath)
	app, appErr := configfile.ParseFile(appPath)
	// Both validated files must retain the one exact resolved Registry version for later review.
	if workspaceErr != nil || appErr != nil || !configWorkspaceServiceHasVersion(workspace.Workspace.Services["linear"], "v1") || app.SDK.Services["linear"].Version != "v1" {
		t.Fatalf("workspaceErr=%v appErr=%v workspace=%#v app=%#v", workspaceErr, appErr, workspace, app)
	}
	// Workspace planning is the only lifecycle call possible before the missing service version is active.
	if strings.Join(*lifecycleCalls, ",") != "workspace-plan" {
		t.Fatalf("lifecycle calls=%#v, want workspace plan only", *lifecycleCalls)
	}
	workspaceReceipt := readReceipt(t, filepath.Join(directory, ".fused", ".state", "workspace.plan.json"))
	// The saved receipt must be the exact plan returned by the workspace boundary.
	if workspaceReceipt.PlanID != "plan-workspace" {
		t.Fatalf("workspace receipt=%#v", workspaceReceipt)
	}
	for _, path := range []string{
		filepath.Join(directory, ".fused", ".state", "sdk.deferred-sdk.1.0.0.plan.json"),
		filepath.Join(directory, "fused-sdks", "deferred-sdk"),
	} {
		// The app cannot be planned before activation, and no package may imply an apply occurred.
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("deferred artifact %s exists: %v", path, statErr)
		}
	}
	text := output.String()
	// Completion output consumes the workspace receipt before creating the app receipt and later downloading the SDK.
	if !strings.Contains(text, "Initialization planned. No Engine changes were applied.") ||
		!strings.Contains(text, "fused-cli workspace apply -f '.fused/workspace.yaml'") ||
		!strings.Contains(text, "fused-cli sdk plan -f '.fused/sdks/deferred-sdk.yaml'") ||
		!strings.Contains(text, "fused-cli sdk apply -f '.fused/sdks/deferred-sdk.yaml' --download") ||
		!strings.Contains(text, "If a saved plan is stale") {
		t.Fatalf("deferred init output=%q", text)
	}
}

// TestUnifiedInitNoApplyPlansAppWhenServicesAreEnabled proves a ready workspace produces the app receipt while still skipping apply and download.
func TestUnifiedInitNoApplyPlansAppWhenServicesAreEnabled(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	planCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// App plan is the only admitted network lifecycle boundary for an already-enabled selection.
		if request.URL.Path != "/sdk-config/plan" {
			t.Fatalf("deferred app init reached %s", request.URL.Path)
		}
		planCalls++
		writeSDKInitPlanResponse(t, writer, request, "plan-sdk")
	}))
	defer server.Close()
	request := unifiedInitFailureTestRequest(filepath.Join(directory, "ready-sdk.yaml"))
	lifecycle := sdkInitLifecycle{
		client:  api.NewClient(server.URL, "test-key"),
		request: request,
		services: []sdkInitResolvedService{{
			target: workspaceServiceAddTarget{slug: "linear", enabledVersions: []string{"v1"}}, version: "v1",
		}},
	}
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	// Planning should finish locally after receiving one app plan and before any apply call.
	if err := createUnifiedInitWithoutApply(command, unifiedInitModeSDK, lifecycle, noOpScaffoldRequirements, defaultTestScaffoldBucket); err != nil {
		t.Fatalf("plan deferred app init: %v", err)
	}
	receipt := readReceipt(t, filepath.Join(directory, defaultReceiptPath("sdk:support-sdk:1.0.0")))
	// One app plan and its exact receipt prove the flow stopped after planning instead of silently applying.
	if planCalls != 1 || receipt.PlanID != "plan-sdk" {
		t.Fatalf("planCalls=%d receipt=%#v", planCalls, receipt)
	}
	text := output.String()
	// A ready app needs only apply later; reprinting plan would obscure that its receipt was already saved.
	if !strings.Contains(text, "fused-cli sdk apply") || strings.Contains(text, "fused-cli sdk plan -f") || !strings.Contains(text, "--download") {
		t.Fatalf("deferred ready-app output=%q", text)
	}
}

// TestUnifiedInitNoApplyReturnsModeSpecificApplyCommands proves deferred output maps each public outcome to its real apply surface.
func TestUnifiedInitNoApplyReturnsModeSpecificApplyCommands(t *testing.T) {
	tests := []struct {
		name      string
		mode      unifiedInitMode
		want      string
		forbidden string
	}{
		{name: "SDK downloads", mode: unifiedInitModeSDK, want: "fused-cli sdk apply -f 'app.yaml' --download"},
		{name: "API uses SDK resource", mode: unifiedInitModeAPI, want: "fused-cli sdk apply -f 'app.yaml'", forbidden: "--download"},
		{name: "MCP uses hosted resource", mode: unifiedInitModeMCP, want: "fused-cli mcp apply -f 'app.yaml'", forbidden: "--download"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			command := &cobra.Command{}
			command.SetOut(&output)
			printUnifiedInitDeferredNextSteps(command, test.mode, "app.yaml", nil, true)
			text := output.String()
			// Each user-facing mode must return a copy-ready command without leaking another mode's package behavior.
			if !strings.Contains(text, test.want) || (test.forbidden != "" && strings.Contains(text, test.forbidden)) {
				t.Fatalf("output=%q want=%q forbidden=%q", text, test.want, test.forbidden)
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

// TestUnifiedInitAPINextStepPrintsAppliedRESTCall proves API onboarding uses apply identity without an eventually consistent lookup.
func TestUnifiedInitAPINextStepPrintsAppliedRESTCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Any request proves the next-step renderer regressed to a name lookup after a committed apply.
		t.Fatalf("unexpected API next-step network request: %s %s", request.Method, request.URL.Path)
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
	printUnifiedInitAPINextStep(command, client, parsed, "app-version-1")
	text := output.String()
	// The result names the immutable app route, token environment variable, and one selected operation.
	if !strings.Contains(text, server.URL+"/v1/apps/app-version-1/executions") || !strings.Contains(text, "FUSED_SDK_TOKEN") || !strings.Contains(text, "REST request template") || !strings.Contains(text, `"operation":"issueUpdate"`) || !strings.Contains(text, `"input":{}`) || strings.Contains(text, `"params"`) {
		t.Fatalf("API next step=%q", text)
	}
}

// TestUnifiedAPIInitCarriesApplyVersionIDToNextStep proves the composed workflow performs no post-commit name lookup.
func TestUnifiedAPIInitCarriesApplyVersionIDToNextStep(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Plan and apply are the complete API-init network lifecycle; GraphQL resolution would be an unexpected third call.
		switch request.URL.Path {
		case "/sdk-config/plan":
			payload, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read plan: %v", err)
			}
			writeUnifiedInitGenerationRepairPlan(t, writer, payload)
		case "/sdk-config/apply":
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-sdk","app_family_id":"api-family","app_id":"api-version-from-apply","generation_status":"skipped"}`))
		default:
			t.Fatalf("unexpected API init path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	request := unifiedInitFailureTestRequest(filepath.Join(directory, "support-api.yaml"))
	request.generate = false
	request.generateSet = true
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeAPI, request, false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	// The concrete route must use the apply-returned immutable ID even while name-based authorization caches could still be converging.
	if err != nil || !strings.Contains(output.String(), server.URL+"/v1/apps/api-version-from-apply/executions") {
		t.Fatalf("error=%v output=%q", err, output.String())
	}
}

// TestUnifiedAPIInitUsesAPIUserFacingLabels proves composed onboarding hides
// its shared SDK adapter from scaffold, plan, readiness, and notification prose.
func TestUnifiedAPIInitUsesAPIUserFacingLabels(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	var plannedConfigKey string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// API mode deliberately retains the SDK plan/apply transport routes.
		switch request.URL.Path {
		case "/sdk-config/plan":
			var payload map[string]any
			// Echoing the canonical identity lets the test prove only presentation was aliased.
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode API plan: %v", err)
			}
			plannedConfigKey, _ = payload["config_key"].(string)
			_, _ = fmt.Fprintf(writer, `{"plan_id":"plan-api","config_key":%q,"source_hash":%q,"summary":{},"notifications":{"warnings":["registry_notifications_unavailable"]}}`, payload["config_key"], payload["source_hash"])
		case "/sdk-config/apply":
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-api","app_family_id":"api-family","app_id":"api-version","generation_status":"skipped"}`))
		default:
			t.Fatalf("unexpected API init path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	path := filepath.Join(directory, "support-api.yaml")
	request := unifiedInitFailureTestRequest(path)
	request.name, request.generate, request.generateSet = "support-api", false, true
	var commandOutput bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&commandOutput)
	var initErr error
	stdout := captureStdout(t, func() {
		initErr = createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeAPI, request, false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	})
	output := commandOutput.String() + stdout
	// Every human lifecycle label must describe the selected API outcome while preserving the sdk-prefixed Engine key.
	if initErr != nil || plannedConfigKey != "sdk:support-api:1.0.0" || !strings.Contains(output, "Created api config skeleton") || !strings.Contains(output, "API plan created for api:support-api:1.0.0") || !strings.Contains(output, "Workspace notifications for api:support-api:1.0.0") || strings.Contains(output, "Created sdk config skeleton") || strings.Contains(output, "Workspace notifications for sdk:support-api:1.0.0") {
		t.Fatalf("error=%v plannedKey=%q output=%q", initErr, plannedConfigKey, output)
	}
	parsed, err := configfile.ParseFile(path)
	// Parsing must succeed before the persisted API-mode invariants can be inspected.
	if err != nil {
		t.Fatalf("parse API config: %v", err)
	}
	// The public alias must not create a new config kind or enable package generation on disk.
	if parsed.Kind != configfile.KindSDK || parsed.SDK.Generate == nil || *parsed.SDK.Generate {
		t.Fatalf("kind=%q generate=%v", parsed.Kind, parsed.SDK.Generate)
	}
	receipt, err := readPlanReceiptFile(defaultReceiptPath("sdk:support-api:1.0.0"))
	// Receipt identity remains canonical so normal sdk apply can consume it unchanged.
	if err != nil || receipt.ConfigKey != "sdk:support-api:1.0.0" {
		t.Fatalf("receipt=%#v error=%v", receipt, err)
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

// TestUnifiedInitPrintsPlanReadinessAndNotifications proves the combined lifecycle retains the plan's non-blocking human guidance.
func TestUnifiedInitPrintsPlanReadinessAndNotifications(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	originalNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = originalNoInput })
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Plan returns both guidance surfaces; apply then completes normally.
		switch request.URL.Path {
		case "/sdk-config/plan":
			var payload map[string]any
			// Echoing the candidate identity keeps the receipt bound to the exact file init will publish.
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode plan: %v", err)
			}
			_, _ = fmt.Fprintf(writer, `{"plan_id":"plan-sdk","config_key":%q,"source_hash":%q,"summary":{},"credential_readiness":{"bucket":{"id":"11111111-1111-4111-8111-111111111111","name":"default"},"missing_credentials":[{"service_id":"22222222-2222-4222-8222-222222222222","service":"Linear","auth_type":"api_key","auth_name":"linearKey","required_fields":[{"name":"api_key","secret_key":"linearKey"}]}]},"notifications":{"warnings":["registry_notifications_unavailable"]}}`, payload["config_key"], payload["source_hash"])
		case "/sdk-config/apply":
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-sdk","app_family_id":"family-1","app_id":"app-1"}`))
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(filepath.Join(directory, "support-sdk.yaml")), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	// Combined init must not hide readiness or notifications that ordinary plan users receive.
	if err != nil || !strings.Contains(output.String(), "Credential readiness for sdk:support-sdk:1.0.0") || !strings.Contains(output.String(), "fused-cli secret set '22222222-2222-4222-8222-222222222222' --bucket '11111111-1111-4111-8111-111111111111' --interactive") || !strings.Contains(output.String(), "registry_notifications_unavailable") {
		t.Fatalf("error=%v output=%q", err, output.String())
	}
}

// TestUnifiedInitCreateNonCommitRemovesLocalState proves a rejected creation restores definite config and receipt absence.
func TestUnifiedInitCreateNonCommitRemovesLocalState(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	configPath := filepath.Join(directory, "support-sdk.yaml")
	err, output := runUnifiedInitNotCommitted(t, unifiedInitFailureTestRequest(configPath))
	assertUnifiedInitNotCommitted(t, err, output)
	// Creation rollback returns the config target to definite absence.
	if _, statErr := os.Stat(configPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("created candidate survived rollback: %v", statErr)
	}
	receiptPath := filepath.Join(directory, defaultReceiptPath("sdk:support-sdk:1.0.0"))
	// Creation rollback restores definite receipt absence alongside config absence.
	if _, statErr := os.Stat(receiptPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("created receipt survived rollback: %v", statErr)
	}
}

// TestUnifiedInitExtendNonCommitRestoresLocalState proves a rejected successor restores exact prior bytes and permissions.
func TestUnifiedInitExtendNonCommitRestoresLocalState(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	configPath := filepath.Join(directory, "support-sdk.yaml")
	originalConfig := []byte("apiVersion: fused/v1\nkind: sdk\nname: support-sdk\nversion: 1.0.0\nlanguage: typescript\nbucket: default\nservices:\n  linear:\n    version: v1\n    operations: [issueGet]\n")
	// Private mode makes permission restoration observable alongside byte restoration.
	if err := os.WriteFile(configPath, originalConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(directory, defaultReceiptPath("sdk:support-sdk:1.1.0"))
	originalReceipt := []byte("prior extension receipt\n")
	// A pre-existing successor receipt proves rollback restores replaced bytes rather than merely deleting it.
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, originalReceipt, 0o600); err != nil {
		t.Fatal(err)
	}
	request := unifiedInitFailureTestRequest(configPath)
	request.extend, request.version, request.versionSet = true, "1.1.0", true
	request.operations = []scaffoldOperation{{service: "linear", operation: "issueUpdate"}}
	err, output := runUnifiedInitNotCommitted(t, request)
	assertUnifiedInitNotCommitted(t, err, output)
	assertUnifiedInitRestoredFile(t, configPath, originalConfig, "config")
	assertUnifiedInitRestoredFile(t, receiptPath, originalReceipt, "receipt")
}

// runUnifiedInitNotCommitted executes one plan-success/apply-rejection lifecycle and captures its human output.
func runUnifiedInitNotCommitted(t *testing.T, request scaffoldRequest) (error, string) {
	t.Helper()
	server := newUnifiedInitApplyFailureServer(t, http.StatusForbidden, `{"error":{"code":"sdk_family_limit_exceeded","message":"SDK family limit exceeded","category":"quota","retryable":false,"phase":"apply_admission","commit_state":"not_committed"}}`)
	defer server.Close()
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, request, false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	return err, output.String()
}

// assertUnifiedInitNotCommitted verifies the Engine proof and local rollback are both visible to the caller.
func assertUnifiedInitNotCommitted(t *testing.T, err error, output string) {
	t.Helper()
	var apiErr *api.APIError
	// Proven quota rejection must retain its typed cause and report local restoration.
	if !errors.As(err, &apiErr) || apiErr.CommitState != "not_committed" || !strings.Contains(output, "Reverted local config") {
		t.Fatalf("error=%v API=%#v output=%q", err, apiErr, output)
	}
}

// assertUnifiedInitRestoredFile verifies exact prior bytes and private mode after rollback.
func assertUnifiedInitRestoredFile(t *testing.T, path string, expected []byte, label string) {
	t.Helper()
	data, readErr := os.ReadFile(path)
	info, statErr := os.Stat(path)
	// Both filesystem reads must succeed before comparing the complete restoration invariant.
	if readErr != nil || statErr != nil || !bytes.Equal(data, expected) || info.Mode().Perm() != 0o600 {
		t.Fatalf("%s readErr=%v statErr=%v mode=%o data=%q", label, readErr, statErr, info.Mode().Perm(), data)
	}
}

// TestUnifiedInitAmbiguousOrCommittedApplyPreservesCandidate keeps inspectable desired state whenever rollback would be unsafe.
func TestUnifiedInitAmbiguousOrCommittedApplyPreservesCandidate(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "unknown proxy outcome", status: http.StatusBadGateway, body: `bad gateway`},
		{name: "committed follow-up failure", status: http.StatusFailedDependency, body: `{"error":{"code":"sdk_followup_failed","message":"post-commit follow-up failed","category":"partial","retryable":false,"phase":"generation","commit_state":"committed"}}`},
	}
	// Both non-negative outcomes must leave local desired state inspectable.
	for _, test := range tests {
		// Unknown and positive commit outcomes both forbid destructive local rollback.
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
			path := filepath.Join(directory, "support-sdk.yaml")
			server := newUnifiedInitApplyFailureServer(t, test.status, test.body)
			defer server.Close()
			command := &cobra.Command{}
			command.SetOut(&bytes.Buffer{})
			err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(path), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
			// A failed command still leaves the planned candidate available for exact state comparison and recovery.
			if err == nil {
				t.Fatal("expected apply failure")
			}
			// Candidate presence is the recovery evidence needed to compare with remote immutable state.
			if _, statErr := os.Stat(path); statErr != nil {
				t.Fatalf("candidate was removed: %v", statErr)
			}
			receiptPath := filepath.Join(directory, defaultReceiptPath("sdk:support-sdk:1.0.0"))
			receipt, receiptErr := readPlanReceiptFile(receiptPath)
			// Unsafe rollback outcomes retain the new receipt paired with the inspectable candidate config.
			if receiptErr != nil || receipt.PlanID != "plan-sdk" || receipt.ConfigKey != "sdk:support-sdk:1.0.0" {
				t.Fatalf("receipt=%#v error=%v", receipt, receiptErr)
			}
		})
	}
}

// TestUnifiedInitNonCommitPreservesConcurrentReceiptAndCandidate proves rollback never overwrites a newer plan receipt.
func TestUnifiedInitNonCommitPreservesConcurrentReceiptAndCandidate(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	configPath := filepath.Join(directory, "support-sdk.yaml")
	receiptPath := filepath.Join(directory, defaultReceiptPath("sdk:support-sdk:1.0.0"))
	concurrentReceipt := []byte("concurrent plan receipt\n")
	server := newUnifiedInitConcurrentReceiptServer(t, receiptPath, concurrentReceipt)
	defer server.Close()
	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(configPath), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	var apiErr *api.APIError
	// The typed non-commit proof survives, but concurrent receipt ownership blocks both local rollback mutations.
	if !errors.As(err, &apiErr) || apiErr.CommitState != "not_committed" || !strings.Contains(err.Error(), "plan receipt") || !strings.Contains(err.Error(), "changed after publication") {
		t.Fatalf("error=%v API=%#v", err, apiErr)
	}
	data, readErr := os.ReadFile(receiptPath)
	// The concurrent receipt and paired candidate config must both remain untouched for operator reconciliation.
	if readErr != nil || !bytes.Equal(data, concurrentReceipt) {
		t.Fatalf("receipt readErr=%v data=%q", readErr, data)
	}
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Fatalf("candidate config was rolled back despite concurrent receipt: %v", statErr)
	}
}

// newUnifiedInitConcurrentReceiptServer replaces the receipt at apply time before returning authoritative negative commit proof.
func newUnifiedInitConcurrentReceiptServer(t *testing.T, receiptPath string, concurrentReceipt []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Only plan and apply belong to this byte-ownership race fixture.
		switch request.URL.Path {
		case "/sdk-config/plan":
			payload, err := io.ReadAll(request.Body)
			// Complete plan bytes are required to echo the exact source identity.
			if err != nil {
				t.Fatalf("read plan: %v", err)
			}
			writeUnifiedInitGenerationRepairPlan(t, writer, payload)
		case "/sdk-config/apply":
			// The concurrent receipt takes ownership before Engine's negative proof reaches rollback.
			if err := os.WriteFile(receiptPath, concurrentReceipt, 0o600); err != nil {
				t.Fatalf("write concurrent receipt: %v", err)
			}
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"error":{"code":"sdk_family_limit_exceeded","message":"SDK family limit exceeded","category":"quota","retryable":false,"phase":"apply_admission","commit_state":"not_committed"}}`))
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
}

// TestUnifiedInitScaffoldOutputFailureRollsBackConfig proves a local presentation error cannot leave an unpublished candidate behind.
func TestUnifiedInitScaffoldOutputFailureRollsBackConfig(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	applyCalls := 0
	server := newUnifiedInitPreApplyTestServer(t, &applyCalls)
	defer server.Close()
	configPath := filepath.Join(directory, "support-sdk.yaml")
	command := &cobra.Command{}
	command.Flags().Bool(jsonOutputFlag, false, "")
	// JSON makes scaffold output propagate the injected writer failure instead of using best-effort human formatting.
	if err := command.Flags().Set(jsonOutputFlag, "true"); err != nil {
		t.Fatal(err)
	}
	command.SetOut(alwaysFailInitWriter{})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(configPath), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	// Failure occurs before receipt publication and apply, restoring both paths to absence.
	if err == nil || applyCalls != 0 {
		t.Fatalf("error=%v applyCalls=%d", err, applyCalls)
	}
	assertUnifiedInitLocalPathsAbsent(t, configPath, filepath.Join(directory, defaultReceiptPath("sdk:support-sdk:1.0.0")))
}

// TestUnifiedInitPlanSummaryFailureRollsBackConfigAndReceipt proves review rendering remains inside the pre-apply local transaction.
func TestUnifiedInitPlanSummaryFailureRollsBackConfigAndReceipt(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	applyCalls := 0
	server := newUnifiedInitPreApplyTestServer(t, &applyCalls)
	defer server.Close()
	configPath := filepath.Join(directory, "support-sdk.yaml")
	command := &cobra.Command{}
	// Scaffold and plan-heading writes succeed; the third write fails exactly at the empty plan summary.
	command.SetOut(&failAfterInitWrites{successfulWrites: 2})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(configPath), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	// Summary failure happens after receipt publication but before apply, so both local artifacts must be removed.
	if err == nil || !strings.Contains(err.Error(), "failed to render plan summary") || applyCalls != 0 {
		t.Fatalf("error=%v applyCalls=%d", err, applyCalls)
	}
	assertUnifiedInitLocalPathsAbsent(t, configPath, filepath.Join(directory, defaultReceiptPath("sdk:support-sdk:1.0.0")))
}

// TestUnifiedInitApplyPreparationFailureRollsBackConfigAndReceipt proves Engine-target validation cannot strand pre-apply local state.
func TestUnifiedInitApplyPreparationFailureRollsBackConfigAndReceipt(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	applyCalls := 0
	server := newUnifiedInitPreApplyTestServer(t, &applyCalls)
	defer server.Close()
	configPath := filepath.Join(directory, "support-sdk.yaml")
	client := api.NewClient(server.URL, "test-key")
	command := &cobra.Command{}
	command.SetOut(&callbackInitWriter{callback: func(write int) {
		// The second write occurs after receipt publication and changes only the active target used by prepareConfigApply.
		if write == 2 {
			client.BaseURL = "https://different-engine.example.com"
		}
	}})
	err := createPlanApplyUnifiedInit(command, client, unifiedInitModeSDK, unifiedInitFailureTestRequest(configPath), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	// Target mismatch is local proof that apply was never called and both publications are safe to undo.
	if err == nil || !strings.Contains(err.Error(), "receipt target invalid") || applyCalls != 0 {
		t.Fatalf("error=%v applyCalls=%d", err, applyCalls)
	}
	assertUnifiedInitLocalPathsAbsent(t, configPath, filepath.Join(directory, defaultReceiptPath("sdk:support-sdk:1.0.0")))
}

// alwaysFailInitWriter injects a deterministic first-write failure into structured scaffold output.
type alwaysFailInitWriter struct{}

// Write rejects every byte so tests can observe rollback before receipt publication.
func (alwaysFailInitWriter) Write([]byte) (int, error) {
	return 0, errors.New("injected output failure")
}

// failAfterInitWrites lets early lifecycle output succeed before failing one exact later review write.
type failAfterInitWrites struct {
	writes           int
	successfulWrites int
}

// Write succeeds for the configured prefix and then returns a deterministic presentation failure.
func (writer *failAfterInitWrites) Write(payload []byte) (int, error) {
	writer.writes++
	// Crossing the successful prefix selects the exact downstream output boundary under test.
	if writer.writes > writer.successfulWrites {
		return 0, errors.New("injected output failure")
	}
	return len(payload), nil
}

// callbackInitWriter observes successful writes so a test can change local preflight context between lifecycle stages.
type callbackInitWriter struct {
	writes   int
	callback func(int)
}

// Write invokes the test callback without altering the output contract.
func (writer *callbackInitWriter) Write(payload []byte) (int, error) {
	writer.writes++
	// Nil callback remains a valid pass-through writer for focused tests.
	if writer.callback != nil {
		writer.callback(writer.writes)
	}
	return len(payload), nil
}

// newUnifiedInitPreApplyTestServer returns a successful plan fixture that counts any forbidden apply request.
func newUnifiedInitPreApplyTestServer(t *testing.T, applyCalls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Plan succeeds while an apply call remains observable as a test failure condition.
		switch request.URL.Path {
		case "/sdk-config/plan":
			payload, err := io.ReadAll(request.Body)
			// Exact plan echo requires the complete request body before local publication proceeds.
			if err != nil {
				t.Fatalf("read plan: %v", err)
			}
			writeUnifiedInitGenerationRepairPlan(t, writer, payload)
		case "/sdk-config/apply":
			(*applyCalls)++
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-sdk","app_family_id":"family-1","app_id":"app-1"}`))
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
}

// assertUnifiedInitLocalPathsAbsent verifies config and receipt rollback together for pre-apply failure tests.
func assertUnifiedInitLocalPathsAbsent(t *testing.T, configPath, receiptPath string) {
	t.Helper()
	// Both paths must return to definite absence because neither existed before the test lifecycle.
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config rollback state: %v", err)
	}
	if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt rollback state: %v", err)
	}
}

// newUnifiedInitApplyFailureServer returns a bounded plan-success/apply-failure fixture for local publication tests.
func newUnifiedInitApplyFailureServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Only the expected plan and apply boundaries are admitted by this fixture.
		switch request.URL.Path {
		case "/sdk-config/plan":
			payload, err := io.ReadAll(request.Body)
			// A complete plan body is required to echo its config key and source hash faithfully.
			if err != nil {
				t.Fatalf("read plan: %v", err)
			}
			writeUnifiedInitGenerationRepairPlan(t, writer, payload)
		case "/sdk-config/apply":
			writer.WriteHeader(status)
			_, _ = writer.Write([]byte(body))
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
}

// TestUnifiedInitPinRepairResolutionFailureLeavesNoConfig proves an unresolvable exact snapshot cannot publish the candidate.
func TestUnifiedInitPinRepairResolutionFailureLeavesNoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "support-sdk.yaml")
	planCalls := 0
	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Repair may read workspace identity after the typed plan failure but must not mutate without one exact match.
		if request.URL.Path == "/engine/graphql" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":{"workspaceServicePage":{"data":[],"total":0}}}`))
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

// TestUnifiedInitCreateCollisionStopsBeforeWorkspaceLifecycle proves init cannot enable services before telling the user to use extend.
func TestUnifiedInitCreateCollisionStopsBeforeWorkspaceLifecycle(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	path := filepath.Join(directory, "support-sdk.yaml")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	remoteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		remoteCalls++
		t.Fatalf("create collision reached remote path %s", request.URL.Path)
	}))
	defer server.Close()
	oldEngineURL, oldAPIKey := EngineURL, APIKey
	EngineURL, APIKey = server.URL, "test-key"
	t.Cleanup(func() { EngineURL, APIKey = oldEngineURL, oldAPIKey })
	err := runUnifiedInitLifecycle(&cobra.Command{}, unifiedInitModeSDK, unifiedInitFailureTestRequest(path))
	// Existing local ownership is decisive before client construction, workspace planning, confirmation, or apply.
	if err == nil || remoteCalls != 0 || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error=%v remoteCalls=%d", err, remoteCalls)
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
