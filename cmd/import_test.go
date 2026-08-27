package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// newImportPlanTestServer returns a deterministic successful planner and the
// decoded request so command tests can assert both transport and output.
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
		response := map[string]any{
			"plan_id": "plan-1", "source_hash": "hash-1", "review_hash": "review-1",
			"adapter_version": "openapi-v2", "service_id": "", "slug": "widgets", "name": "Widgets",
			"is_new_service": true, "action": "create_service", "target_version": "1.0",
			"diff": map[string]any{"added": 1, "changed": 0, "removed": 0},
		}
		// The fake server mirrors Registry's closed explicit response shape only when requested.
		if destination, ok := capture.body["destination_version"].(string); ok && destination != "" {
			response["source_version"] = "source-v2"
			response["target_version"] = destination
			response["destination_version"] = destination
			response["is_new_service"] = false
			response["action"] = "update_version"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
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

const importApplyTestPlanID = "11111111-1111-4111-8111-111111111111"

// newImportApplyTestServer returns a complete committed proof for the exact captured receipt.
func newImportApplyTestServer(t *testing.T) (*httptest.Server, *importApplyRequestCapture) {
	t.Helper()
	capture := &importApplyRequestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Receipt apply must never silently recompute the reviewed plan.
		if r.URL.Path == "/integrations/import/plan" {
			t.Fatal("apply must not re-plan when a receipt exists")
		}
		// The fixture admits only the single mutation boundary under test.
		if r.URL.Path != "/integrations/import/apply" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		capture.saw = true
		// An unreadable request would invalidate the receipt-identity assertion.
		if err := json.NewDecoder(r.Body).Decode(&capture.body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"` + importApplyTestPlanID + `","operation_id":"` + importApplyTestPlanID + `","phase":"complete","commit_state":"committed","service_id":"22222222-2222-4222-8222-222222222222","service_version_id":"33333333-3333-4333-8333-333333333333","slug":"widgets","is_new_service":false,"action":"update_version","version":"2026-07-14","revision":2}`))
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

// TestBuildSpecImportRequestKeepsDestinationSeparateFromSourceVersion proves
// webhook attachment does not overload the versionless-source fallback.
func TestBuildSpecImportRequestKeepsDestinationSeparateFromSourceVersion(t *testing.T) {
	req, err := buildSpecImportRequest("", importSpecPlanOptions{
		name: "Events", slug: "events", url: "https://example.test/webhooks.yaml",
		version: "source-v2", destinationVersion: "service-v1", target: "webhooks",
	})
	// A valid split-version request must pass CLI validation unchanged.
	if err != nil {
		t.Fatal(err)
	}
	// All three identity dimensions must remain independently observable.
	if req.Version != "source-v2" || req.DestinationVersion != "service-v1" || req.TargetType != "webhooks" {
		t.Fatalf("source and destination versions were not kept distinct: %+v", req)
	}
}

// TestBuildSpecImportRequestRestrictsDestinationToWebhooks keeps destination
// attachment from changing endpoint import replacement semantics.
func TestBuildSpecImportRequestRestrictsDestinationToWebhooks(t *testing.T) {
	_, err := buildSpecImportRequest("", importSpecPlanOptions{
		name: "Events", slug: "events", url: "https://example.test/openapi.yaml",
		destinationVersion: "service-v1", target: "endpoints",
	})
	// Cross-surface destination targeting would reintroduce implicit contract merging.
	if err == nil || !strings.Contains(err.Error(), "--target webhooks") {
		t.Fatalf("expected webhook-only destination validation, got %v", err)
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

// TestPrintImportPlanSummaryShowsDistinctAttachedSourceVersion keeps source
// metadata visible without duplicating the ordinary single-version headline.
func TestPrintImportPlanSummaryShowsDistinctAttachedSourceVersion(t *testing.T) {
	tests := []struct {
		name               string
		sourceVersion      string
		destinationVersion string
		wantSourceLine     bool
	}{
		{name: "different explicit destination", sourceVersion: "webhooks-v2", destinationVersion: "service-v1", wantSourceLine: true},
		{name: "same explicit destination", sourceVersion: "service-v1", destinationVersion: "service-v1"},
		{name: "ordinary import", sourceVersion: "service-v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := &strings.Builder{}
			printImportPlanSummary(out, &api.SpecImportPlanResponse{
				PlanID: "plan-1", Name: "Widgets", Slug: "widgets", Action: "update_version",
				SourceVersion: test.sourceVersion, DestinationVersion: test.destinationVersion,
				TargetVersion: "service-v1", TargetType: "webhooks",
			})
			hasSourceLine := strings.Contains(out.String(), "Source version: "+test.sourceVersion)
			// Only a distinct explicit attachment source warrants an extra line.
			if hasSourceLine != test.wantSourceLine {
				t.Fatalf("source version line present=%v, want %v:\n%s", hasSourceLine, test.wantSourceLine, out.String())
			}
		})
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

// TestImportPlanRegistersDestinationVersionFlag keeps the explicit attachment
// mechanism discoverable without changing the established --version default.
func TestImportPlanRegistersDestinationVersionFlag(t *testing.T) {
	flag := importPlanCmd.Flags().Lookup("destination-version")
	// The destination selector must be opt-in and explain its narrow scope.
	if flag == nil || flag.DefValue != "" || !strings.Contains(flag.Usage, "--target webhooks") {
		t.Fatalf("destination-version flag = %#v, want an empty webhook-scoped flag", flag)
	}
	versionFlag := importPlanCmd.Flags().Lookup("version")
	// The established source fallback must remain discoverable as a separate concept.
	if versionFlag == nil || !strings.Contains(versionFlag.Usage, "Source provider version fallback") {
		t.Fatalf("version flag must retain source fallback semantics: %#v", versionFlag)
	}
}

// TestRunImportPlanRecordsStrictMode keeps import telemetry useful for audits
// while proving overlay bytes and review identities stay out of span data.
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
	err := runImportPlan(command, specPath, importSpecPlanOptions{
		name: "Widgets", slug: "widgets", strict: true, overlay: overlayPath, jsonOut: true,
		target: "webhooks", destinationVersion: "1.0",
	})
	span.End()
	if err != nil {
		t.Fatalf("runImportPlan: %v", err)
	}
	spans := exporter.GetSpans()
	assertImportPlanSpanAttribute(t, spans, "strict_mode", true)
	assertImportPlanSpanAttribute(t, spans, "overlay_present", true)
	assertImportPlanSpanAttribute(t, spans, "destination_version_present", true)
	assertImportPlanSpanAttribute(t, spans, "adapter_version", "openapi-v2")
	assertImportPlanSpanAttribute(t, spans, "outcome", "update_version")
	assertImportPlanTelemetrySecretSafe(t, spans, overlayPath)
}

// TestRunImportPlanRejectsMismatchedDestinationEcho prevents an old Registry
// from silently ignoring webhook attachment intent and creating a new version.
func TestRunImportPlanRejectsMismatchedDestinationEcho(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The acknowledgement is correct while the resolved target is not, so
		// this fixture exercises the second half of the attachment invariant.
		_, _ = w.Write([]byte(`{
			"plan_id":"plan-1","review_hash":"review-1","target_version":"1.0",
			"destination_version":"service-v2","target_type":"webhooks","action":"update_version"
		}`))
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)
	specPath := filepath.Join(t.TempDir(), "webhooks.json")
	// A local immutable fixture keeps the test on the same source-content path as production.
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}

	command := &cobra.Command{}
	command.SetContext(t.Context())
	err := runImportPlan(command, specPath, importSpecPlanOptions{
		name: "Widgets", slug: "widgets", target: "webhooks", destinationVersion: "service-v2",
	})
	// A correct marker with a redirected target must still fail closed.
	if err == nil || !strings.Contains(err.Error(), "did not plan the requested destination version") {
		t.Fatalf("expected destination echo mismatch, got %v", err)
	}
}

// TestRunImportPlanRejectsMissingDestinationAcknowledgement covers the subtle
// old-server case where source and destination resolve to the same version.
func TestRunImportPlanRejectsMissingDestinationAcknowledgement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// This response deliberately models an older Registry: target_version
		// happens to match, but no destination_version acknowledgement exists.
		_, _ = w.Write([]byte(`{
			"plan_id":"plan-1","review_hash":"review-1","target_version":"1.0",
			"target_type":"webhooks","action":"update_version"
		}`))
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)
	specPath := filepath.Join(t.TempDir(), "webhooks.json")
	// Matching source and destination values reproduce the legacy false-positive case.
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0","info":{"version":"1.0"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	command := &cobra.Command{}
	command.SetContext(t.Context())
	err := runImportPlan(command, specPath, importSpecPlanOptions{
		name: "Widgets", slug: "widgets", version: "1.0", target: "webhooks", destinationVersion: "1.0",
	})
	// Exact target equality is insufficient unless the server acknowledges destination semantics.
	if err == nil || !strings.Contains(err.Error(), "did not acknowledge") {
		t.Fatalf("expected missing destination acknowledgement, got %v", err)
	}
}

// TestNormalizeImportPlanResponseRejectsUnsolicitedDestinationMetadata ensures
// an ordinary request cannot be upgraded into attachment semantics by response fields.
func TestNormalizeImportPlanResponseRejectsUnsolicitedDestinationMetadata(t *testing.T) {
	err := normalizeAndValidateImportPlanResponse(
		api.SpecImportPlanRequest{TargetType: "endpoints"},
		&api.SpecImportPlanResponse{ReviewHash: "review-1", TargetVersion: "1.0", DestinationVersion: "1.0", SourceVersion: "source-v2"},
	)
	// Closed explicit-only response markers must fail when the request omitted them.
	if err == nil || !strings.Contains(err.Error(), "unsolicited") {
		t.Fatalf("expected unsolicited destination rejection, got %v", err)
	}
}

// assertImportPlanSpanAttribute keeps the telemetry contract table-driven so
// adding a bounded field does not increase the orchestration test's branches.
func assertImportPlanSpanAttribute(t *testing.T, spans tracetest.SpanStubs, key string, want any) {
	t.Helper()
	value, ok := importPlanSpanAttribute(spans, key)
	if !ok || value != want {
		t.Fatalf("%s OTEL attribute = %#v, present=%v, want %#v", key, value, ok, want)
	}
}

// assertImportPlanTelemetrySecretSafe checks every attribute because a new
// span or field must not silently expose the local overlay or review identity.
func assertImportPlanTelemetrySecretSafe(t *testing.T, spans tracetest.SpanStubs, overlayPath string) {
	t.Helper()
	for _, span := range spans {
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

// TestRunImportPlanPrintsStrictRejectionDiagnostics verifies the current
// nested contract retains actionable, bounded parser guidance.
func TestRunImportPlanPrintsStrictRejectionDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{
			"code":"strict_import_rejected",
			"message":"strict import rejected provider contract diagnostics",
			"diagnostics":[{"severity":"warning","code":"unsupported_request_media_type","scope":"operation","method":"POST","path":"/widgets","message":"Request body was skipped.","recommendation":"Declare application/json."}]
		}}`))
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
	err := runImportPlan(command, specPath, importSpecPlanOptions{name: "Widgets", slug: "widgets", strict: true})
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
	receipt := importPlanReceipt{Slug: "widgets", PlanID: importApplyTestPlanID, ReviewHash: "review-3", SourceHash: "hash-3", EngineURL: server.URL}
	data, _ := json.Marshal(receipt)
	if err := os.WriteFile(receiptPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	out := runCommandInDirOutput(t, dir, server.URL, []string{"import", "apply"})

	if !capture.saw {
		t.Fatal("expected a request to /integrations/import/apply")
	}
	if capture.body["plan_id"] != importApplyTestPlanID || capture.body["review_hash"] != "review-3" {
		t.Errorf("expected plan_id/review_hash from the receipt, got %#v", capture.body)
	}
	if _, exists := capture.body["source_hash"]; exists {
		t.Errorf("source_hash must not authorize apply, got %#v", capture.body)
	}
	if !strings.Contains(out, "22222222-2222-4222-8222-222222222222") || !strings.Contains(out, "2026-07-14") {
		t.Errorf("expected apply result naming service/version, got %q", out)
	}
	if !strings.Contains(out, "revision 2") {
		t.Errorf("expected apply result naming the internal revision, got %q", out)
	}
}

