package cmd

import (
	"bytes"
	"encoding/json"
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

// TestSDKInitComposesWorkspaceAndAppReceipts exercises the production command through both existing plan/apply boundaries.
func TestSDKInitComposesWorkspaceAndAppReceipts(t *testing.T) {
	dir := t.TempDir()
	server, calls := newSDKInitLifecycleServer(t)
	defer server.Close()
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })

	output := runCommandInDirOutput(t, dir, server.URL, []string{
		"sdk", "init", "support-sdk", "--service", "linear", "--operation", "linear=issueUpdate", "--no-input",
	})
	workspace, err := configfile.ParseFile(filepath.Join(dir, ".fused", "workspace.yaml"))
	if err != nil {
		t.Fatalf("parse workspace config: %v", err)
	}
	sdk, err := configfile.ParseFile(filepath.Join(dir, ".fused", "sdks", "support-sdk.yaml"))
	if err != nil {
		t.Fatalf("parse SDK config: %v", err)
	}
	if !configWorkspaceServiceHasVersion(workspace.Workspace.Services["linear"], "v1") || sdk.SDK.Services["linear"].Version != "v1" {
		t.Fatalf("resolved version did not reach both configs: workspace=%#v sdk=%#v", workspace.Workspace.Services, sdk.SDK.Services)
	}
	workspaceReceipt := readReceipt(t, filepath.Join(dir, ".fused", ".state", "workspace.plan.json"))
	sdkReceipt := readReceipt(t, filepath.Join(dir, ".fused", ".state", "sdk.support-sdk.1.0.0.plan.json"))
	if workspaceReceipt.PlanID != "plan-workspace" || sdkReceipt.PlanID != "plan-sdk" {
		t.Fatalf("distinct receipts = %#v / %#v", workspaceReceipt, sdkReceipt)
	}
	wantCalls := []string{"workspace-plan", "workspace-apply", "sdk-plan", "sdk-apply"}
	if strings.Join(*calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("lifecycle order = %#v; want %#v", *calls, wantCalls)
	}
	if !strings.Contains(output, "Successfully applied workspace config") || !strings.Contains(output, "Successfully applied SDK support-sdk") {
		t.Fatalf("missing composed apply output: %q", output)
	}
}

// TestSDKInitPromptsOnceForCombinedIntent proves Registry confirmation is folded into one workspace-and-app review.
func TestSDKInitPromptsOnceForCombinedIntent(t *testing.T) {
	dir := t.TempDir()
	server, _ := newSDKInitLifecycleServer(t)
	defer server.Close()
	oldNoInput, oldConfirm := NoInput, confirmSDKInit
	NoInput = false
	t.Setenv("CI", "")
	prompts := 0
	confirmSDKInit = func(message string) (bool, error) {
		prompts++
		// Shared Cobra slice flags can retain earlier test selections, but the combined authorization wording must remain singular.
		if !strings.Contains(message, "linear v1 isn't in your workspace yet — enable it and create support-sdk") {
			t.Fatalf("combined prompt = %q", message)
		}
		return true, nil
	}
	t.Cleanup(func() {
		NoInput, confirmSDKInit = oldNoInput, oldConfirm
	})

	runCommandInDir(t, dir, server.URL, []string{
		"sdk", "init", "support-sdk", "--service", "linear", "--operation", "linear=issueUpdate",
	})
	if prompts != 1 {
		t.Fatalf("combined prompt count = %d; want 1", prompts)
	}
}

