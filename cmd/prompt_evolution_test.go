package cmd

import (
	"bytes"
	"encoding/json"
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

const promptSequenceFixture = `{"clarification":"","unified_operations":{"customer.invoice":{
"description":"Create a customer then invoice that customer",
"input":{"type":"object","properties":{"email":{"type":"string"}},"required":["email"]},
"bindings":{
"customer":{"service":"billing","operation":"createCustomer","input":{"email":"${input.email}"}},
"invoice":{"service":"billing","operation":"createInvoice","depends_on":["customer"],"input":{"customerId":"${response.customer.id}"}}},
"output":{"type":"object","properties":{"invoiceId":{"type":"string","value":"${response.invoice.id}"}}}
}}}`

// promptSequenceRequest supplies the exact classified scope used by all composition boundary tests.
func promptSequenceRequest() scaffoldRequest {
	return scaffoldRequest{kind: configfile.KindSDK, name: "billing-sdk", version: "1.0.0", language: "typescript", bucket: "default", bucketSet: true,
		services:   []scaffoldService{{name: "billing", version: "v1"}},
		operations: []scaffoldOperation{{service: "billing", operation: "createCustomer"}, {service: "billing", operation: "createInvoice"}},
	}
}

// TestPromptSequenceDraftPreservesDataflowAndReview verifies the complete generated definition survives shared validation and review.
func TestPromptSequenceDraftPreservesDataflowAndReview(t *testing.T) {
	operations, err := decodePromptUnifiedDraft(promptSequenceFixture, promptSequenceRequest())
	// A fully grounded two-step definition should retain its input, dependency, and exact output.
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printPromptUnifiedOperations(&out, operations); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"customer -> invoice", "${response.customer.id}", "${input.email}", "invoiceId"} {
		// Approval must expose every behavior-bearing part of the generated composition.
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("proposal omitted %q: %s", expected, &out)
		}
	}
}

// TestPromptSequenceRejectsUnsafeDrafts prevents malformed or widened model output from reaching config publication.
func TestPromptSequenceRejectsUnsafeDrafts(t *testing.T) {
	cases := map[string]string{
		"parallel":             strings.Replace(promptSequenceFixture, `"depends_on":["customer"]`, `"depends_on":[]`, 1),
		"cycle":                strings.Replace(promptSequenceFixture, `"depends_on":["customer"]`, `"depends_on":["invoice"]`, 1),
		"extra call":           strings.Replace(promptSequenceFixture, `"createInvoice"`, `"deleteCustomer"`, 1),
		"rollback":             strings.Replace(promptSequenceFixture, `"operation":"createInvoice"`, `"operation":"createInvoice","rollback":{"operation":"createInvoice"}`, 1),
		"unknown field":        strings.Replace(promptSequenceFixture, `"description":`, `"on_failure":"continue","description":`, 1),
		"unavailable response": strings.Replace(promptSequenceFixture, `${response.customer.id}`, `${response.unselected.id}`, 1),
		"clarification":        `{"clarification":"Which customer ID should be used?","unified_operations":{}}`,
		"trailing prose":       promptSequenceFixture + " please apply",
	}
	for name, raw := range cases {
		// Each malformed variant must fail independently before any lifecycle call.
		t.Run(name, func(t *testing.T) {
			if _, err := decodePromptUnifiedDraft(raw, promptSequenceRequest()); err == nil {
				t.Fatal("unsafe draft accepted")
			}
		})
	}
	request := promptSequenceRequest()
	request.operations = append(request.operations, scaffoldOperation{service: "billing", operation: "getCustomer"})
	// A syntactically valid subset still fails when the model omitted a requested capability.
	if _, err := decodePromptUnifiedDraft(promptSequenceFixture, request); err == nil || !strings.Contains(err.Error(), "omitted") {
		t.Fatalf("omitted operation: %v", err)
	}
}

