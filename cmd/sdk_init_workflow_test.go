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

// TestSDKInitWorkspaceDraftSeedsRemoteStateWithoutLocalFile proves additive onboarding cannot deactivate an existing Engine service.
func TestSDKInitWorkspaceDraftSeedsRemoteStateWithoutLocalFile(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Only the in-memory remote snapshot and final workspace plan are expected before confirmation.
		if request.URL.Path == "/workspace/config/plan" {
			var body struct {
				Config map[string]any `json:"config"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode additive workspace plan: %v", err)
			}
			services, _ := body.Config["services"].(map[string]any)
			// Both entries in the planned desired state prove the new service cannot imply removal of the existing one.
			if _, exists := services["@provider/gmail"]; !exists {
				t.Fatalf("workspace plan dropped existing remote service: %#v", body.Config)
			}
			if _, exists := services["linear"]; !exists {
				t.Fatalf("workspace plan omitted requested service: %#v", body.Config)
			}
			_, _ = writer.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"source","summary":{}}`))
			return
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode additive workspace GraphQL: %v", err)
		}
		// Engine inventory and Registry visibility are the minimum authority needed to reconstruct safe additive intent.
		switch {
		case request.URL.Path == "/engine/graphql" && strings.Contains(body.Query, "WorkspaceServices"):
			_, _ = writer.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"service_id":"service-gmail","service_slug":"@provider/gmail","service_name":"Gmail","enabled_versions":[{"service_version_id":"gmail-v1","version":"v1","status":"public"}]}],"total":1}}}`))
		case request.URL.Path == "/engine/graphql" && strings.Contains(body.Query, "WorkspaceConnectionProfiles"):
			_, _ = writer.Write([]byte(`{"data":{"workspaceConnectionProfiles":[]}}`))
		case request.URL.Path == "/graphql" && strings.Contains(body.Query, "ServiceVisibilities"):
			_, _ = writer.Write([]byte(`{"data":{"servicesByIds":[{"id":"service-gmail","slug":"gmail","provider":{"handle":"provider"},"is_owner":false,"is_public":true}]}}`))
		default:
			t.Fatalf("unexpected additive workspace request: %s %s", request.URL.Path, body.Query)
		}
	}))
	defer server.Close()
	services := []sdkInitResolvedService{{
		target: workspaceServiceAddTarget{slug: "linear", serviceID: "service-linear", requestedRefs: []string{"linear"}}, version: "v1",
	}}
	draft, err := planSDKInitWorkspace(api.NewClient(server.URL, "test-key"), services)
	if err != nil {
		t.Fatalf("plan additive workspace: %v", err)
	}
	// The draft remains in memory; combined confirmation still precedes both the file write and remote apply.
	if draft == nil || len(draft.config.Services) != 2 {
		t.Fatalf("additive workspace draft = %#v", draft)
	}
	if _, err := os.Stat(filepath.Join(directory, ".fused", "workspace.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace draft published before confirmation: %v", err)
	}
}

// TestSDKInitWorkspaceDraftPreservesRemoteStateMissingFromStaleLocalFile proves additive init never interprets a partial file as removal intent.
func TestSDKInitWorkspaceDraftPreservesRemoteStateMissingFromStaleLocalFile(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	workspacePath := filepath.Join(directory, ".fused", "workspace.yaml")
	if err := os.MkdirAll(filepath.Dir(workspacePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacePath, []byte("apiVersion: fused/v1\nkind: workspace\nservices:\n  slack:\n    service_id: service-slack\n    versions:\n      - version: v2\n        service_version_id: slack-v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// The final plan must retain local-only Slack, live-only Gmail, and the requested Linear addition.
		if request.URL.Path == "/workspace/config/plan" {
			var body struct {
				Config map[string]any `json:"config"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode stale additive workspace plan: %v", err)
			}
			services, _ := body.Config["services"].(map[string]any)
			// All three identities prove the union preserved both sides before adding the requested service.
			if len(services) != 3 || services["gmail"] == nil || services["slack"] == nil || services["linear"] == nil {
				t.Fatalf("workspace plan is not additive: %#v", body.Config)
			}
			_, _ = writer.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"source","summary":{}}`))
			return
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode stale additive workspace GraphQL: %v", err)
		}
		// The remote snapshot uses one live service absent from the stale file.
		switch {
		case request.URL.Path == "/engine/graphql" && strings.Contains(body.Query, "WorkspaceServices"):
			_, _ = writer.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"service_id":"service-gmail","service_slug":"gmail","service_name":"Gmail","enabled_versions":[{"service_version_id":"gmail-v1","version":"v1","status":"public"}]}],"total":1}}}`))
		case request.URL.Path == "/engine/graphql" && strings.Contains(body.Query, "WorkspaceConnectionProfiles"):
			_, _ = writer.Write([]byte(`{"data":{"workspaceConnectionProfiles":[]}}`))
		case request.URL.Path == "/graphql" && strings.Contains(body.Query, "ServiceVisibilities"):
			_, _ = writer.Write([]byte(`{"data":{"servicesByIds":[{"id":"service-gmail","slug":"gmail","is_owner":false,"is_public":true}]}}`))
		default:
			t.Fatalf("unexpected stale additive workspace request: %s %s", request.URL.Path, body.Query)
		}
	}))
	defer server.Close()
	services := []sdkInitResolvedService{{
		target: workspaceServiceAddTarget{slug: "linear", serviceID: "service-linear", requestedRefs: []string{"linear"}}, version: "v1",
	}}
	draft, err := planSDKInitWorkspace(api.NewClient(server.URL, "test-key"), services)
	// Planning remains in memory while still demonstrating that no existing local or remote membership is dropped.
	if err != nil || draft == nil || len(draft.config.Services) != 3 {
		t.Fatalf("draft=%#v err=%v", draft, err)
	}
}

