package cmd

import (
	"bytes"
	"encoding/json"
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

// TestPromptExecutionConfigProducesEngineCompiledCart verifies describe sends source and scope without a local bundle.
func TestPromptExecutionConfigProducesEngineCompiledCart(t *testing.T) {
	request := scaffoldRequest{name: "billing", version: "1.0.0", bucket: "main", services: []scaffoldService{{name: "crm", version: "v1"}}, operations: []scaffoldOperation{{service: "crm", operation: "createCustomer"}}}
	source := "export default buildExecutionApp({});"
	config := promptExecutionConfig(request, source)
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := configfile.Parse(data, ".fused/executions/billing.yaml")
	// The shared parser must admit source-only hosted desired state and retain exact scope.
	if err != nil || parsed.Kind != configfile.KindExecution || parsed.Execution.Source != source || parsed.Execution.BundleDigest != "" {
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

// TestPromptExecutionDraftSelectionsPinsRegistryIdentity validates the model receives only selected contracts.
func TestPromptExecutionDraftSelectionsPinsRegistryIdentity(t *testing.T) {
	serviceID := uuid.New().String()
	plan := promptInitPlan{primary: scaffoldRequest{operations: []scaffoldOperation{{service: "crm", operation: "create"}}}, resolved: []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "crm", serviceID: serviceID}, version: "v1"}}}
	got, err := promptExecutionDraftSelections(plan)
	if err != nil || len(got) != 1 || got[0] != (api.PromptOperationSelection{Service: "crm", ServiceID: serviceID, Version: "v1", Operation: "create"}) {
		t.Fatalf("draft selections = %#v, %v", got, err)
	}
}

// TestDeployPromptExecutionSendsCartToEngine verifies plan compiles remotely and apply needs no CLI bundle attachment.
func TestDeployPromptExecutionSendsCartToEngine(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(".fused", "executions"), 0o755); err != nil {
		t.Fatal(err)
	}
	appID := uuid.New().String()
	paths := []string{}
	server := newPromptExecutionTestServer(t, appID, &paths)
	defer server.Close()
	client := api.NewClient(server.URL, "fsk_test")
	request := scaffoldRequest{name: "billing", version: "1.0.0", bucket: "main", path: filepath.Join(".fused", "executions", "billing.yaml"), services: []scaffoldService{{name: "crm", version: "v1"}}, operations: []scaffoldOperation{{service: "crm", operation: "create"}}}
	source := "export default buildExecutionApp({});"
	plan := promptInitPlan{mode: unifiedInitModeExecution, primary: request, execution: &promptExecutionDraft{source: source}}
	command := &cobra.Command{}
	var output bytes.Buffer
	command.SetOut(&output)
	if err := deployPromptExecution(command, client, plan, false); err != nil {
		t.Fatal(err)
	}
	// Engine receives the source-bound cart once, then applies without a bundle upload route.
	if strings.Join(paths, ",") != "/execution-config/plan,/execution-config/apply" || !strings.Contains(output.String(), "Execution token (shown once): once") {
		t.Fatalf("routes=%#v output=%q", paths, output.String())
	}
	parsed, err := configfile.ParseFile(request.path)
	if err != nil || parsed.Kind != configfile.KindExecution || parsed.Execution.Source != source {
		t.Fatalf("published config=%#v err=%v", parsed, err)
	}
}

// newPromptExecutionTestServer records the Engine control routes and checks the source-bearing cart.
func newPromptExecutionTestServer(t *testing.T, appID string, paths *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		*paths = append(*paths, request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		// The plan owns source compilation; apply only consumes its immutable receipt.
		switch request.URL.Path {
		case "/execution-config/plan":
			assertPromptExecutionPlanCart(t, request)
			_, _ = writer.Write([]byte(`{"plan_id":"plan-1","summary":{}}`))
		case "/execution-config/apply":
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-1","app_family_id":"family-1","app_id":"` + appID + `","execution_token":"once"}`))
		default:
			t.Errorf("unexpected route %s", request.URL.Path)
		}
	}))
}

// assertPromptExecutionPlanCart checks source, selected operations, and absence of a client digest.
func assertPromptExecutionPlanCart(t *testing.T, request *http.Request) {
	t.Helper()
	var body struct {
		Config struct {
			Kind         string `json:"kind"`
			Source       string `json:"source"`
			BundleDigest string `json:"bundle_digest"`
			Services     map[string]struct {
				Operations []string `json:"operations"`
			} `json:"services"`
		} `json:"config"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	// A local compiler regression would remove source or add a digest before Engine receives the cart.
	if body.Config.Kind != "execution" || !strings.Contains(body.Config.Source, "buildExecutionApp") || body.Config.BundleDigest != "" || len(body.Config.Services["crm"].Operations) != 1 {
		t.Errorf("invalid Engine cart: %#v", body.Config)
	}
}
