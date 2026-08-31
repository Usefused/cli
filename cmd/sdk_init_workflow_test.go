package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
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