// TestSpecImportTimeoutUsesLargeDefaultAndExplicitOverride protects the split
// between ordinary one-minute requests and source-size-dependent import work.
func TestSpecImportTimeoutUsesLargeDefaultAndExplicitOverride(t *testing.T) {
	if got := specImportTimeout(&cobra.Command{}); got != 20*time.Minute {
		t.Fatalf("default spec import timeout = %s, want 20m", got)
	}

	previous := RequestTimeout
	t.Cleanup(func() { RequestTimeout = previous })
	for _, args := range [][]string{{"--timeout", "37s", "apply"}, {"apply", "--timeout", "37s"}} {
		RequestTimeout = api.DefaultTimeout
		root := &cobra.Command{Use: "fused-cli"}
		apply := &cobra.Command{Use: "apply"}
		root.PersistentFlags().DurationVar(&RequestTimeout, "timeout", api.DefaultTimeout, "test timeout")
		root.AddCommand(apply)
		root.SetArgs(args)
		apply.RunE = func(cmd *cobra.Command, _ []string) error {
			if got := specImportTimeout(cmd); got != 37*time.Second {
				t.Fatalf("explicit spec import timeout = %s, want 37s", got)
			}
			return nil
		}
		if err := root.Execute(); err != nil {
			t.Fatalf("execute %v: %v", args, err)
		}
	}
}

