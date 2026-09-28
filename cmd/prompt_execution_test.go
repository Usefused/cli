package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// TestPromptExecutionConfigProducesHostedApp verifies describe emits executable App desired state, not SDK output.
func TestPromptExecutionConfigProducesHostedApp(t *testing.T) {
	request := scaffoldRequest{name: "billing", version: "1.0.0", bucket: "main", services: []scaffoldService{{name: "crm", version: "v1"}}, operations: []scaffoldOperation{{service: "crm", operation: "createCustomer"}}}
	digest := "sha256:" + strings.Repeat("a", 64)
	config := promptExecutionConfig(request, digest)
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := configfile.Parse(data, ".fused/executions/billing.yaml")
	// Shared config validation must admit the exact hosted contract describe writes.
	if err != nil || parsed.Kind != configfile.KindExecution || parsed.Execution.BundleDigest != digest || parsed.Execution.Generate == nil || *parsed.Execution.Generate {
		t.Fatalf("hosted config = %#v, %v", parsed, err)
	}
	if got := parsed.Execution.Services["crm"]; len(got.Operations) != 1 || got.Operations[0] != "createCustomer" {
		t.Fatalf("operation scope = %#v", got)
	}
}

// TestDecodePromptExecutionDraftRequiresReviewableSource rejects ambiguous and incomplete model output.
func TestDecodePromptExecutionDraftRequiresReviewableSource(t *testing.T) {
	for _, raw := range []string{`{"clarification":"Which account?","source":""}`, `{"source":"const x = 1"}`, `not json`} {
		// A failed draft must stop before source or workspace changes.
		if _, err := decodePromptExecutionDraft(raw); err == nil {
			t.Fatalf("accepted draft %q", raw)
		}
	}
	source, err := decodePromptExecutionDraft(`{"clarification":"","source":"export default buildExecutionApp({})"}`)
	if err != nil || !strings.Contains(source, "buildExecutionApp") {
		t.Fatalf("source = %q, %v", source, err)
	}
}

// TestPromptExecutionEngineSelectionsRetainsOnlyOperationServices keeps exact compiler scope finite.
func TestPromptExecutionEngineSelectionsRetainsOnlyOperationServices(t *testing.T) {
	request := scaffoldRequest{services: []scaffoldService{{name: "crm", version: "v1"}, {name: "mail", version: "v2"}}, operations: []scaffoldOperation{{service: "crm", operation: "create"}}}
	got := promptExecutionEngineSelections(request)
	if len(got) != 1 || got[0].Service != "crm" || got[0].Version != "v1" || len(got[0].Operations) != 1 || got[0].Operations[0] != "create" {
		t.Fatalf("selections = %#v", got)
	}
}

// TestReadPromptExecutionArtifactsRejectsChangedBundle prevents post-build bytes from entering an applied version.
func TestReadPromptExecutionArtifactsRejectsChangedBundle(t *testing.T) {
	dir := t.TempDir()
	draft := &promptExecutionDraft{bundlePath: filepath.Join(dir, "bundle.js"), manifestPath: filepath.Join(dir, "manifest.json"), digestPath: filepath.Join(dir, "digest.json")}
	for path, data := range map[string]string{draft.bundlePath: "changed", draft.manifestPath: `{}`, draft.digestPath: `{"bundle_digest":"sha256:` + strings.Repeat("a", 64) + `"}`} {
		// Every artifact exists; the digest mismatch alone must reject deployment.
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := readPromptExecutionArtifacts(draft); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("expected bundle mismatch, got %v", err)
	}
}

// TestPromptExecutionDraftSelectionsPinsRegistryIdentity validates the model receives only selected contracts.
func TestPromptExecutionDraftSelectionsPinsRegistryIdentity(t *testing.T) {
	serviceID := uuid.New().String()
	plan := promptInitPlan{primary: scaffoldRequest{operations: []scaffoldOperation{{service: "crm", operation: "create"}}}, resolved: []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "crm", serviceID: serviceID}, version: "v1"}}}
	got, err := promptExecutionDraftSelections(plan)
	if err != nil || len(got) != 1 || got[0] != (api.PromptOperationSelection{Service: "crm", ServiceID: serviceID, Version: "v1", Operation: "create"}) {
		t.Fatalf("draft selections = %#v, %v", got, err)
	}
}