// TestSDKInitSelectsMissingOperationsInteractively proves the concise service-only command discovers scope before its combined review.
func TestSDKInitSelectsMissingOperationsInteractively(t *testing.T) {
	server, _ := newSDKInitLifecycleServer(t)
	defer server.Close()
	oldNoInput, oldInput := NoInput, sdkInput
	NoInput = false
	t.Setenv("CI", "")
	sdkInput = strings.NewReader("1\n")
	t.Cleanup(func() {
		NoInput, sdkInput = oldNoInput, oldInput
	})
	restoreSelector := stubSDKOperationSelectionRunner(t, func(_ io.Reader, _ io.Writer, serviceName, serviceVersion string, endpoints []api.Integration) (sdkOperationSelection, error) {
		if serviceName != "linear" || serviceVersion != "v1" || len(endpoints) != 1 {
			t.Fatalf("selector input = %s %s %#v", serviceName, serviceVersion, endpoints)
		}
		// Choosing the narrower path preserves exact Registry operation IDs in the SDK config.
		return sdkOperationSelection{operations: []string{endpoints[0].Name}}, nil
	})
	t.Cleanup(restoreSelector)

	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	request, err := completeSDKInitOperationSelections(command, api.NewClient(server.URL, "test-key"), scaffoldRequest{name: "support-sdk"}, []sdkInitResolvedService{{
		target: workspaceServiceAddTarget{slug: "linear", serviceID: "00000000-0000-4000-8000-000000000001"}, version: "v1",
	}})
	if err != nil {
		t.Fatalf("select missing SDK operations: %v", err)
	}
	if len(request.operations) != 1 || request.operations[0].operation != "issueUpdate" {
		t.Fatalf("selected operations = %#v; want issueUpdate", request.operations)
	}
	// The combined review must name the operation selected through the shared prompt.
	message := sdkInitConfirmationMessage(request, []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "linear"}, version: "v1"}}, true)
	if !strings.Contains(message, "create support-sdk with issueUpdate?") {
		t.Fatalf("combined prompt = %q", message)
	}
}

// TestSDKInitDefaultsAnEmptyInteractiveSelectionToAllOperations keeps the fastest path free of operation-ID knowledge.
func TestSDKInitDefaultsAnEmptyInteractiveSelectionToAllOperations(t *testing.T) {
	server, _ := newSDKInitLifecycleServer(t)
	defer server.Close()
	oldNoInput, oldInput := NoInput, sdkInput
	NoInput = false
	t.Setenv("CI", "")
	sdkInput = strings.NewReader("\n")
	t.Cleanup(func() {
		NoInput, sdkInput = oldNoInput, oldInput
	})
	restoreSelector := stubSDKOperationSelectionRunner(t, func(_ io.Reader, _ io.Writer, _ string, _ string, endpoints []api.Integration) (sdkOperationSelection, error) {
		// Enter on the visible default returns select_all while retaining fetched IDs for operation-add compatibility.
		return sdkOperationSelection{operations: operationNames(endpoints), selectAll: true}, nil
	})
	t.Cleanup(restoreSelector)

	command := &cobra.Command{}
	request, err := completeSDKInitOperationSelections(command, api.NewClient(server.URL, "test-key"), scaffoldRequest{name: "support-sdk"}, []sdkInitResolvedService{{
		target: workspaceServiceAddTarget{slug: "linear", serviceID: "00000000-0000-4000-8000-000000000001"}, version: "v1",
	}})
	if err != nil {
		t.Fatalf("default missing SDK operations: %v", err)
	}
	if len(request.selectAll) != 1 || request.selectAll[0] != "linear" || len(request.operations) != 0 {
		t.Fatalf("service selection = %#v / %#v; want select_all without an explicit list", request.selectAll, request.operations)
	}
	// The review describes the complete selected surface without requiring an operation ID.
	message := sdkInitConfirmationMessage(request, []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "linear"}, version: "v1"}}, true)
	if !strings.Contains(message, "create support-sdk with all operations for the selected services?") {
		t.Fatalf("combined prompt = %q", message)
	}
}

// TestSDKInitNoInputRequiresOperationScope keeps unattended capability grants explicit and fails before either config is written.
func TestSDKInitNoInputRequiresOperationScope(t *testing.T) {
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })

	_, err := completeSDKInitOperationSelections(&cobra.Command{}, nil, scaffoldRequest{name: "support-sdk"}, []sdkInitResolvedService{{
		target: workspaceServiceAddTarget{slug: "linear"}, version: "v1",
	}})
	if err == nil || !strings.Contains(err.Error(), "--no-input requires --operation 'linear=<operationId>' or --select-all 'linear'") {
		t.Fatalf("missing explicit operation remediation: %v", err)
	}
}