// TestImportApplyTimeoutIsOutcomeUnknownAndNotRetried proves the CLI neither
// claims failure nor replays a one-shot mutation after losing its response.
func TestImportApplyTimeoutIsOutcomeUnknownAndNotRetried(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)

	receiptPath := writeImportTimeoutReceipt(t, server.URL)

	previous := RequestTimeout
	RequestTimeout = 10 * time.Millisecond
	t.Cleanup(func() { RequestTimeout = previous })
	command := &cobra.Command{Use: "apply"}
	command.SetContext(context.Background())
	command.Flags().Duration("timeout", api.DefaultTimeout, "test timeout")
	if err := command.Flags().Set("timeout", RequestTimeout.String()); err != nil {
		t.Fatal(err)
	}

	err := runImportApply(command, importSpecApplyOptions{receiptPath: receiptPath})
	assertUnknownImportApplyTimeout(t, command, err, requests.Load())
}

// TestImportApplyMalformedSuccessRecoversThroughStatus proves a non-timeout 2xx
// body cannot record applied success or fall through to generic retry advice.
func TestImportApplyMalformedSuccessRecoversThroughStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"applied"`))
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)
	receiptPath := writeImportTimeoutReceipt(t, server.URL)
	command := &cobra.Command{Use: "apply"}
	command.SetContext(context.Background())
	err := runImportApply(command, importSpecApplyOptions{receiptPath: receiptPath})
	var unknown *importApplyOutcomeUnknownError
	// Malformed proof is unknown without pretending the configured budget expired.
	if !errors.As(err, &unknown) || unknown.timedOut || !strings.Contains(err.Error(), "fused-cli import status "+importApplyTestPlanID) || strings.Contains(err.Error(), "future large imports") {
		t.Fatalf("malformed apply recovery = %T %v", err, err)
	}
	classified := classifyCommandError(command, err)
	if classified.Code != "import_apply_outcome_unknown" || classified.Retryable || classified.CommitState != "unknown" || strings.Contains(strings.ToLower(classified.Message), "timeout") {
		t.Fatalf("malformed apply classification = %#v", classified)
	}
	// Non-timeout proof loss must omit deadline metadata instead of presenting a
	// configured budget as an elapsed duration.
	if _, present := classified.Details["timeout_ms"]; present {
		t.Fatalf("malformed apply classification retained timeout metadata: %#v", classified)
	}
}

// TestImportApplyCommittedPartialRecordsMutationEvidence proves a post-commit
// Engine failure remains an error while still leaving a durable OTEL audit event.
func TestImportApplyCommittedPartialRecordsMutationEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusFailedDependency)
		// The Engine owns this reviewed envelope, so the CLI can preserve its
		// recovery contract without recording the diagnostic text in telemetry.
		_, _ = w.Write([]byte(`{"error":{"code":"import_workspace_activation_failed","message":"The service was published, but workspace activation failed.","category":"partial","phase":"workspace_activation","operation_id":"` + importApplyTestPlanID + `","request_id":"request-1","commit_state":"committed","recovery":"fused-cli workspace service add chargebee --apply"}}`))
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := provider.Tracer("test").Start(t.Context(), "cli.import.apply")
	command := &cobra.Command{Use: "apply"}
	command.SetContext(ctx)
	command.SetOut(&strings.Builder{})
	err := runImportApply(command, importSpecApplyOptions{receiptPath: writeImportTimeoutReceipt(t, server.URL)})
	span.End()

	var apiError *api.APIError
	// A committed partial must not be rewritten as outcome-unknown or success.
	if !errors.As(err, &apiError) || apiError.CommitState != "committed" || apiError.Phase != "workspace_activation" {
		t.Fatalf("committed partial = %T %v", err, err)
	}
	spans := exporter.GetSpans()
	if got := countAppliedChangeEvents(spans); got != 1 {
		t.Fatalf("applied change event count = %d, want 1", got)
	}
	assertImportPlanSpanAttribute(t, spans, "outcome", "partial")
	assertImportPlanSpanAttribute(t, spans, "failure_phase", "workspace_activation")
	// User-facing diagnostics are deliberately absent from bounded audit data.
	if strings.Contains(spanText(spans), apiError.Message) || strings.Contains(spanText(spans), apiError.Recovery) {
		t.Fatalf("telemetry contains Engine diagnostic text: %s", spanText(spans))
	}
}

// writeImportTimeoutReceipt preserves the real receipt boundary so the test
// exercises slug-based recovery guidance instead of constructing hidden state.
func writeImportTimeoutReceipt(t *testing.T, engineURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipt.json")
	receipt := importPlanReceipt{Slug: "large-api", PlanID: importApplyTestPlanID, ReviewHash: "review-large", EngineURL: engineURL}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertUnknownImportApplyTimeout verifies both the human and structured
// contracts while keeping retry count explicit.
func assertUnknownImportApplyTimeout(t *testing.T, command *cobra.Command, err error, requests int32) {
	t.Helper()
	var unknown *importApplyOutcomeUnknownError
	if !errors.As(err, &unknown) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("apply timeout = %T %v, want outcome-unknown deadline", err, err)
	}
	if requests != 1 {
		t.Fatalf("apply request count = %d, want one", requests)
	}
	if !strings.Contains(err.Error(), "fused-cli import status "+importApplyTestPlanID) {
		t.Fatalf("timeout remediation = %q", err)
	}
	assertTimeoutErrorHidesEngineURL(t, err)
	classified := classifyCommandError(command, err)
	if classified.Code != "import_apply_outcome_unknown" || classified.Retryable || classified.CommitState != "unknown" || classified.Recovery == "" {
		t.Fatalf("classified timeout = %#v", classified)
	}
}

// assertTimeoutErrorHidesEngineURL prevents Go transport errors from exposing
// a private Engine endpoint through the human timeout message.
func assertTimeoutErrorHidesEngineURL(t *testing.T, err error) {
	t.Helper()
	// Transport errors can embed the full URL, so neither HTTP scheme may render.
	if strings.Contains(err.Error(), "http://") || strings.Contains(err.Error(), "https://") {
		t.Fatalf("timeout error leaked Engine URL: %q", err)
	}
}

// TestImportApplyOutcomeUnknownSanitizesOperationID prevents edited receipts
// from injecting terminal text into the recommended status command.
func TestImportApplyOutcomeUnknownSanitizesOperationID(t *testing.T) {
	err := &importApplyOutcomeUnknownError{
		cause: context.DeadlineExceeded, timeout: time.Minute,
		operationID: "bad` && echo secret",
	}
	if strings.Contains(err.Error(), "echo secret") || !strings.Contains(err.Error(), "fused-cli import status <operation-id>") {
		t.Fatalf("unsafe timeout remediation = %q", err)
	}
}

