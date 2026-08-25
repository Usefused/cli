package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestPlanSpecImport_PostsToImportPlanEndpoint verifies PlanSpecImport sends
// a POST with the expected body/headers to /integrations/import/plan and
// decodes the plan response, including a populated usage block.
func TestPlanSpecImport_PostsToImportPlanEndpoint(t *testing.T) {
	var reqMethod, reqPath, authHeader string
	var decoded api.SpecImportPlanRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMethod = r.Method
		reqPath = r.URL.Path
		authHeader = r.Header.Get("x-api-key")
		json.NewDecoder(r.Body).Decode(&decoded)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(api.SpecImportPlanResponse{
			PlanID:           "plan-1",
			SourceHash:       "hash-1",
			OverlayPresent:   true,
			OverlayHash:      "overlay-1",
			ReviewHash:       "review-1",
			SourceBundleHash: "bundle-1",
			SourceFormat:     "swagger2",
			AdapterVersion:   "swagger2-v1",
			ServiceID:        "svc-1",
			Slug:             "widgets",
			Name:             "Widgets",
			IsNewService:     false,
			Action:           "update_version",
			TargetVersion:    "1.0",
			Diff: api.SpecImportDiff{
				Added: 1, Changed: 2, Removed: 0,
				ChangedNames: []string{"listWidgets"},
			},
			Usage: &api.SpecImportUsage{
				SDKs:       []api.SpecImportSDKUsage{{ID: "sdk-1", Name: "widgets-sdk", UsesChangedEndpoint: true}},
				Workspaces: []api.SpecImportWorkspaceUsage{{ID: "ws-1", Name: "prod"}},
			},
		})
	}))
	defer srv.Close()

	overlay := "operations:\r\n  listWidgets: {}\n"
	client := api.NewClient(srv.URL, "test-key")
	resp, err := client.PlanSpecImport(api.SpecImportPlanRequest{
		Name:           "Widgets",
		Slug:           "widgets",
		Version:        "1.0",
		SourceContent:  `{"openapi":"3.0.0"}`,
		OverlayContent: &overlay,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSpecImportPlanRequest(t, reqMethod, reqPath, authHeader, decoded)
	assertSpecImportPlanResponse(t, resp)
}

func assertSpecImportPlanRequest(t *testing.T, method, path, authHeader string, decoded api.SpecImportPlanRequest) {
	t.Helper()
	if method != "POST" {
		t.Errorf("expected POST, got %s", method)
	}
	if path != "/integrations/import/plan" {
		t.Errorf("expected /integrations/import/plan, got %s", path)
	}
	if authHeader != "test-key" {
		t.Errorf("expected auth header test-key, got %s", authHeader)
	}
	if decoded.Name != "Widgets" || decoded.Slug != "widgets" {
		t.Errorf("expected name/slug to reach the server, got %+v", decoded)
	}
	if decoded.Version != "1.0" {
		t.Errorf("expected explicit version to reach the server, got %+v", decoded)
	}
	if decoded.OverlayContent == nil || *decoded.OverlayContent != "operations:\r\n  listWidgets: {}\n" {
		t.Errorf("expected exact overlay content to reach the server, got %+v", decoded.OverlayContent)
	}
}

func assertSpecImportPlanResponse(t *testing.T, resp *api.SpecImportPlanResponse) {
	t.Helper()
	assertSpecImportPlanIdentity(t, resp)
	assertSpecImportPlanReview(t, resp)
	assertSpecImportPlanUsage(t, resp)
}

func assertSpecImportPlanIdentity(t *testing.T, resp *api.SpecImportPlanResponse) {
	t.Helper()
	if resp.PlanID != "plan-1" || resp.Action != "update_version" || resp.TargetVersion != "1.0" || resp.SourceFormat != "swagger2" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// assertSpecImportPlanReview checks every reviewed identity returned by the
// Registry so JSON decoding cannot silently discard future replay metadata.
func assertSpecImportPlanReview(t *testing.T, resp *api.SpecImportPlanResponse) {
	t.Helper()
	if !resp.OverlayPresent || resp.ReviewHash != "review-1" || resp.OverlayHash != "overlay-1" || resp.SourceBundleHash != "bundle-1" || resp.AdapterVersion != "swagger2-v1" {
		t.Errorf("expected review metadata to decode, got %+v", resp)
	}
}

func assertSpecImportPlanUsage(t *testing.T, resp *api.SpecImportPlanResponse) {
	t.Helper()
	if resp.Usage == nil || len(resp.Usage.SDKs) != 1 || !resp.Usage.SDKs[0].UsesChangedEndpoint {
		t.Errorf("expected usage block to decode, got %+v", resp.Usage)
	}
}

func TestPlanSpecImportDecodesRegistrySourceFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plan_id":"plan-1","source_format":"swagger2"}`))
	}))
	defer server.Close()

	response, err := api.NewClient(server.URL, "test-key").PlanSpecImport(api.SpecImportPlanRequest{Name: "Widgets"})
	if err != nil {
		t.Fatalf("PlanSpecImport: %v", err)
	}
	if response.SourceFormat != "swagger2" {
		t.Fatalf("source format = %q, want Registry value %q", response.SourceFormat, "swagger2")
	}
}