// TestSDKInitInteractiveExtensionInfersMinorSuccessor keeps one file while assigning a deterministic successor before planning.
func TestSDKInitInteractiveExtensionInfersMinorSuccessor(t *testing.T) {
	path := writeSDKInitExtensionFixture(t)
	server := newSDKInitAppReferenceServer(t, "1.1.0", false)
	defer server.Close()
	oldNoInput := NoInput
	NoInput = false
	t.Setenv("CI", "")
	t.Cleanup(func() { NoInput = oldNoInput })
	request := sdkInitExtensionRequest(path)
	resolved, err := completeSDKInitVersionExtension(api.NewClient(server.URL, "test-key"), request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatalf("infer interactive version extension: %v", err)
	}
	// Inference changes only the pending request; the original file remains untouched until the reviewed commit boundary.
	data, readErr := os.ReadFile(path)
	if readErr != nil || resolved.version != "1.1.0" || !resolved.versionSet || !strings.Contains(string(data), "version: 1.0.0") {
		t.Fatalf("resolved=%#v file=%q readErr=%v", resolved, data, readErr)
	}
	message := sdkInitConfirmationMessage(resolved, []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "linear"}, version: "v1"}}, false)
	// The final lifecycle confirmation must present successor identity and expanded operation together.
	if !strings.Contains(message, "Extend support-sdk version 1.1.0 with issueUpdate") {
		t.Fatalf("successor confirmation=%q", message)
	}
}

// TestSDKInitNoInputExtensionInfersMinorSuccessor proves automation uses the same deterministic successor as terminals.
func TestSDKInitNoInputExtensionInfersMinorSuccessor(t *testing.T) {
	path := writeSDKInitExtensionFixture(t)
	server := newSDKInitAppReferenceServer(t, "1.1.0", false)
	defer server.Close()
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })
	resolved, err := completeSDKInitVersionExtension(api.NewClient(server.URL, "test-key"), sdkInitExtensionRequest(path), noOpScaffoldRequirements, defaultTestScaffoldBucket)
	// Non-interactive behavior cannot depend on terminal state or a prompt implementation.
	if err != nil || resolved.version != "1.1.0" || !resolved.versionSet {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
}

// TestSDKInitExplicitCurrentVersionRejectsChangedAppliedScope proves a repeated immutable identity fails before the config write.
func TestSDKInitExplicitCurrentVersionRejectsChangedAppliedScope(t *testing.T) {
	path := writeSDKInitExtensionFixture(t)
	request := sdkInitExtensionRequest(path)
	request.version, request.versionSet = "1.0.0", true
	_, err := completeSDKInitVersionExtension(nil, request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	// The error must name the applied identity and exact successor flag without rewriting the source file.
	data, readErr := os.ReadFile(path)
	if err == nil || !strings.Contains(err.Error(), "support-sdk@1.0.0 already identifies the current config") || !strings.Contains(err.Error(), "--version <new-version>") || readErr != nil || !strings.Contains(string(data), "operations: [issueGet]") {
		t.Fatalf("error=%v file=%q readErr=%v", err, data, readErr)
	}
}

// TestSDKInitExplicitSuccessorCollisionFailsBeforeWrite proves a visible immutable target is rejected locally.
func TestSDKInitExplicitSuccessorCollisionFailsBeforeWrite(t *testing.T) {
	path := writeSDKInitExtensionFixture(t)
	server := newSDKInitAppReferenceServer(t, "1.1.0", true)
	defer server.Close()
	request := sdkInitExtensionRequest(path)
	request.version, request.versionSet = "1.1.0", true
	_, err := completeSDKInitVersionExtension(api.NewClient(server.URL, "test-key"), request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	data, readErr := os.ReadFile(path)
	// Collision checks must run before any candidate replaces the authored current version.
	if err == nil || !strings.Contains(err.Error(), "support-sdk@1.1.0 already exists") || readErr != nil || !strings.Contains(string(data), "version: 1.0.0") {
		t.Fatalf("error=%v file=%q readErr=%v", err, data, readErr)
	}
}

// TestSDKInitIdempotentExtensionKeepsAppliedVersion avoids prompting when the requested additions are already present.
func TestSDKInitIdempotentExtensionKeepsAppliedVersion(t *testing.T) {
	path := writeSDKInitExtensionFixture(t)
	request := sdkInitExtensionRequest(path)
	request.operations = []scaffoldOperation{{service: "linear", operation: "issueGet"}}
	resolved, err := completeSDKInitVersionExtension(nil, request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatalf("complete idempotent extension: %v", err)
	}
	// A nil client proves the no-change path never performs an unnecessary remote app lookup.
	if resolved.versionSet || resolved.version != "" {
		t.Fatalf("idempotent extension changed version intent: %#v", resolved)
	}
}

// TestSDKInitUnpublishedDraftExtensionAlsoInfersSuccessor avoids treating visibility-ambiguous not-found as mutation authority.
func TestSDKInitUnpublishedDraftExtensionAlsoInfersSuccessor(t *testing.T) {
	path := writeSDKInitExtensionFixture(t)
	server := newSDKInitAppReferenceServer(t, "1.1.0", false)
	defer server.Close()
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })
	resolved, err := completeSDKInitVersionExtension(api.NewClient(server.URL, "test-key"), sdkInitExtensionRequest(path), noOpScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatalf("complete unpublished draft extension: %v", err)
	}
	// Absence and inaccessibility share one response, so every real change advances the authored version uniformly.
	if !resolved.versionSet || resolved.version != "1.1.0" {
		t.Fatalf("draft extension did not infer successor: %#v", resolved)
	}
}