// TestClassifyImportAPIErrorPreservesRecoveryFields keeps the Registry's slim
// safe error contract intact in CLI JSON output.
func TestClassifyImportAPIErrorPreservesRecoveryFields(t *testing.T) {
	apiError := &api.APIError{
		Code: "IMPORT_APPLY_FAILED", Message: "failed to apply import plan",
		Phase: "registry_apply", OperationID: "11111111-1111-4111-8111-111111111111",
		CommitState: "unknown", Recovery: "fused-cli import status 11111111-1111-4111-8111-111111111111",
	}
	result := classifyCommandError(&cobra.Command{Use: "apply"}, apiError)
	if result.Phase != apiError.Phase || result.OperationID != apiError.OperationID || result.CommitState != apiError.CommitState || result.Recovery != apiError.Recovery {
		t.Fatalf("classified import error = %#v", result)
	}
}

// TestRunImportStatusPrintsCommittedResult verifies the composite recovery
// command reads status once and renders the stored service identity.
func TestRunImportStatusPrintsCommittedResult(t *testing.T) {
	operationID := "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Whitespace accepted by the CLI must not survive into the route segment.
		if r.URL.Path != "/integrations/import/operations/"+operationID {
			t.Fatalf("status path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(api.SpecImportStatusResponse{
			Status: "applied", OperationID: operationID,
			Phase: "complete", CommitState: "committed", ServiceID: "svc-1",
			Version: "2026-08-25", Revision: 4,
		})
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)
	command := &cobra.Command{Use: "status"}
	output := &strings.Builder{}
	command.SetOut(output)
	if err := runImportStatus(command, "  "+operationID+"  "); err != nil {
		t.Fatalf("runImportStatus: %v", err)
	}
	if !strings.Contains(output.String(), "applied (complete, committed)") || !strings.Contains(output.String(), "Service svc-1 · version 2026-08-25 · revision 4") {
		t.Fatalf("status output = %q", output)
	}
}