func TestPlanSpecImportSendsStrictMode(t *testing.T) {
	var request api.SpecImportPlanRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&request)
		_, _ = w.Write([]byte(`{"plan_id":"plan-1"}`))
	}))
	defer server.Close()
	_, err := api.NewClient(server.URL, "test-key").PlanSpecImport(api.SpecImportPlanRequest{Name: "Widgets", Strict: true})
	if err != nil {
		t.Fatalf("PlanSpecImport: %v", err)
	}
	if !request.Strict {
		t.Fatalf("expected strict mode to reach the server: %+v", request)
	}
}

func TestPlanSpecImportDecodesDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(api.SpecImportPlanResponse{Diagnostics: []api.SpecImportDiagnostic{{
			Severity: "warning", Code: "unsupported_request_media_type", Scope: "operation",
			Method: "POST", Path: "/widgets", Message: "Request body media type was not imported.",
			SourceFormat: "openapi", SourceVersion: "3.1.0", Pointer: "/paths/~1widgets/post/requestBody",
			Service: "widgets", Disposition: "diagnosed", RequiredCapability: "http.request.alternatives.v1",
			Provenance: "openapi-source",
		}}})
	}))
	defer server.Close()
	response, err := api.NewClient(server.URL, "test-key").PlanSpecImport(api.SpecImportPlanRequest{Name: "Widgets"})
	if err != nil {
		t.Fatalf("PlanSpecImport: %v", err)
	}
	if len(response.Diagnostics) != 1 || response.Diagnostics[0].Code != "unsupported_request_media_type" {
		t.Fatalf("expected diagnostics to decode, got %+v", response.Diagnostics)
	}
	diagnostic := response.Diagnostics[0]
	if diagnostic.SourceVersion != "3.1.0" || diagnostic.Pointer == "" || diagnostic.RequiredCapability == "" || len(diagnostic.Provenance) == 0 {
		t.Fatalf("expected loss-aware diagnostic metadata to decode, got %+v", diagnostic)
	}
}