// writeSDKInitExtensionFixture creates one valid private SDK config for in-place lifecycle tests.
func writeSDKInitExtensionFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "support-sdk.yaml")
	data := `apiVersion: fused/v1
kind: sdk
name: support-sdk
version: 1.0.0
language: typescript
bucket: default
services:
  linear:
    version: v1
    operations: [issueGet]
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// sdkInitExtensionRequest returns one changed selection without preselecting a successor version.
func sdkInitExtensionRequest(path string) scaffoldRequest {
	return scaffoldRequest{
		kind: configfile.KindSDK, name: "support-sdk", path: path, extend: true,
		services:   []scaffoldService{{name: "linear", version: "v1"}},
		operations: []scaffoldOperation{{service: "linear", operation: "issueUpdate"}},
	}
}

// newSDKInitAppReferenceServer returns an exact Engine app-reference result for extension lifecycle tests.
func newSDKInitAppReferenceServer(t *testing.T, expectedVersion string, exists bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode app reference request: %v", err)
		}
		// Collision detection must resolve the exact proposed name, version, and SDK kind before writing.
		if !strings.Contains(body.Query, "ResolveAppReference") || body.Variables["reference"] != "support-sdk" || body.Variables["version"] != expectedVersion || body.Variables["kind"] != "sdk" {
			t.Fatalf("app reference request=%#v", body)
		}
		// Tests can distinguish an existing immutable version from a still-local draft without changing the client contract.
		if !exists {
			_, _ = writer.Write([]byte(`{"errors":[{"message":"not found","extensions":{"code":"FUSED_RESOURCE_NOT_FOUND"}}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"data":{"appReference":{"id":"app-version-1","kind":"sdk"}}}`))
	}))
}

// newSDKInitLifecycleServer returns exact fixtures for Registry resolution, scaffold discovery, and both existing lifecycle commits.
func newSDKInitLifecycleServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	calls := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// REST lifecycle endpoints record only the two distinct plan/apply boundaries.
		switch request.URL.Path {
		case "/health":
			_, _ = writer.Write([]byte(`{"environment":"development"}`))
			return
		case "/workspace/config/plan":
			calls = append(calls, "workspace-plan")
			writeSDKInitPlanResponse(t, writer, request, "plan-workspace")
			return
		case "/workspace/config/apply":
			calls = append(calls, "workspace-apply")
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
			return
		case "/sdk-config/plan":
			calls = append(calls, "sdk-plan")
			writeSDKInitPlanResponse(t, writer, request, "plan-sdk")
			return
		case "/sdk-config/apply":
			calls = append(calls, "sdk-apply")
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-sdk","app_family_id":"family-1","app_id":"app-1"}`))
			return
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}
		// Engine reads remain distinct from Registry catalogue/version resolution.
		if request.URL.Path == "/engine/graphql" {
			writeSDKInitEngineGraphQL(t, writer, body.Query)
			return
		}
		if request.URL.Path == "/graphql" {
			writeSDKInitRegistryGraphQL(t, writer, body.Query)
			return
		}
		t.Fatalf("unexpected SDK init path %s", request.URL.Path)
	}))
	return server, &calls
}

