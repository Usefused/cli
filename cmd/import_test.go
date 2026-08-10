package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type importPlanRequestCapture struct {
	saw        bool
	authHeader string
	body       map[string]any
}

func newImportPlanTestServer(t *testing.T) (*httptest.Server, *importPlanRequestCapture) {
	t.Helper()
	capture := &importPlanRequestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/integrations/import/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		capture.saw = true
		capture.authHeader = r.Header.Get("x-api-key")
		if err := json.NewDecoder(r.Body).Decode(&capture.body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_id": "plan-1",
			"source_hash": "hash-1",
			"review_hash": "review-1",
			"adapter_version": "openapi-v2",
			"service_id": "",
			"slug": "widgets",
			"name": "Widgets",
			"is_new_service": true,
			"action": "create_service",
			"target_version": "1.0",
			"diff": {"added": 1, "changed": 0, "removed": 0}
		}`))
	}))
	return server, capture
}

func assertImportPlanRequest(t *testing.T, capture *importPlanRequestCapture) {
	t.Helper()
	if !capture.saw {
		t.Fatal("expected a request to /integrations/import/plan")
	}
	if capture.authHeader != "fsk_test" {
		t.Errorf("expected api key header, got %q", capture.authHeader)
	}
	if capture.body["name"] != "Widgets" || capture.body["slug"] != "widgets" {
		t.Errorf("unexpected request body %#v", capture.body)
	}
	if capture.body["source_content"] == "" || capture.body["source_content"] == nil {
		t.Error("expected the local spec file's content to be sent as source_content")
	}
	if capture.body["target_type"] != "endpoints" {
		t.Errorf("expected endpoint target in request, got %#v", capture.body["target_type"])
	}
}

func assertImportPlanOutput(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "create_service") || !strings.Contains(out, "plan-1") {
		t.Errorf("expected a new-service summary naming the plan ID, got %q", out)
	}
	if !strings.Contains(out, "target: endpoints") {
		t.Errorf("expected the plan summary to confirm target scope, got %q", out)
	}
	if !strings.Contains(out, "Run `fused-cli import apply`") {
		t.Errorf("expected the summary to name the installed CLI binary, got %q", out)
	}
}

// TestImportPlanWritesReceiptAndPostsSpecContent covers Task 6's CLI test
// requirement: a local spec file is read and sent as source_content, the
// request reaches /integrations/import/plan with the right headers, and the
// plan receipt is written for a later "import apply" to pick up.
func TestImportPlanWritesReceiptAndPostsSpecContent(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "widgets.json")
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0","info":{"title":"Widgets","version":"1.0"},"paths":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	server, capture := newImportPlanTestServer(t)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"import", "plan", specPath, "--name", "Widgets", "--slug", "widgets"})
	assertImportPlanRequest(t, capture)
	assertImportPlanOutput(t, out)

	receiptPath := filepath.Join(dir, ".fused/.state/import.plan.json")
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("expected a plan receipt to be written: %v", err)
	}
	var receipt importPlanReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.PlanID != "plan-1" || receipt.ReviewHash != "review-1" || receipt.SourceHash != "hash-1" {
		t.Errorf("unexpected receipt %+v", receipt)
	}
}

type importApplyRequestCapture struct {
	saw  bool
	body map[string]string
}

func newImportApplyTestServer(t *testing.T) (*httptest.Server, *importApplyRequestCapture) {
	t.Helper()
	capture := &importApplyRequestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/integrations/import/plan" {
			t.Fatal("apply must not re-plan when a receipt exists")
		}
		if r.URL.Path != "/integrations/import/apply" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		capture.saw = true
		if err := json.NewDecoder(r.Body).Decode(&capture.body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-3","service_id":"svc-1","is_new_service":false,"action":"update_version","version":"2026-07-14","revision":2}`))
	}))
	return server, capture
}