// TestRunImportStatusPrintsTerminalRecovery ensures failed and incomplete
// committed operations show a stable code without a blank guessed service row.
func TestRunImportStatusPrintsTerminalRecovery(t *testing.T) {
	operationID := "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.SpecImportStatusResponse{
			Status: "applied", OperationID: operationID,
			Phase: "failed", CommitState: "committed", Code: "IMPORT_RESULT_UNAVAILABLE",
			Recovery: "fused-cli import plan --help",
		})
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)
	command := &cobra.Command{Use: "status"}
	output := &strings.Builder{}
	command.SetOut(output)
	if err := runImportStatus(command, operationID); err != nil {
		t.Fatalf("runImportStatus: %v", err)
	}
	if !strings.Contains(output.String(), "Code: IMPORT_RESULT_UNAVAILABLE") || !strings.Contains(output.String(), "fused-cli import plan --help") || strings.Contains(output.String(), "Service  ·") {
		t.Fatalf("terminal status output = %q", output)
	}
}

// TestRunImportStatusUsesSharedJSONOutput verifies status reads use the common
// command-local flag and encoder instead of maintaining parallel JSON state.
func TestRunImportStatusUsesSharedJSONOutput(t *testing.T) {
	operationID := "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.SpecImportStatusResponse{
			Status: "pending", OperationID: operationID,
			Phase: "ready", CommitState: "unknown", Guidance: "import apply is in progress; poll status again",
		})
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)
	command := &cobra.Command{Use: "status"}
	addJSONOutputFlag(command)
	// Setting the shared flag is the only supported structured-output switch;
	// no package-global status boolean should influence this direct command.
	if err := command.Flags().Set(jsonOutputFlag, "true"); err != nil {
		t.Fatal(err)
	}
	output := &strings.Builder{}
	command.SetOut(output)
	// The direct runner must observe the command-local flag without Cobra's
	// package-global execution state.
	if err := runImportStatus(command, operationID); err != nil {
		t.Fatalf("runImportStatus JSON: %v", err)
	}
	// Pending JSON carries poll guidance without presenting the same command as terminal recovery.
	if !strings.Contains(output.String(), `"operation_id":"`+operationID+`"`) || !strings.Contains(output.String(), `"guidance":"import apply is in progress; poll status again"`) || strings.Contains(output.String(), `"recovery"`) {
		t.Fatalf("shared status JSON output = %q", output)
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

// TestRunImportPlanWritesExplicitReceiptInJSONMode verifies structured output
// preserves Registry response metadata while receipt persistence stays explicit.
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
		name: "Widgets", slug: "widgets", target: "webhooks", destinationVersion: "1.0",
		jsonOut: true, receiptOut: receiptPath,
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
	// Source version is part of the reviewed response and must survive raw JSON output.
	if !strings.Contains(out.String(), `"review_hash":"review-1"`) || !strings.Contains(out.String(), `"adapter_version":"openapi-v2"`) || !strings.Contains(out.String(), `"source_version":"source-v2"`) {
		t.Fatalf("JSON output omitted Registry review metadata: %s", out.String())
	}
}