func TestPlanSpecImportDecodesStrictRejectionWithoutRawBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{
			"error":"strict_import_rejected",
			"message":"strict import rejected provider contract diagnostics",
			"diagnostics":[{"severity":"warning","code":"unsupported_request_media_type","scope":"operation","method":"POST","path":"/widgets","message":"Request body was skipped."}],
			"unrecognized_secret":"must-not-leak"
		}`))
	}))
	defer server.Close()
	_, err := api.NewClient(server.URL, "test-key").PlanSpecImport(api.SpecImportPlanRequest{Name: "Widgets", Strict: true})
	var strictError *api.SpecImportStrictError
	if !errors.As(err, &strictError) {
		t.Fatalf("expected typed strict rejection, got %v", err)
	}
	if len(strictError.Diagnostics) != 1 || strictError.Diagnostics[0].Code != "unsupported_request_media_type" {
		t.Fatalf("unexpected strict diagnostics: %+v", strictError.Diagnostics)
	}
	if strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("strict rejection leaked an unknown response field: %v", err)
	}
}

// TestPlanSpecImport_HandlesError verifies an owner sees the exact bounded
// parser decision while the stable error category remains machine-readable.
func TestPlanSpecImport_HandlesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"operation \"listCampaigns\" (GET /campaigns): invalid x-fused-pagination: json: unknown field \"items_path\""}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	_, err := client.PlanSpecImport(api.SpecImportPlanRequest{Name: "Widgets", SourceContent: "not a spec"})
	if err == nil {
		t.Fatal("expected an error on 400 response")
	}
	if !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "request_rejected") {
		t.Errorf("expected safe HTTP error category, got: %v", err)
	}
	if !strings.Contains(err.Error(), `unknown field "items_path"`) {
		t.Errorf("expected parser decision in CLI error, got: %v", err)
	}
}

// TestPlanSpecImportDecodesVersionRequiredError proves versionless Postman
// imports retain the Registry's actionable structured contract through Engine.
func TestPlanSpecImportDecodesVersionRequiredError(t *testing.T) {
	// The fake Engine preserves the Registry's nested envelope so this test owns
	// only CLI transport and presentation behavior.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"The imported source does not declare a version.","code":"import_version_required","category":"validation","retryable":false,"remediation":"Enter a version and try again."}}`))
	}))
	defer server.Close()

	_, err := api.NewClient(server.URL, "test-key").PlanSpecImport(api.SpecImportPlanRequest{Name: "Versionless"})
	var apiError *api.APIError
	// The typed code lets human and JSON callers recover without parsing prose.
	if !errors.As(err, &apiError) {
		t.Fatalf("version error = %T %v, want APIError", err, err)
	}
	// The bounded Registry contract must survive the plan wrapper unchanged.
	if apiError.Code != "import_version_required" || apiError.Category != "validation" || apiError.Retryable {
		t.Fatalf("version error = %#v", apiError)
	}
	// Human output needs the correction while avoiding a fabricated default.
	if !strings.Contains(err.Error(), "Enter a version and try again.") {
		t.Fatalf("version remediation = %q", err)
	}
}

// TestApplySpecImport_PostsToImportApplyEndpoint verifies ApplySpecImport
// sends plan_id/review_hash to /integrations/import/apply.
func TestApplySpecImport_PostsToImportApplyEndpoint(t *testing.T) {
	planID := "11111111-1111-4111-8111-111111111111"
	serviceID := "22222222-2222-4222-8222-222222222222"
	versionID := "33333333-3333-4333-8333-333333333333"
	var reqPath string
	var decoded api.SpecImportApplyRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&decoded)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(api.SpecImportApplyResponse{
			Status: "applied", PlanID: planID, OperationID: planID, Phase: "complete", CommitState: "committed",
			ServiceID: serviceID, ServiceVersionID: versionID, Slug: "widgets",
			IsNewService: false, Action: "update_version", Version: "2026-07-14", Revision: 2,
		})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	resp, err := client.ApplySpecImport(planID, "review-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqPath != "/integrations/import/apply" {
		t.Errorf("expected /integrations/import/apply, got %s", reqPath)
	}
	if decoded.PlanID != planID || decoded.ReviewHash != "review-1" {
		t.Errorf("expected plan_id/review_hash to reach the server, got %+v", decoded)
	}
	if resp.Status != "applied" || resp.Action != "update_version" || resp.Version != "2026-07-14" || resp.Revision != 2 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// TestApplySpecImport_HandlesError verifies a review_hash mismatch (or any
// non-2xx) surfaces as an error rather than a silently empty response.
func TestApplySpecImport_HandlesError(t *testing.T) {
	planID := "11111111-1111-4111-8111-111111111111"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"IMPORT_REVIEW_MISMATCH","message":"review receipt changed","phase":"registry_apply","operation_id":"11111111-1111-4111-8111-111111111111","commit_state":"not_committed","recovery":"fused-cli import plan --help"}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	_, err := client.ApplySpecImport(planID, "stale-hash")
	if err == nil {
		t.Fatal("expected an error on 409 response")
	}
	var apiError *api.APIError
	if !errors.As(err, &apiError) || apiError.Code != "IMPORT_REVIEW_MISMATCH" || apiError.CommitState != "not_committed" {
		t.Errorf("expected authoritative safe HTTP error, got: %v", err)
	}
	if strings.Contains(err.Error(), "stale-hash") {
		t.Errorf("expected remote response body to be omitted, got: %v", err)
	}
}