func TestBuildSpecImportRequestRequiresSlug(t *testing.T) {
	specPath := filepath.Join(t.TempDir(), "widgets.json")
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0","info":{"title":"Widgets","version":"1.0"},"paths":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := buildSpecImportRequest(specPath, importSpecPlanOptions{name: "Widgets"})
	if err == nil || !strings.Contains(err.Error(), "--slug") {
		t.Fatalf("expected a required-slug error, got %v", err)
	}
}

func TestBuildSpecImportRequestUsesURLFlagAndExplicitVersion(t *testing.T) {
	req, err := buildSpecImportRequest("", importSpecPlanOptions{
		name: "Events", slug: "events", url: "https://example.test/asyncapi.yaml", version: "2026-07", strict: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.SourceURL != "https://example.test/asyncapi.yaml" || req.Version != "2026-07" || req.SourceContent != "" {
		t.Fatalf("unexpected URL import request: %+v", req)
	}
	if !req.Strict {
		t.Fatal("expected strict mode on the import request")
	}
}

func TestBuildSpecImportRequestSendsOverlayBytesUnchanged(t *testing.T) {
	overlayPath := filepath.Join(t.TempDir(), "provider.overlay.yaml")
	overlay := []byte("operations:\r\n  listWidgets: {pagination: cursor}\r\n# keep me\n")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}

	req, err := buildSpecImportRequest("", importSpecPlanOptions{
		name: "Widgets", slug: "widgets", url: "https://example.test/openapi.json", overlay: overlayPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.OverlayContent == nil || *req.OverlayContent != string(overlay) {
		t.Fatalf("overlay bytes changed: %#v", req.OverlayContent)
	}
}

func TestBuildSpecImportRequestPreservesEmptyOverlayForRegistryValidation(t *testing.T) {
	overlayPath := filepath.Join(t.TempDir(), "empty.overlay.yaml")
	if err := os.WriteFile(overlayPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	req, err := buildSpecImportRequest("", importSpecPlanOptions{
		name: "Widgets", slug: "widgets", url: "https://example.test/openapi.json", overlay: overlayPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.OverlayContent == nil || *req.OverlayContent != "" {
		t.Fatalf("empty overlay must still reach Registry, got %#v", req.OverlayContent)
	}
}

func TestBuildSpecImportRequestRejectsRemoteOverlay(t *testing.T) {
	_, err := buildSpecImportRequest("", importSpecPlanOptions{
		name: "Widgets", slug: "widgets", url: "https://example.test/openapi.json", overlay: "https://example.test/overlay.yaml",
	})
	if err == nil || !strings.Contains(err.Error(), "local file path") {
		t.Fatalf("expected local-overlay error, got %v", err)
	}
}

func TestBuildSpecImportRequestRejectsUnreadableOverlay(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.overlay.yaml")
	_, err := buildSpecImportRequest("", importSpecPlanOptions{
		name: "Widgets", slug: "widgets", url: "https://example.test/openapi.json", overlay: missing,
	})
	if err == nil || !strings.Contains(err.Error(), "failed to read overlay file") {
		t.Fatalf("expected overlay read error, got %v", err)
	}
}

func TestImportPlanReceiptContainsReviewMetadataOnly(t *testing.T) {
	receipt := newImportPlanReceipt(&api.SpecImportPlanResponse{
		PlanID: "plan-1", Slug: "widgets", ReviewHash: "review-1",
		SourceHash: "source-1", OverlayHash: "overlay-1",
	})
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"review_hash":"review-1"`, `"source_hash":"source-1"`, `"overlay_hash":"overlay-1"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("receipt missing %s: %s", expected, encoded)
		}
	}
	for _, forbidden := range []string{"overlay_content", "overlay_path"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("receipt must not store %s: %s", forbidden, encoded)
		}
	}
}

func TestPrintImportPlanSummaryIncludesDiagnostics(t *testing.T) {
	response := &api.SpecImportPlanResponse{
		PlanID: "plan-1", Name: "Widgets", Slug: "widgets", TargetVersion: "1.0", TargetType: "endpoints", Action: "create_service",
		Diagnostics: []api.SpecImportDiagnostic{
			{Severity: "warning", Code: "unsupported_request_media_type", Scope: "operation", Method: "post", Path: "/widgets", Message: "Request body\nmedia type was not imported.", Recommendation: "Choose a supported media type."},
			{Severity: "info", Code: "source_format_detected", Scope: "source", Message: "OpenAPI 3.1 detected."},
		},
	}
	out := &strings.Builder{}
	printImportPlanSummary(out, response)

	for _, expected := range []string{
		"Diagnostics (2):",
		"WARNING unsupported_request_media_type [POST /widgets]: Request body media type was not imported.",
		"Recommendation: Choose a supported media type.",
		"INFO source_format_detected [source]: OpenAPI 3.1 detected.",
	} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("diagnostic summary missing %q:\n%s", expected, out.String())
		}
	}
}

func TestPrintImportPlanSummaryIncludesRegistrySourceFormat(t *testing.T) {
	response := &api.SpecImportPlanResponse{
		PlanID: "plan-1", Name: "Widgets", Slug: "widgets", TargetVersion: "1.0",
		TargetType: "endpoints", Action: "create_service", SourceFormat: "swagger2",
		ReviewHash: "review-1", OverlayHash: "overlay-1",
	}
	out := &strings.Builder{}
	printImportPlanSummary(out, response)
	if !strings.Contains(out.String(), "Source format: swagger2") {
		t.Fatalf("plan summary omitted Registry source format: %q", out.String())
	}
	if !strings.Contains(out.String(), "Review hash: review-1") || !strings.Contains(out.String(), "Overlay: applied") {
		t.Fatalf("plan summary omitted review identity: %q", out.String())
	}
}

func TestImportPlanRegistersStrictFlag(t *testing.T) {
	flag := importPlanCmd.Flags().Lookup("strict")
	if flag == nil || flag.DefValue != "false" {
		t.Fatalf("strict flag = %#v, want default false", flag)
	}
}

func TestImportPlanRegistersOverlayFlag(t *testing.T) {
	flag := importPlanCmd.Flags().Lookup("overlay")
	if flag == nil || flag.DefValue != "" {
		t.Fatalf("overlay flag = %#v, want empty default", flag)
	}
	if importApplyCmd.Flags().Lookup("source-hash") != nil {
		t.Fatal("legacy --source-hash apply flag must not remain registered")
	}
}

func TestRunImportPlanRecordsStrictMode(t *testing.T) {
	server, _ := newImportPlanTestServer(t)
	defer server.Close()
	setImportTestAPI(t, server.URL)
	specPath := filepath.Join(t.TempDir(), "widgets.json")
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := provider.Tracer("test").Start(t.Context(), "cli.import.plan")
	command := &cobra.Command{}
	command.SetContext(ctx)
	command.SetOut(&strings.Builder{})
	overlayPath := filepath.Join(t.TempDir(), "overlay.yaml")
	if err := os.WriteFile(overlayPath, []byte("secret-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	err := runImportPlan(command, specPath, importSpecPlanOptions{name: "Widgets", slug: "widgets", strict: true, overlay: overlayPath, jsonOut: true})
	span.End()
	if err != nil {
		t.Fatalf("runImportPlan: %v", err)
	}
	value, ok := importPlanSpanAttribute(exporter.GetSpans(), "strict_mode")
	if !ok || value != true {
		t.Fatalf("strict_mode OTEL attribute = %#v, present=%v", value, ok)
	}
	value, ok = importPlanSpanAttribute(exporter.GetSpans(), "overlay_present")
	if !ok || value != true {
		t.Fatalf("overlay_present OTEL attribute = %#v, present=%v", value, ok)
	}
	value, ok = importPlanSpanAttribute(exporter.GetSpans(), "adapter_version")
	if !ok || value != "openapi-v2" {
		t.Fatalf("adapter_version OTEL attribute = %#v, present=%v", value, ok)
	}
	value, ok = importPlanSpanAttribute(exporter.GetSpans(), "outcome")
	if !ok || value != "create_service" {
		t.Fatalf("outcome OTEL attribute = %#v, present=%v", value, ok)
	}
	for _, span := range exporter.GetSpans() {
		for _, value := range span.Attributes {
			if strings.Contains(value.Value.Emit(), overlayPath) || strings.Contains(value.Value.Emit(), "secret-marker") || strings.Contains(value.Value.Emit(), "review-1") {
				t.Fatalf("sensitive overlay/review data reached OTEL: %s=%s", value.Key, value.Value.Emit())
			}
		}
	}
}

func TestImportPlanTelemetryLabelsAreBounded(t *testing.T) {
	if got := boundedImportTelemetryValue("adapter with spaces"); got != "other" {
		t.Fatalf("unsafe adapter label = %q, want other", got)
	}
	if got := boundedImportTelemetryValue(strings.Repeat("a", 65)); got != "other" {
		t.Fatalf("long adapter label = %q, want other", got)
	}
	if got := importPlanOutcome("provider_defined_action"); got != "unknown" {
		t.Fatalf("unbounded outcome = %q, want unknown", got)
	}
}

func TestRunImportPlanPrintsStrictRejectionDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{
			"error":"strict_import_rejected",
			"message":"strict import rejected provider contract diagnostics",
			"diagnostics":[{"severity":"warning","code":"unsupported_request_media_type","scope":"operation","method":"POST","path":"/widgets","message":"Request body was skipped.","recommendation":"Declare application/json."}]
		}`))
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)
	specPath := filepath.Join(t.TempDir(), "widgets.json")
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	command := &cobra.Command{}
	command.SetContext(t.Context())
	errOut := &strings.Builder{}
	command.SetErr(errOut)
	err := runImportPlan(command, specPath, importSpecPlanOptions{name: "Widgets", slug: "widgets", strict: true, jsonOut: true})
	if err == nil || !strings.Contains(err.Error(), "strict_import_rejected") {
		t.Fatalf("expected strict rejection, got %v", err)
	}
	for _, expected := range []string{"WARNING unsupported_request_media_type [POST /widgets]", "Recommendation: Declare application/json."} {
		if !strings.Contains(errOut.String(), expected) {
			t.Fatalf("strict rejection output missing %q: %s", expected, errOut.String())
		}
	}
}

func setImportTestAPI(t *testing.T, serverURL string) {
	t.Helper()
	previousEngineURL, previousAPIKey := EngineURL, APIKey
	EngineURL, APIKey = serverURL, "fsk_test"
	t.Cleanup(func() { EngineURL, APIKey = previousEngineURL, previousAPIKey })
}

func importPlanSpanAttribute(spans tracetest.SpanStubs, key string) (any, bool) {
	for _, span := range spans {
		for _, value := range span.Attributes {
			if string(value.Key) == key {
				return value.Value.AsInterface(), true
			}
		}
	}
	return nil, false
}

func TestBuildSpecImportRequestNormalizesAndValidatesTarget(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   string
		bad    bool
	}{
		{name: "omitted means endpoints", want: "endpoints"},
		{name: "all uses backward compatible wire value", target: " ALL "},
		{name: "endpoints", target: "endpoints", want: "endpoints"},
		{name: "webhooks", target: "webhooks", want: "webhooks"},
		{name: "invalid", target: "schemas", bad: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := buildSpecImportRequest("", importSpecPlanOptions{
				name: "Events", slug: "events", url: "https://example.test/asyncapi.yaml", target: test.target,
			})
			if test.bad {
				if err == nil || !strings.Contains(err.Error(), "--target") {
					t.Fatalf("expected target validation error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if req.TargetType != test.want {
				t.Errorf("target_type = %q, want %q", req.TargetType, test.want)
			}
		})
	}
}

func TestBuildSpecImportRequestRejectsPositionalURL(t *testing.T) {
	_, err := buildSpecImportRequest("https://example.test/schema", importSpecPlanOptions{name: "Events", slug: "events"})
	if err == nil || !strings.Contains(err.Error(), "--url") {
		t.Fatalf("expected positional URLs to direct users to --url, got %v", err)
	}
}

// TestImportPlanPrintsUsageWarningWhenNonEmpty is Task 6's other explicit
// CLI test requirement: when the mocked plan response's usage block is
// non-empty, the printed summary must include the "N SDKs / M workspaces"
// line naming which ones touch the changed endpoint.
func TestImportPlanPrintsUsageWarningWhenNonEmpty(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "widgets.json")
	os.WriteFile(specPath, []byte(`{"openapi":"3.0.0","info":{"title":"Widgets","version":"1.0"},"paths":{}}`), 0644)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"plan_id": "plan-2",
			"source_hash": "hash-2",
			"review_hash": "review-2",
			"service_id": "svc-1",
			"slug": "widgets",
			"name": "Widgets",
			"is_new_service": false,
			"action": "update_version",
			"target_version": "1.0",
			"diff": {"added": 0, "changed": 1, "removed": 0, "changed_names": ["listWidgets"]},
			"usage": {
				"sdks": [{"id": "sdk-1", "name": "widgets-sdk", "uses_changed_endpoint": true}],
				"workspaces": [{"id": "ws-1", "name": "prod"}]
			}
		}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"import", "plan", specPath, "--name", "Widgets", "--slug", "widgets"})

	if !strings.Contains(out, "1 SDKs / 1 workspaces use this version") {
		t.Fatalf("expected the usage-warning line, got %q", out)
	}
	if !strings.Contains(out, "widgets-sdk") || !strings.Contains(out, "uses a changed/removed endpoint") {
		t.Fatalf("expected the SDK usage line to name the SDK and flag the changed endpoint, got %q", out)
	}
	if !strings.Contains(out, "prod") {
		t.Fatalf("expected the workspace usage line, got %q", out)
	}
}

// TestImportApplyUsesReceiptWithoutReplanning covers "apply reads the most
// recent local receipt" -- it must post plan_id/review_hash straight from
// the receipt file, never re-plan.
func TestImportApplyUsesReceiptWithoutReplanning(t *testing.T) {
	dir := t.TempDir()
	server, capture := newImportApplyTestServer(t)
	defer server.Close()
	receiptPath := filepath.Join(dir, ".fused/.state/import.plan.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0755); err != nil {
		t.Fatal(err)
	}
	receipt := importPlanReceipt{Slug: "widgets", PlanID: "plan-3", ReviewHash: "review-3", SourceHash: "hash-3", EngineURL: server.URL}
	data, _ := json.Marshal(receipt)
	if err := os.WriteFile(receiptPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	out := runCommandInDirOutput(t, dir, server.URL, []string{"import", "apply"})

	if !capture.saw {
		t.Fatal("expected a request to /integrations/import/apply")
	}
	if capture.body["plan_id"] != "plan-3" || capture.body["review_hash"] != "review-3" {
		t.Errorf("expected plan_id/review_hash from the receipt, got %#v", capture.body)
	}
	if _, exists := capture.body["source_hash"]; exists {
		t.Errorf("source_hash must not authorize apply, got %#v", capture.body)
	}
	if !strings.Contains(out, "svc-1") || !strings.Contains(out, "2026-07-14") {
		t.Errorf("expected apply result naming service/version, got %q", out)
	}
	if !strings.Contains(out, "revision 2") {
		t.Errorf("expected apply result naming the internal revision, got %q", out)
	}
}

func TestImportApplyRejectsReceiptForDifferentEngine(t *testing.T) {
	dir := t.TempDir()
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requestCount++
	}))
	defer server.Close()
	receiptPath := filepath.Join(dir, ".fused/.state/import.plan.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0755); err != nil {
		t.Fatal(err)
	}
	receipt := importPlanReceipt{
		Slug: "widgets", PlanID: "plan-3", ReviewHash: "review-3", SourceHash: "hash-3",
		EngineURL: "https://different-engine.example.com",
	}
	data, _ := json.Marshal(receipt)
	if err := os.WriteFile(receiptPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	out := runCommandInDirExpectError(t, dir, server.URL, []string{"import", "apply"})
	if !strings.Contains(out, "receipt targets") {
		t.Fatalf("expected target mismatch, got %q", out)
	}
	if requestCount != 0 {
		t.Fatalf("target mismatch must happen before import mutation; got %d request(s)", requestCount)
	}
}

// TestImportApplyPlanIDRequiresReviewHash guards the direct apply contract:
// deviation from the config plan/apply flags: --plan-id alone isn't enough
// because Registry owns the combined source and overlay review identity.
func TestImportApplyPlanIDRequiresReviewHash(t *testing.T) {
	dir := t.TempDir()
	oldwd, _ := os.Getwd()
	defer os.Chdir(oldwd)
	os.Chdir(dir)

	oldEngineURL, oldAPIKey := EngineURL, APIKey
	defer func() { EngineURL, APIKey = oldEngineURL, oldAPIKey }()
	EngineURL, APIKey = "http://example.invalid", "fsk_test"

	err := runImportApply(RootCmd, importSpecApplyOptions{planID: "plan-4"})
	if err == nil || !strings.Contains(err.Error(), "--review-hash") {
		t.Fatalf("expected a --review-hash-required error, got %v", err)
	}
}

func TestResolveImportApplyReceiptValidatesFlagPairsAndLegacyReceipts(t *testing.T) {
	tests := []struct {
		name string
		opts importSpecApplyOptions
		want string
	}{
		{name: "review without plan", opts: importSpecApplyOptions{reviewHash: "review-1"}, want: "--plan-id is required"},
		{name: "receipt and direct", opts: importSpecApplyOptions{planID: "plan-1", reviewHash: "review-1", receiptPath: "receipt.json"}, want: "cannot be combined"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveImportApplyReceipt(test.opts)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy-receipt.json")
	if err := os.WriteFile(legacyPath, []byte(`{"plan_id":"plan-1","source_hash":"source-1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := resolveImportApplyReceipt(importSpecApplyOptions{receiptPath: legacyPath})
	if err == nil || !strings.Contains(err.Error(), "no review_hash") {
		t.Fatalf("legacy source-only receipt must fail closed, got %v", err)
	}
}

func TestRunImportPlanWritesExplicitReceiptInJSONMode(t *testing.T) {
	server, _ := newImportPlanTestServer(t)
	defer server.Close()
	setImportTestAPI(t, server.URL)
	specPath := filepath.Join(t.TempDir(), "widgets.json")
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(t.TempDir(), "plan.json")
	command := &cobra.Command{}
	command.SetContext(t.Context())
	out := &strings.Builder{}
	command.SetOut(out)
	err := runImportPlan(command, specPath, importSpecPlanOptions{
		name: "Widgets", slug: "widgets", jsonOut: true, receiptOut: receiptPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := readImportPlanReceiptFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.PlanID != "plan-1" || receipt.ReviewHash != "review-1" {
		t.Fatalf("unexpected JSON-mode receipt: %+v", receipt)
	}
	if !strings.Contains(out.String(), `"review_hash":"review-1"`) || !strings.Contains(out.String(), `"adapter_version":"openapi-v2"`) {
		t.Fatalf("JSON output omitted Registry review metadata: %s", out.String())
	}
}