// TestDeployPromptExecutionPlansAppliesAndAttaches verifies one reviewed source becomes one hosted version.
func TestDeployPromptExecutionPlansAppliesAndAttaches(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(".fused", "executions", "billing"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := []byte("globalThis.FusedExecutionApp = {};")
	digest := "sha256:" + fmt.Sprintf("%x", sha256.Sum256(bundle))
	draft := &promptExecutionDraft{
		bundlePath:   filepath.Join(".fused", "executions", "billing", "bundle.js"),
		manifestPath: filepath.Join(".fused", "executions", "billing", "manifest.json"),
		digestPath:   filepath.Join(".fused", "executions", "billing", "digest.json"),
	}
	selected := []api.ExecutionBuildSelection{{Service: "crm", Operation: "create", ServiceID: uuid.New().String(), ServiceVersionID: uuid.New().String(), EndpointID: uuid.New().String()}}
	writeTestPromptExecutionArtifacts(t, draft, bundle, digest, selected)
	appID := uuid.New().String()
	paths := []string{}
	server := newPromptExecutionTestServer(t, appID, &paths)
	defer server.Close()
	client := api.NewClient(server.URL, "fsk_test")
	request := scaffoldRequest{name: "billing", version: "1.0.0", bucket: "main", path: filepath.Join(".fused", "executions", "billing.yaml"), services: []scaffoldService{{name: "crm", version: "v1"}}, operations: []scaffoldOperation{{service: "crm", operation: "create"}}}
	plan := promptInitPlan{primary: request, execution: draft}
	command := &cobra.Command{}
	var output bytes.Buffer
	command.SetOut(&output)
	if err := deployPromptExecution(command, client, plan, selected); err != nil {
		t.Fatal(err)
	}
	// Apply returns the token once; attachment and durable config follow the same exact source hash.
	if strings.Join(paths, ",") != "/execution-config/plan,/execution-config/apply,/apps/"+appID+"/bundle" || !strings.Contains(output.String(), "Execution token (shown once): once") {
		t.Fatalf("routes=%#v output=%q", paths, output.String())
	}
	parsed, err := configfile.ParseFile(request.path)
	if err != nil || parsed.Kind != configfile.KindExecution || parsed.Execution.BundleDigest != digest {
		t.Fatalf("published config=%#v err=%v", parsed, err)
	}
}

// writeTestPromptExecutionArtifacts keeps exact compiler outputs stable for the deployment test.
func writeTestPromptExecutionArtifacts(t *testing.T, draft *promptExecutionDraft, bundle []byte, digest string, selected []api.ExecutionBuildSelection) {
	t.Helper()
	manifest, err := json.Marshal(map[string]any{"schemaVersion": 1, "inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}, "selectedOperations": selected})
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{draft.bundlePath: bundle, draft.manifestPath: manifest, draft.digestPath: []byte(`{"bundle_digest":"` + digest + `"}`)} {
		// Artifact content is fixed before the control-plane request sequence begins.
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// newPromptExecutionTestServer records the three Engine control routes without running a provider call.
func newPromptExecutionTestServer(t *testing.T, appID string, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		*paths = append(*paths, request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		// Each route has one stable place in the plan, apply, and attach sequence.
		switch request.URL.Path {
		case "/execution-config/plan":
			_, _ = writer.Write([]byte(`{"plan_id":"plan-1","summary":{}}`))
		case "/execution-config/apply":
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-1","app_family_id":"family-1","app_id":"` + appID + `","execution_token":"once"}`))
		case "/apps/" + appID + "/bundle":
			_, _ = writer.Write([]byte(`{"status":"attached","app_id":"` + appID + `"}`))
		default:
			t.Errorf("unexpected route %s", request.URL.Path)
		}
	}))
}
