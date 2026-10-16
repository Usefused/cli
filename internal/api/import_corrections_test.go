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

// TestImportCorrectionTransport retains Registry evidence for JSON clients without recomputing repairs locally.
func TestImportCorrectionTransport(t *testing.T) {
	// The HTTP fixture supplies the same additive diagnostic field as the Registry plan API.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"diagnostics":[{"severity":"info","code":"source_fixed_tuple","scope":"schema","message":"Preserved a fixed tuple.","disposition":"repaired","pointer":"/paths/~1capabilities/get/responses/200","correction":{"rule":"fixed_tuple","version":"1","before_hash":"` + strings.Repeat("a", 64) + `","after_hash":"` + strings.Repeat("b", 64) + `"}}]}`))
	}))
	defer server.Close()
	response, err := api.NewClient(server.URL, "fixture-key").PlanSpecImport(api.SpecImportPlanRequest{Name: "Capabilities"})
	// Transport must retain the correction rather than merely render its prose.
	if err != nil || len(response.Diagnostics) != 1 || response.Diagnostics[0].Correction == nil {
		t.Fatalf("response: %#v error: %v", response, err)
	}
	correction := response.Diagnostics[0].Correction
	// Rule identity and both digests let API consumers reconcile the same reviewed evidence.
	if correction.Rule != "fixed_tuple" || correction.Version != "1" || correction.BeforeHash != strings.Repeat("a", 64) || correction.AfterHash != strings.Repeat("b", 64) {
		t.Fatalf("correction: %#v", correction)
	}
}

// TestLargeReportOnlyReview proves multi-operation suggestions survive the import-specific transport bound.
func TestLargeReportOnlyReview(t *testing.T) {
	diagnostics := make([]api.SpecImportDiagnostic, 120)
	for index := range diagnostics {
		diagnostics[index] = api.SpecImportDiagnostic{Code: "repair", Message: strings.Repeat("review ", 100), Disposition: "suggested", Correction: &api.SpecImportCorrection{Rule: "required", Version: "1"}}
	}
	// A real HTTP envelope reproduces provider reports larger than the generic 64 KiB error ceiling.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "import_repairs_required", "message": "Source corrections required", "diagnostics": diagnostics}})
	}))
	defer server.Close()
	_, err := api.NewClient(server.URL, "test").PlanSpecImport(api.SpecImportPlanRequest{RepairMode: "report_only"})
	var review *api.SpecImportStrictError
	// All suggestions remain machine-readable and are never classified as completed repairs.
	if !errors.As(err, &review) || review.Code != "import_repairs_required" || len(review.Diagnostics) != 120 || review.Diagnostics[0].Disposition != "suggested" {
		t.Fatalf("lost large review: %v", err)
	}
}