// TestCompleteSDKInitCreateBucketFailsBeforeWorkspacePlanning locks the early credential-container preflight used by composed init.
func TestCompleteSDKInitCreateBucketFailsBeforeWorkspacePlanning(t *testing.T) {
	request := scaffoldRequest{services: []scaffoldService{{name: "linear", version: "v1"}}}
	resolverCalls := 0
	resolved, err := completeSDKInitCreateBucket(request, func() (string, error) {
		resolverCalls++
		return "", errors.New("no visible bucket is available")
	})
	// A missing bucket returns no partially resolved request and invokes the read-only dependency exactly once.
	if err == nil || resolverCalls != 1 || resolved.services != nil || !strings.Contains(err.Error(), "no visible bucket") {
		t.Fatalf("resolved=%#v resolverCalls=%d err=%v", resolved, resolverCalls, err)
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

// TestHydrateSDKInitExtendServiceReferencesPinsOperationOnlyServices proves extend can validate a new operation without repeating --service.
func TestHydrateSDKInitExtendServiceReferencesPinsOperationOnlyServices(t *testing.T) {
	path := writeSDKInitExtensionFixture(t)
	request := scaffoldRequest{
		kind: configfile.KindSDK, name: "support-sdk", path: path, extend: true,
		operations: []scaffoldOperation{{service: "linear", operation: "issueUpdate"}},
		selectAll:  []string{"linear"},
	}
	resolved, err := hydrateSDKInitExtendServiceReferences(request)
	// Hydration must succeed locally before any Registry resolution is necessary.
	if err != nil {
		t.Fatalf("hydrate operation-only extension: %v", err)
	}
	// Repeated operation/select-all references must produce one exact pin copied from the accepted config.
	if len(resolved.services) != 1 || resolved.services[0].name != "linear" || resolved.services[0].version != "v1" {
		t.Fatalf("hydrated services = %#v; want linear@v1", resolved.services)
	}
}

// TestSDKInitOperationOnlyExtensionRejectsInvalidOperationBeforeMutation proves inherited pins restore the normal Registry validation boundary.
func TestSDKInitOperationOnlyExtensionRejectsInvalidOperationBeforeMutation(t *testing.T) {
	path := writeSDKInitExtensionFixture(t)
	before, err := os.ReadFile(path)
	// The pre-test bytes are the accepted state that any failed extension must preserve exactly.
	if err != nil {
		t.Fatal(err)
	}
	mutationCalls := 0
	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Any lifecycle plan/apply request would prove invalid operation validation happened too late.
		if request.URL.Path != "/engine/graphql" && request.URL.Path != "/graphql" {
			mutationCalls++
			t.Fatalf("invalid operation reached remote lifecycle path %s", request.URL.Path)
		}
		var body struct {
			Query string `json:"query"`
		}
		// Read-only validation requests must still be well-formed GraphQL envelopes.
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode operation-only validation request: %v", err)
		}
		// Workspace-first resolution supplies immutable identity; Registry supplies the bounded operation catalogue.
		switch {
		case request.URL.Path == "/engine/graphql" && strings.Contains(body.Query, "WorkspaceServices"):
			_, _ = writer.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"service_id":"00000000-0000-4000-8000-000000000001","service_slug":"linear","service_name":"Linear","enabled_versions":[{"service_version_id":"version-v1","version":"v1","status":"public"}]}],"total":1}}}`))
		case request.URL.Path == "/graphql" && strings.Contains(body.Query, "ServiceOperations"):
			_, _ = writer.Write([]byte(`{"data":{"serviceOperations":[{"id":"endpoint-1","name":"issueGet","method":"GET","path":"/issues/{id}","description":"","service_id":"00000000-0000-4000-8000-000000000001"}]}}`))
		default:
			t.Fatalf("unexpected operation-only validation request: %s %s", request.URL.Path, body.Query)
		}
	}))
	defer server.Close()
	oldEngineURL, oldAPIKey, oldNoInput := EngineURL, APIKey, NoInput
	EngineURL, APIKey, NoInput = server.URL, "test-key", true
	t.Cleanup(func() { EngineURL, APIKey, NoInput = oldEngineURL, oldAPIKey, oldNoInput })
	request := scaffoldRequest{
		kind: configfile.KindSDK, name: "support-sdk", path: path, extend: true,
		operations: []scaffoldOperation{{service: "linear", operation: "issueMissing"}},
	}
	_, err = prepareSDKInitLifecycle(&cobra.Command{}, request, defaultTestScaffoldBucket)
	after, readErr := os.ReadFile(path)
	// The missing operation must be actionable and leave the accepted config and every remote receipt untouched.
	if err == nil || !strings.Contains(err.Error(), "operation issueMissing is not available for service linear version v1") || readErr != nil || !bytes.Equal(before, after) || mutationCalls != 0 {
		t.Fatalf("error=%v readErr=%v unchanged=%t mutationCalls=%d", err, readErr, bytes.Equal(before, after), mutationCalls)
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
	resolved, err := completeSDKInitVersionExtension(api.NewClient(server.URL, "test-key"), request, defaultTestScaffoldBucket)
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
	resolved, err := completeSDKInitVersionExtension(api.NewClient(server.URL, "test-key"), sdkInitExtensionRequest(path), defaultTestScaffoldBucket)
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
	_, err := completeSDKInitVersionExtension(nil, request, defaultTestScaffoldBucket)
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
	_, err := completeSDKInitVersionExtension(api.NewClient(server.URL, "test-key"), request, defaultTestScaffoldBucket)
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
	resolved, err := completeSDKInitVersionExtension(nil, request, defaultTestScaffoldBucket)
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
	resolved, err := completeSDKInitVersionExtension(api.NewClient(server.URL, "test-key"), sdkInitExtensionRequest(path), defaultTestScaffoldBucket)
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
		_, _ = writer.Write([]byte(`{"data":{"workspaceServicePage":{"data":[],"total":0}}}`))
	case strings.Contains(query, "WorkspaceConnectionProfiles"):
		_, _ = writer.Write([]byte(`{"data":{"workspaceConnectionProfiles":[]}}`))
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
	case strings.Contains(query, "ServiceOperations"):
		_, _ = writer.Write([]byte(`{"data":{"serviceOperations":[{"id":"endpoint-1","name":"issueUpdate","method":"PATCH","path":"/issues/{id}","description":"","service_id":"00000000-0000-4000-8000-000000000001"}]}}`))
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