// writeSDKInitPlanResponse echoes the planner's source hash so receipt verification exercises the normal apply helper.
func writeSDKInitPlanResponse(t *testing.T, writer http.ResponseWriter, request *http.Request, planID string) {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatalf("decode plan request: %v", err)
	}
	_, _ = fmt.Fprintf(writer, `{"plan_id":%q,"config_key":%q,"source_hash":%q,"summary":{}}`, planID, body["config_key"], body["source_hash"])
}

// writeSDKInitEngineGraphQL serves workspace visibility, bucket selection, and scaffold requirement reads from one Engine endpoint.
func writeSDKInitEngineGraphQL(t *testing.T, writer http.ResponseWriter, query string) {
	t.Helper()
	switch {
	case strings.Contains(query, "WorkspaceServices"):
		_, _ = writer.Write([]byte(`{"data":{"workspaceServices":[]}}`))
	case strings.Contains(query, "BucketSummaryPage"):
		_, _ = writer.Write([]byte(`{"data":{"bucketSummaryPage":{"total":1,"items":[{"id":"bucket-1","name":"default","is_default":true}]}}}`))
	case strings.Contains(query, "AppScaffoldRequirements"):
		_, _ = writer.Write([]byte(`{"data":{"appScaffoldRequirements":[]}}`))
	default:
		t.Fatalf("unexpected Engine GraphQL query: %s", query)
	}
}

// writeSDKInitRegistryGraphQL serves the exact service candidate and latest immutable version used by both configs.
func writeSDKInitRegistryGraphQL(t *testing.T, writer http.ResponseWriter, query string) {
	t.Helper()
	switch {
	case strings.Contains(query, "ServiceCandidatesByRefs"):
		_, _ = writer.Write([]byte(`{"data":{"serviceCandidatesByRefs":[{"ref":"linear","candidates":[{"id":"00000000-0000-4000-8000-000000000001","name":"Linear","slug":"linear","is_owner":true,"is_public":true}]}]}}`))
	case strings.Contains(query, "GetServiceLatestVersion"):
		_, _ = writer.Write([]byte(`{"data":{"service":{"service_versions":[{"name":"v1"}]}}}`))
	// The shared selector consumes the same Registry operation projection in operation-add and init workflows.
	case strings.Contains(query, "SearchEndpoints"):
		_, _ = writer.Write([]byte(`{"data":{"searchEndpoints":[{"id":"endpoint-1","name":"issueUpdate","method":"PATCH","path":"/issues/{id}","description":"","service_id":"00000000-0000-4000-8000-000000000001"}]}}`))
	default:
		t.Fatalf("unexpected Registry GraphQL query: %s", query)
	}
}

// TestSDKInitAcceptsOmittedServiceVersionWhileMCPRequiresOne proves version defaulting is scoped to the composite SDK workflow.
func TestSDKInitAcceptsOmittedServiceVersionWhileMCPRequiresOne(t *testing.T) {
	sdkOpts := &scaffoldOptions{services: []string{"linear"}, version: defaultScaffoldVersion, language: defaultScaffoldLanguage}
	sdkCmd := newScaffoldCommandWithDependencies(configfile.KindSDK, nil, nil)
	if _, err := buildScaffoldRequest(sdkCmd, configfile.KindSDK, []string{"support-sdk"}, sdkOpts); err != nil {
		t.Fatalf("SDK init rejected an omitted provider version: %v", err)
	}

	mcpOpts := &scaffoldOptions{services: []string{"linear"}, version: defaultScaffoldVersion}
	mcpCmd := newScaffoldCommandWithDependencies(configfile.KindMCP, nil, nil)
	if _, err := buildScaffoldRequest(mcpCmd, configfile.KindMCP, []string{"support-mcp"}, mcpOpts); err == nil || !strings.Contains(err.Error(), "requires a version") {
		t.Fatalf("MCP init should retain exact-version validation, got %v", err)
	}
}

