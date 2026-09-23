package cmd

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// workflowReleaseFixture models Registry's exact JSON and digest contract without invoking a provider.
func workflowReleaseFixture(name, operation, version string) api.WorkflowRelease {
	raw := fmt.Sprintf(`{"schema_version":1,"slug":%q,"version":"1.0.0","name":%q,"description":"Fixture","category":"Support","requirements":[],"services":{"@example/issues":{"service_id":"11111111-1111-4111-8111-111111111111","service_version_id":"22222222-2222-4222-8222-222222222222","version":%q,"operations":[%q]}},"unified_operations":{%q:{"input":{"type":"object"},"bindings":{"issue":{"service":"@example/issues","operation":%q}}}}}`, name, name, version, operation, "issues."+name, operation)
	return api.WorkflowRelease{ID: uuid.NewString(), Hash: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(raw))), Template: raw}
}

// TestWorkflowMultipleReleasesUseSharedScaffold exercises real app validation for SDK and MCP composition.
func TestWorkflowMultipleReleasesUseSharedScaffold(t *testing.T) {
	for _, kind := range []configfile.ConfigKind{configfile.KindSDK, configfile.KindMCP} {
		request := scaffoldRequest{kind: kind, name: "support", version: "1.0.0", language: "typescript", description: "Support workflows", bucket: "default", bucketSet: true}
		for _, release := range []api.WorkflowRelease{workflowReleaseFixture("create", "createIssue", "v1"), workflowReleaseFixture("read", "getIssue", "v1")} {
			// Every selected release must compose before the shared scaffold can write a config.
			if err := mergeWorkflowRelease(&request, release); err != nil {
				t.Fatal(err)
			}
		}
		raw, _, err := newScaffoldData(request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
		// Composition must pass the shared app parser before its resulting scope is asserted.
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := configfile.Parse(raw, "workflow.yaml")
		// Composition must pass the shared app parser before its resulting scope is asserted.
		if err != nil {
			t.Fatal(err)
		}
		config := parsed.SDK
		// Both adapters retain identical composition and provenance while sharing one provider selection.
		if kind == configfile.KindMCP {
			config = parsed.MCP
		}
		// Both app adapters must share dependencies without losing logical methods or provenance.
		if len(config.Services) != 1 || len(config.UnifiedOperations) != 2 || len(config.WorkflowSources) != 2 {
			t.Fatalf("incomplete composition: %s", raw)
		}
	}
}

// TestWorkflowCompositionRejectsConflictAndTamper prevents partial workspace mutation for incompatible selections.
func TestWorkflowCompositionRejectsConflictAndTamper(t *testing.T) {
	first := workflowReleaseFixture("create", "createIssue", "v1")
	request := scaffoldRequest{}
	// An admitted baseline is required before conflicting extensions can be tested.
	if err := mergeWorkflowRelease(&request, first); err != nil {
		t.Fatal(err)
	}
	// Repeating the same immutable release must merge into the same app state.
	if err := mergeWorkflowRelease(&request, first); err != nil {
		t.Fatal(err)
	}
	config := &configfile.AppConfig{Services: map[string]configfile.AppService{}}
	// Provenance must pass through the same merger as ordinary app selections.
	if _, err := mergeAppSelections(config, request); err != nil {
		t.Fatal(err)
	}
	// Reinstalling an immutable release must not duplicate provenance.
	if len(config.WorkflowSources) != 1 {
		t.Fatal("repeat duplicated provenance")
	}
	// Adding a method cannot silently retarget the app to another provider version.
	if err := mergeWorkflowRelease(&request, workflowReleaseFixture("read", "getIssue", "v2")); err == nil {
		t.Fatal("version conflict accepted")
	}
	// An existing method cannot acquire different behavior through another workflow.
	if err := mergeWorkflowRelease(&request, workflowReleaseFixture("create", "deleteIssue", "v1")); err == nil {
		t.Fatal("method conflict accepted")
	}
	first.Template = strings.Replace(first.Template, "createIssue", "deleteIssue", 1)
	// Changing authored content without its digest must fail before workspace mutation.
	if _, err := decodeWorkflowRelease(first); err == nil {
		t.Fatal("tampered release accepted")
	}
}

// TestWorkflowInitAcceptsSelectionWithoutPhysicalFlags keeps the user-facing command on the existing init grammar.
func TestWorkflowInitAcceptsSelectionWithoutPhysicalFlags(t *testing.T) {
	id := uuid.NewString()
	executeUnifiedInitForTest(t, func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error {
		// Resolution is deferred to the lifecycle, but the complete selected set must survive flag parsing.
		if len(request.workflowIDs) != 1 || request.workflowIDs[0] != id {
			t.Fatalf("lost workflow flags: %v", request.workflowIDs)
		}
		return nil
	}, "support", "--sdk", "--workflow", id)
}

// TestWorkflowExactSelection rejects equal-length substitutions as well as incomplete and duplicated responses.
func TestWorkflowExactSelection(t *testing.T) {
	for _, releases := range [][]api.WorkflowRelease{{{ID: "a"}, {ID: "c"}}, {{ID: "a"}}, {{ID: "a"}, {ID: "a"}}} {
		// No malformed result may reach service activation even when its count looks correct.
		if err := validateWorkflowResponse([]string{"a", "b"}, releases); err == nil {
			t.Fatal("nonexact response accepted")
		}
	}
	// Order is presentation only; an exact set remains valid in either order.
	if err := validateWorkflowResponse([]string{"a", "b"}, []api.WorkflowRelease{{ID: "b"}, {ID: "a"}}); err != nil {
		t.Fatal(err)
	}
}

// TestWorkflowAliasRewrite preserves dataflow step names while adapting canonical provider references to workspace aliases.
func TestWorkflowAliasRewrite(t *testing.T) {
	request := scaffoldRequest{}
	// The published fixture must remain valid before testing its routing adaptation.
	if err := mergeWorkflowRelease(&request, workflowReleaseFixture("create", "createIssue", "v1")); err != nil {
		t.Fatal(err)
	}
	rewritten := rewriteSDKInitUnifiedServices(request.unifiedOperations, map[string]string{"@example/issues": "issues"})
	// A copied binding changes routing only, retaining the original template and execution-step identity.
	if rewritten["issues.create"].Bindings["issue"].Service != "issues" || request.unifiedOperations["issues.create"].Bindings["issue"].Service != "@example/issues" {
		t.Fatal("routing rewrite changed source or lost step identity")
	}
}

// TestWorkflowImmutablePins rejects reused labels and preserves exact dependency UUIDs in the workspace plan.
func TestWorkflowImmutablePins(t *testing.T) {
	pins := map[string]workflowServicePin{"@example/issues": {"service", "version", "v1"}}
	services := []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "issues", serviceID: "service", requestedRefs: []string{"@example/issues"}}, version: "v1"}}
	// Alias resolution must preserve the published immutable service and version identities.
	if err := bindWorkflowPins(pins, services); err != nil {
		t.Fatal(err)
	}
	config := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{"issues": {Versions: []configfile.WorkspaceServiceVersion{{Version: "v1"}}}}}
	// The normal desired-state document must carry the exact pin into Engine planning.
	if err := pinWorkflowWorkspaceVersions(config, services); err != nil {
		t.Fatal(err)
	}
	// A lost pin would allow a later reused display label to change execution scope.
	if config.Services["issues"].Versions[0].ServiceVersionID != "version" {
		t.Fatal("immutable pin lost")
	}
	config.Services["issues"].Versions[0].ServiceVersionID = "reused-label"
	// Existing membership under another immutable version must fail before workspace apply.
	if err := pinWorkflowWorkspaceVersions(config, services); err == nil {
		t.Fatal("reused version label accepted")
	}
	services[0].target.serviceID = "other-provider"
	// Canonical naming cannot authorize substitution of another provider identity.
	if err := bindWorkflowPins(pins, services); err == nil {
		t.Fatal("substituted provider accepted")
	}
}