// TestPromptUpdatePreservesAuthAndInfersSuccessor proves a generated composition uses the existing additive lifecycle.
func TestPromptUpdatePreservesAuthAndInfersSuccessor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "billing.yaml")
	baseline := `apiVersion: fused/v1
kind: sdk
name: billing-sdk
version: 1.2.3
language: python
bucket: enterprise
services:
  billing:
    version: v1
    operations: [getCustomer]
    auth:
      type: api_key
      name: private-routing
`
	// The baseline represents authored state that must survive model-driven additions byte-for-byte until apply.
	if err := os.WriteFile(path, []byte(baseline), 0600); err != nil {
		t.Fatal(err)
	}
	target, err := parseUnifiedExtendTarget(path, "billing-sdk")
	if err != nil {
		t.Fatal(err)
	}
	request, err := promptUpdateRequest(&cobra.Command{}, promptSequenceRequest(), &target, &promptInitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A fixture's explicit create bucket must be omitted to simulate an ordinary update without --bucket.
	request.bucket, request.bucketSet = "", false
	request.unifiedOperations, err = decodePromptUnifiedDraft(promptSequenceFixture, request)
	if err != nil {
		t.Fatal(err)
	}
	// Only immutable successor lookup is needed; any unexpected operation execution would fail this server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"resource_not_found","message":"not found"}`))
	}))
	defer server.Close()
	plan, err := finalizePromptPlan(api.NewClient(server.URL, "test"), promptInitPlan{mode: unifiedInitModeSDK, primary: request, baseHash: target.config.SourceHash, baseVersion: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	_, parsed, _, err := prepareUnifiedInitPlanInput(plan.mode, plan.primary, deferScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatal(err)
	}
	// The new immutable version must retain settings and old operations while gaining the new composition.
	if parsed.SDK.Version != "1.3.0" || parsed.SDK.Language != "python" || parsed.SDK.Bucket != "enterprise" || parsed.SDK.Services["billing"].Version != "v1" || len(parsed.SDK.Services["billing"].Operations) != 3 || len(parsed.SDK.UnifiedOperations) != 1 {
		t.Fatalf("wrong successor: %#v", parsed.SDK)
	}
	data, _ := os.ReadFile(path)
	if string(data) != baseline || !strings.Contains(string(data), "private-routing") {
		// Draft preparation must not publish or strip authored auth routing.
		t.Fatal("proposal mutated baseline")
	}
	if strings.Contains(promptUpdateContext(&target), "private-routing") || strings.Contains(promptUpdateContext(&target), "enterprise") {
		// Context projection must not leak bucket or authentication configuration.
		t.Fatal("private config leaked into parser context")
	}
	if err := validatePromptPlanBaseline(plan); err != nil {
		t.Fatal(err)
	}
	// A concurrent edit invalidates the user's review before any resource can be applied.
	if err := os.WriteFile(path, append(data, []byte("\n# changed\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validatePromptPlanBaseline(plan); err == nil {
		t.Fatal("stale proposal accepted")
	}
}

// TestPromptUnifiedMergeIsAdditive verifies equal repeats are no-ops and conflicting names never overwrite an authored graph.
func TestPromptUnifiedMergeIsAdditive(t *testing.T) {
	operations, err := decodePromptUnifiedDraft(promptSequenceFixture, promptSequenceRequest())
	if err != nil {
		t.Fatal(err)
	}
	config := &configfile.AppConfig{}
	changed, err := mergePromptUnifiedOperations(config, operations)
	if err != nil || !changed {
		t.Fatalf("first merge: %v %v", changed, err)
	}
	changed, err = mergePromptUnifiedOperations(config, operations)
	if err != nil || changed {
		t.Fatalf("repeat merge: %v %v", changed, err)
	}
	conflict := operations["customer.invoice"]
	conflict.Description = "Different behavior"
	if _, err := mergePromptUnifiedOperations(config, map[string]configfile.UnifiedOperation{"customer.invoice": conflict}); err == nil {
		t.Fatal("conflicting composition overwrote existing graph")
	}
}

// TestPromptUpdateTargetNeverFallsBackToCreate protects missing targets and unsupported update intent before any lifecycle work.
func TestPromptUpdateTargetNeverFallsBackToCreate(t *testing.T) {
	dir := t.TempDir()
	withUnifiedExtendWorkingDirectory(t, dir)
	oldFile := ConfigFile
	ConfigFile = ""
	// Isolate the process-wide explicit path used by deterministic target resolution.
	t.Cleanup(func() { ConfigFile = oldFile })
	for _, intent := range []api.IntentPayload{
		{Action: "update"}, {Action: "update", Target: "missing"},
		{Action: "create", Target: "existing"}, {Action: "delete", Target: "existing"},
		{Action: "update", Clarification: "Removing operations requires an explicit config edit"},
	} {
		// Ambiguous or unsupported actions need corrected intent, never a creation fallback.
		if _, err := promptIntentUpdateTarget(nil, &intent); err == nil {
			t.Fatalf("invalid update became a proposal: %#v", intent)
		}
	}
}

// TestPromptIndependentCapabilitiesRemainIndependent verifies normal creation never calls the composition model.
func TestPromptIndependentCapabilitiesRemainIndependent(t *testing.T) {
	dir := t.TempDir()
	withUnifiedExtendWorkingDirectory(t, dir)
	// Every allowed request is discovery; an unexpected draft or mutation fails closed in this fixture.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string
			Variables map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		switch {
		case strings.Contains(body.Query, "ParsePromptIntent"):
			_, _ = w.Write([]byte(`{"data":{"parseSDKIntent":{"action":"create","kind":"sdk","name":"billing-sdk","language":"typescript","sequential":false,"services":[{"name":"billing","endpoint_queries":["createCustomer","createInvoice"]}]}}}`))
		case strings.Contains(body.Query, "WorkspaceServices"):
			_, _ = w.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"service_id":"service-billing","service_name":"Billing","service_slug":"billing","version":"v1","enabled_versions":[{"version":"v1"}]}],"total":1}}}`))
		case strings.Contains(body.Query, "classifyPromptOperation"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"classifyPromptOperation": body.Variables["query"]}})
		default:
			t.Errorf("independent capability goal reached unexpected route: %s %s", r.URL.Path, body.Query)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	oldURL, oldKey, oldFile, oldNoInput := EngineURL, APIKey, ConfigFile, NoInput
	EngineURL, APIKey, ConfigFile, NoInput = server.URL, "test", "", true
	// These globals configure the same discovery client as the public command.
	t.Cleanup(func() { EngineURL, APIKey, ConfigFile, NoInput = oldURL, oldKey, oldFile, oldNoInput })
	command := &cobra.Command{}
	command.SetErr(io.Discard)
	plan, err := buildPromptInitPlan(command, api.NewClient(server.URL, "test"), "Create an SDK with customer creation and invoice creation", &promptInitOptions{version: "1.0.0", bucket: "default"})
	if err != nil || len(plan.primary.unifiedOperations) != 0 || len(plan.primary.operations) != 2 || plan.primary.extend {
		// Plain capabilities must retain their independent methods and create-only behavior.
		t.Fatalf("unexpected independent plan: %#v %v", plan.primary, err)
	}
}

// TestPromptDraftUsesExactRegistrySelections verifies disclosed composition uses Registry contracts without sending authored config.
func TestPromptDraftUsesExactRegistrySelections(t *testing.T) {
	var disclosure bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string
			Variables map[string]string
		}
		// Only the bounded composition query is permitted in this fixture.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/graphql" || !strings.Contains(body.Query, "draftPromptUnifiedOperation") || !strings.Contains(disclosure.String(), "drafting model") {
			t.Error("wrong drafting transport or late disclosure")
		}
		var selections []api.PromptOperationSelection
		if err := json.Unmarshal([]byte(body.Variables["selections"]), &selections); err != nil || len(selections) != 2 || selections[0].Version != "v1" || selections[0].ServiceID != "service-billing" {
			t.Errorf("wrong exact grounding: %#v %v", selections, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"draftPromptUnifiedOperation": promptSequenceFixture}})
	}))
	defer server.Close()
	command := &cobra.Command{}
	command.SetErr(&disclosure)
	plan := promptInitPlan{goal: "Create a customer then invoice that customer", mode: unifiedInitModeSDK, primary: promptSequenceRequest(), resolved: []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "billing", serviceID: "service-billing"}, version: "v1"}}}
	if _, err := draftPromptUnifiedOperation(command, api.NewClient(server.URL, "test"), plan); err != nil {
		t.Fatal(err)
	}
	plan.primary.language = "go"
	// Unsupported emitters must reject sequential intent before paying for a drafting call.
	if _, err := draftPromptUnifiedOperation(command, nil, plan); err == nil {
		t.Fatal("Go composition accepted")
	}
}

// TestPromptUpdateSequenceThroughApply covers intent parsing, Jev selection, drafting, successor planning, and additive publication together.
func TestPromptUpdateSequenceThroughApply(t *testing.T) {
	// Both explicit CLI targeting and a target named only in prose must converge on the same reviewed successor.
	t.Run("explicit target", func(t *testing.T) { runPromptUpdateSequenceThroughApply(t, false) })
	t.Run("natural target", func(t *testing.T) { runPromptUpdateSequenceThroughApply(t, true) })
}

// runPromptUpdateSequenceThroughApply shares exact mutation assertions across explicit and inferred update selection.
func runPromptUpdateSequenceThroughApply(t *testing.T, inferred bool) {
	t.Helper()
	dir := t.TempDir()
	withUnifiedExtendWorkingDirectory(t, dir)
	path := filepath.Join(dir, ".fused", "sdks", "billing.yaml")
	// Conventional discovery paths allow the natural-language update to find the same original file.
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	baseline := "apiVersion: fused/v1\nkind: sdk\nname: billing-sdk\nversion: 1.0.0\nlanguage: python\nbucket: default\nservices:\n  billing:\n    version: v1\n    operations: [getCustomer]\n"
	if err := os.WriteFile(path, []byte(baseline), 0600); err != nil {
		// A valid baseline is required to exercise the update path rather than implicit creation.
		t.Fatal(err)
	}
	base, calls := newSDKInitLifecycleServer(t)
	defer base.Close()
	archive := deferredSDKArchive(t)
	classifications, drafts, parses := 0, 0, 0
	// Add model/discovery responses while retaining the real init test harness for plan/apply receipt validation.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		var body struct {
			Query     string
			Variables map[string]string
		}
		_ = json.Unmarshal(data, &body)
		// Each fixture branch represents a separate read-only preparation boundary.
		switch {
		case r.URL.Path == "/sdk-config/generation/app-1":
			_, _ = w.Write([]byte(`{"status":"complete","app_family_id":"family-1","app_id":"app-1"}`))
		case r.URL.Path == "/sdks/app-1/download":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive)
		case strings.Contains(body.Query, "ParsePromptIntent"):
			parses++
			if !(inferred && parses == 1) && !strings.Contains(body.Variables["appContext"], "billing-sdk") {
				// The parser must know the existing target without receiving its whole config.
				t.Error("missing update context")
			}
			// The first natural-language pass identifies the target; the second resolves its omitted service from context.
			if inferred && parses == 1 {
				_, _ = w.Write([]byte(`{"data":{"parseSDKIntent":{"action":"update","target":"billing-sdk","kind":"sdk","services":[]}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"parseSDKIntent":{"action":"update","target":"billing-sdk","kind":"sdk","name":"do-not-create-this","language":"typescript","sequential":true,"services":[{"name":"billing","endpoint_queries":["createCustomer","createInvoice"]}]}}}`))
		case strings.Contains(body.Query, "WorkspaceServices"):
			_, _ = w.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"service_id":"00000000-0000-4000-8000-000000000001","service_name":"Billing","service_slug":"billing","version":"v2","enabled_versions":[{"version":"v1"},{"version":"v2"}]}],"total":1}}}`))
		case strings.Contains(body.Query, "classifyPromptOperation"):
			classifications++
			if body.Variables["version"] != "v1" {
				// The workspace's newer default must not replace the app's authored service pin.
				t.Errorf("classification escaped pin: %#v", body.Variables)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"classifyPromptOperation": body.Variables["query"]}})
		case strings.Contains(body.Query, "DraftPromptUnifiedOperation"):
			drafts++
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"draftPromptUnifiedOperation": promptSequenceFixture}})
		case strings.Contains(body.Query, "ResolveAppReference"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"resource_not_found","message":"not found"}`))
		case strings.Contains(body.Query, "ServiceOperations"):
			_, _ = w.Write([]byte(`{"data":{"serviceOperations":[{"name":"getCustomer"},{"name":"createCustomer"},{"name":"createInvoice"}]}}`))
		default:
			// The underlying harness fails any unexpected provider execution or lifecycle route.
			base.Config.Handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	oldURL, oldKey, oldFile, oldNoInput := EngineURL, APIKey, ConfigFile, NoInput
	EngineURL, APIKey, ConfigFile, NoInput = server.URL, "test", "", true
	// Global command state is isolated from neighboring lifecycle tests.
	t.Cleanup(func() { EngineURL, APIKey, ConfigFile, NoInput = oldURL, oldKey, oldFile, oldNoInput })
	command := &cobra.Command{}
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	opts := &promptInitOptions{update: "billing-sdk"}
	// The inferred case supplies no flag or exact file, exercising deterministic discovery after initial parsing.
	if inferred {
		opts.update = ""
	}
	plan, err := buildPromptInitPlan(command, api.NewClient(server.URL, "test"), "Add an operation to billing-sdk that creates a customer then invoices them", opts)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != baseline || len(*calls) != 0 || classifications != 2 || drafts != 1 {
		// Model preparation must remain mutation-free until the proposal is accepted.
		t.Fatalf("unreviewed mutation or incomplete preparation: %v, %d, %d", *calls, classifications, drafts)
	}
	if err := printPromptInitPlan(command, plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1.0.0 -> 1.1.0") || !strings.Contains(output.String(), "customer -> invoice") {
		// Review must contain both immutable identity change and executable order.
		t.Fatalf("incomplete proposal: %s", &output)
	}
	if err := executePromptInitPlan(command, plan); err != nil {
		t.Fatal(err)
	}
	parsed, err := configfile.ParseFile(path)
	if err != nil || parsed.SDK.Version != "1.1.0" || parsed.SDK.Language != "python" || len(parsed.SDK.UnifiedOperations) != 1 || len(parsed.SDK.Services["billing"].Operations) != 3 {
		// Final persisted state must be the reviewed additive successor in the original file.
		t.Fatalf("wrong applied config: %#v %v", parsed, err)
	}
	if strings.Join(*calls, ",") != "sdk-plan,sdk-apply" {
		// Existing workspace membership needs no activation and the workflow is never executed as a test.
		t.Fatalf("unexpected mutation sequence: %v", *calls)
	}
}