// TestApplySpecImportTreatsUnusablePostOutcomesAsUnknown proves transport loss,
// incomplete 2xx bodies, and invalid success proofs all recover through status.
func TestApplySpecImportTreatsUnusablePostOutcomesAsUnknown(t *testing.T) {
	planID := "11111111-1111-4111-8111-111111111111"
	validProof := `{"status":"applied","plan_id":"` + planID + `","operation_id":"` + planID + `","phase":"complete","commit_state":"committed","service_id":"22222222-2222-4222-8222-222222222222","service_version_id":"33333333-3333-4333-8333-333333333333","slug":"widgets","version":"1.0","revision":1}`
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "connection reset", handler: closeImportApplyConnection},
		{name: "unexpected EOF", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte(`{"status":"applied"}`))
		}},
		{name: "empty 2xx", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }},
		{name: "malformed 2xx", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":`)) }},
		{name: "trailing 2xx", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(validProof + ` trailing`)) }},
		{name: "unstructured proxy error", handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "upstream reset", http.StatusBadGateway) }},
		{name: "identity mismatch", handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Replace(validProof, planID, "44444444-4444-4444-8444-444444444444", 1)))
		}},
		{name: "incomplete proof", handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"applied","plan_id":"` + planID + `"}`))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			_, err := api.NewClient(server.URL, "test-key").ApplySpecImport(planID, "review-1")
			var unknown *api.SpecImportApplyOutcomeUnknownError
			// No unusable POST response may be recorded as a proven local failure or success.
			if !errors.As(err, &unknown) {
				t.Fatalf("apply outcome = %T %v, want outcome unknown", err, err)
			}
		})
	}
}

// closeImportApplyConnection simulates a proxy reset after accepting the POST.
func closeImportApplyConnection(w http.ResponseWriter, _ *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	// httptest's HTTP/1 server must support hijacking for a deterministic reset fixture.
	if !ok {
		return
	}
	connection, _, err := hijacker.Hijack()
	// A successful hijack closes without an HTTP response, matching a proxy reset.
	if err == nil {
		_ = connection.Close()
	}
}

// TestGetSpecImportStatusReadsDurableOperation verifies the recovery client
// uses the read-only operation route and preserves committed result fields.
func TestGetSpecImportStatusReadsDurableOperation(t *testing.T) {
	operationID := "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Status recovery must never replay the apply mutation.
		if r.Method != http.MethodGet || r.URL.Path != "/integrations/import/operations/"+operationID {
			t.Fatalf("status request = %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(api.SpecImportStatusResponse{
			Status: "applied", OperationID: operationID,
			Phase: "complete", CommitState: "committed", ServiceID: "svc-1",
			Version: "2026-08-25", Revision: 2,
		})
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "test-key")
	status, err := client.GetSpecImportStatus(operationID)
	if err != nil || status.CommitState != "committed" || status.ServiceID != "svc-1" {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
}

// TestImportSafeErrorPreservesRecoveryContract verifies structured Registry
// apply errors remain machine-readable through the shared HTTP error parser.
func TestImportSafeErrorPreservesRecoveryContract(t *testing.T) {
	body := []byte(`{"error":{"code":"IMPORT_APPLY_FAILED","message":"failed to apply import plan","phase":"registry_apply","operation_id":"11111111-1111-4111-8111-111111111111","commit_state":"unknown","recovery":"fused-cli import status 11111111-1111-4111-8111-111111111111"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	_, err := api.NewClient(server.URL, "test-key").ApplySpecImport("plan-1", "review-1")
	var apiError *api.APIError
	if !errors.As(err, &apiError) || apiError.Phase != "registry_apply" || apiError.OperationID == "" || apiError.CommitState != "unknown" || apiError.Recovery == "" {
		t.Fatalf("safe import error = %#v", apiError)
	}
}