// TestResolveSDKInitServiceVersionPrefersExplicitThenWorkspaceDefault verifies both deterministic version paths avoid Registry lookup.
func TestResolveSDKInitServiceVersionPrefersExplicitThenWorkspaceDefault(t *testing.T) {
	target := workspaceServiceAddTarget{slug: "linear", requestedRefs: []string{"linear"}, version: "v2"}
	version, err := resolveSDKInitServiceVersion([]scaffoldService{{name: "linear", version: "v1"}}, target, nil)
	if err != nil || version != "v1" {
		t.Fatalf("explicit version = %q, %v; want v1", version, err)
	}
	version, err = resolveSDKInitServiceVersion([]scaffoldService{{name: "linear"}}, target, nil)
	if err != nil || version != "v2" {
		t.Fatalf("workspace default = %q, %v; want v2", version, err)
	}
}

// TestResolveSDKInitServiceVersionRejectsAliasConflicts keeps deduplication from weakening immutable version selection.
func TestResolveSDKInitServiceVersionRejectsAliasConflicts(t *testing.T) {
	target := workspaceServiceAddTarget{slug: "@acme/linear", requestedRefs: []string{"linear", "@acme/linear"}}
	_, err := resolveSDKInitServiceVersion([]scaffoldService{
		{name: "linear", version: "v1"},
		{name: "@acme/linear", version: "v2"},
	}, target, nil)
	if err == nil || !strings.Contains(err.Error(), "conflicting versions") {
		t.Fatalf("expected immutable version conflict, got %v", err)
	}
}

// TestSDKInitWorkspaceAdditionsIncludesOnlyMissingVersions proves enabled execution authority skips the workspace receipt boundary.
func TestSDKInitWorkspaceAdditionsIncludesOnlyMissingVersions(t *testing.T) {
	services := []sdkInitResolvedService{
		{target: workspaceServiceAddTarget{slug: "linear", serviceID: "service-linear", enabledVersions: []string{"v1"}}, version: "v1"},
		{target: workspaceServiceAddTarget{slug: "gmail", serviceID: "service-gmail", requestedRefs: []string{"mail"}}, version: "v2"},
	}
	additions := sdkInitWorkspaceAdditions(services)
	if len(additions) != 1 || additions[0].serviceName != "gmail" || additions[0].version != "v2" {
		t.Fatalf("workspace additions = %#v; want only gmail v2", additions)
	}
}

// TestSDKInitConfirmationMessageCombinesTheCommonCase locks the single-review wording for one missing service and operation.
func TestSDKInitConfirmationMessageCombinesTheCommonCase(t *testing.T) {
	request := scaffoldRequest{
		name:       "support-sdk",
		operations: []scaffoldOperation{{service: "linear", operation: "issueUpdate"}},
	}
	services := []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "linear"}, version: "v1"}}
	got := sdkInitConfirmationMessage(request, services, true)
	want := "linear v1 isn't in your workspace yet — enable it and create support-sdk with issueUpdate?"
	if got != want {
		t.Fatalf("confirmation = %q; want %q", got, want)
	}
}

// TestRewriteSDKInitSelectionsUsesCanonicalServiceKeys verifies operations and select-all follow the resolved Registry identity.
func TestRewriteSDKInitSelectionsUsesCanonicalServiceKeys(t *testing.T) {
	aliases := map[string]string{"linear": "@acme/linear"}
	operations := rewriteSDKInitOperations([]scaffoldOperation{{service: "linear", operation: "issueUpdate"}}, aliases)
	selectAll := rewriteSDKInitNames([]string{"linear"}, aliases)
	if operations[0].service != "@acme/linear" || selectAll[0] != "@acme/linear" {
		t.Fatalf("canonical selections = %#v / %#v", operations, selectAll)
	}
}
