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

// TestPromptUnifiedAppConfigProducesEngineCompiledCart verifies describe preserves a lintable file without sending its path.
func TestPromptUnifiedAppConfigProducesEngineCompiledCart(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join(".fused", "unified_app"), 0o755); err != nil {
		t.Fatal(err)
	}
	request := scaffoldRequest{name: "billing", version: "1.0.0", bucket: "main", services: []scaffoldService{{name: "crm", version: "v1"}}, operations: []scaffoldOperation{{service: "crm", operation: "createCustomer"}}, events: []scaffoldEvent{{service: "crm", event: "customer.created"}}, webhookAttachment: "billing-webhooks"}
	source := "export default buildUnifiedApp({});"
	if err := os.WriteFile(filepath.Join(".fused", "unified_app", "billing.tsx"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	config := promptUnifiedAppConfig(request, "billing.tsx")
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := configfile.Parse(data, ".fused/unified_app/billing.yaml")
	// The shared parser must admit source-only hosted desired state and retain exact scope.
	if err != nil || parsed.Kind != configfile.KindUnifiedApp || parsed.UnifiedApp.Source != source || parsed.UnifiedApp.SourcePath != "billing.tsx" || parsed.UnifiedApp.BundleDigest != "" {
		t.Fatalf("hosted config = %#v, %v", parsed, err)
	}
	if got := parsed.UnifiedApp.Services["crm"]; len(got.Operations) != 1 || got.Operations[0] != "createCustomer" {
		t.Fatalf("operation scope = %#v", got)
	}
	// The source and trigger must travel in the same immutable Engine cart.
	if got := parsed.UnifiedApp.Services["crm"]; parsed.UnifiedApp.WebhookAttachment != "billing-webhooks" || len(got.Webhooks) != 1 || got.Webhooks[0] != "customer.created" {
		t.Fatalf("trigger scope = %#v", parsed.UnifiedApp)
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
	source, err := decodePromptExecutionDraft(`{"clarification":"","source":"export default buildUnifiedApp({})"}`)
	if err != nil || !strings.Contains(source, "buildUnifiedApp") {
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

// TestPromptExecutionDraftSelectionsPinsEvents proves webhook input evidence never grants an operation binding.
func TestPromptExecutionDraftSelectionsPinsEvents(t *testing.T) {
	serviceID := uuid.New().String()
	plan := promptInitPlan{primary: scaffoldRequest{events: []scaffoldEvent{{service: "crm", event: "customer.created"}}}, resolved: []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "crm", serviceID: serviceID}, version: "v1"}}}
	got, err := promptExecutionDraftSelections(plan)
	// The exact event enters Registry composition even when the app calls no provider operation.
	if err != nil || len(got) != 1 || got[0] != (api.PromptOperationSelection{Service: "crm", ServiceID: serviceID, Version: "v1", Event: "customer.created"}) {
		t.Fatalf("event selections = %#v, %v", got, err)
	}
}

// TestDeployPromptExecutionSendsCartToEngine verifies plan compiles remotely and apply needs no CLI bundle attachment.
func TestDeployPromptExecutionSendsCartToEngine(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(".fused", "unified_app"), 0o755); err != nil {
		t.Fatal(err)
	}
	appID := uuid.New().String()
	paths := []string{}
	server := newPromptExecutionTestServer(t, appID, &paths)
	defer server.Close()
	client := api.NewClient(server.URL, "fsk_test")
	request := scaffoldRequest{name: "billing", version: "1.0.0", bucket: "main", path: filepath.Join(".fused", "unified_app", "billing.yaml"), services: []scaffoldService{{name: "crm", version: "v1"}}, operations: []scaffoldOperation{{service: "crm", operation: "create"}}}
	source := "export default buildUnifiedApp({});"
	plan := promptInitPlan{mode: unifiedInitModeUnified, primary: request, execution: &promptExecutionDraft{source: source}}
	command := &cobra.Command{}
	var output bytes.Buffer
	command.SetOut(&output)
	if err := deployPromptExecution(command, client, plan, false); err != nil {
		t.Fatal(err)
	}
	// Engine receives the source-bound cart once, then applies without a bundle upload route.
	if strings.Join(paths, ",") != "/unified-app-config/plan,/unified-app-config/apply" || !strings.Contains(output.String(), "Execution token (shown once): once") {
		t.Fatalf("routes=%#v output=%q", paths, output.String())
	}
	// Deploy output must offer the stable invocation URL rather than the newly published version UUID.
	if !strings.Contains(output.String(), server.URL+"/v1/apps/family-1/executions") || strings.Contains(output.String(), "/v1/apps/"+appID+"/executions") {
		t.Fatalf("unstable execution URL: %s", output.String())
	}
	parsed, err := configfile.ParseFile(request.path)
	if err != nil || parsed.Kind != configfile.KindUnifiedApp || parsed.UnifiedApp.Source != source || parsed.UnifiedApp.SourcePath != "billing.tsx" {
		t.Fatalf("published config=%#v err=%v", parsed, err)
	}
	// Describe must leave reviewable YAML linked to TypeScript, never embed the generated code.
	if savedConfig, err := os.ReadFile(request.path); err != nil || !strings.Contains(string(savedConfig), "source_path: billing.tsx") || strings.Contains(string(savedConfig), "source: |") {
		t.Fatalf("saved config = %q, %v", savedConfig, err)
	}
	// A successful describe keeps its reviewed TypeScript in a standalone editable file.
	if saved, err := os.ReadFile(filepath.Join(".fused", "unified_app", "billing.tsx")); err != nil || string(saved) != source {
		t.Fatalf("saved source = %q, %v", saved, err)
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
		case "/unified-app-config/plan":
			assertPromptExecutionPlanCart(t, request)
			_, _ = writer.Write([]byte(`{"plan_id":"plan-1","summary":{}}`))
		case "/unified-app-config/apply":
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
	if body.Config.Kind != "unified_app" || !strings.Contains(body.Config.Source, "buildUnifiedApp") || body.Config.BundleDigest != "" || len(body.Config.Services["crm"].Operations) != 1 {
		t.Errorf("invalid Engine cart: %#v", body.Config)
	}
}
