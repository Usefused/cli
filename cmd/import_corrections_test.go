package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestDiscoveryCorrectionReview preserves embedded Registry reviews in the human discovery handoff.
func TestDiscoveryCorrectionReview(t *testing.T) {
	runner, output := discoveryBrowserTestRunner(t, importDiscoveryOptions{receiptOut: filepath.Join(t.TempDir(), "receipt.json")})
	snapshot := &api.DiscoverySnapshot{Payload: json.RawMessage(`{"plan":{"plan_id":"plan-1","review_hash":"review-1"},"import_plan":{"plan_id":"plan-1","review_hash":"review-1","source_extension_inventory":{},"expected_target":{},"webhook_draft":{},"diff":{"settings_changed":true},"diagnostics":[{"message":"Preserved fixed tuple.","disposition":"repaired","pointer":"/components/schemas/tuple","correction":{"rule":"fixed_tuple","version":"1"}}]}}`)}
	// A valid embedded review must be printable without weakening strict discovery decoding.
	if err := runner.finish(snapshot); err != nil {
		t.Fatal(err)
	}
	// Discovery should expose the same source correction as an ordinary file import.
	if !strings.Contains(output.String(), "Corrected: Preserved fixed tuple.") {
		t.Fatalf("missing discovery correction: %s", output.String())
	}
	snapshot.Payload = json.RawMessage(`{"plan":{"plan_id":"plan-1","review_hash":"review-1"},"import_plan":{"plan_id":"plan-2","review_hash":"review-1"}}`)
	// A review for another plan cannot be associated with the saved receipt.
	if err := runner.finish(snapshot); err == nil {
		t.Fatal("accepted mismatched import review")
	}
}

// TestImportCorrectionsHumanReview distinguishes deterministic repairs from incomplete schemas before apply.
func TestImportCorrectionsHumanReview(t *testing.T) {
	var output strings.Builder
	printImportDiagnostics(&output, []api.SpecImportDiagnostic{
		{Message: "Moved the required flag.", Pointer: "/paths/~1cover/put/requestBody", Disposition: "repaired", Correction: &api.SpecImportCorrection{Rule: "scalar_body_required", Version: "1"}},
		{Message: "Error body remains unspecified.", Pointer: "/paths/~1webhooks/post/responses/400", Disposition: "incomplete", Correction: &api.SpecImportCorrection{Rule: "missing_error_schema", Version: "1"}},
	})
	for _, expected := range []string{"Corrected: Moved the required flag.", "Unspecified: Error body remains unspecified.", "/paths/~1cover/put/requestBody", "rule scalar_body_required v1"} {
		// Users must see both the actual correction and any retained limitation.
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q: %s", expected, output.String())
		}
	}
}

// TestReportOnlyReceiptRequiresAcknowledgement prevents older Registries from silently applying repairs.
func TestReportOnlyReceiptRequiresAcknowledgement(t *testing.T) {
	request := api.SpecImportPlanRequest{RepairMode: "report_only"}
	response := &api.SpecImportPlanResponse{ReviewHash: "review"}
	// Missing acknowledgement is unsafe even when an older server returns a valid review hash.
	if normalizeAndValidateImportPlanResponse(request, response) == nil {
		t.Fatal("accepted ignored report-only")
	}
	response.RepairMode = "report_only"
	// Acknowledged mode must survive local receipt persistence.
	if err := normalizeAndValidateImportPlanResponse(request, response); err != nil {
		t.Fatal(err)
	}
	if newImportPlanReceipt(response).RepairMode != "report_only" {
		t.Fatal("lost receipt mode")
	}
	var output strings.Builder
	printImportDiagnostics(&output, []api.SpecImportDiagnostic{{Message: "Change required.", Disposition: "suggested", Correction: &api.SpecImportCorrection{Rule: "required", Version: "1"}}})
	// Human output cannot imply report-only modified the provider's definition.
	if !strings.Contains(output.String(), "Suggested:") || strings.Contains(output.String(), "Corrected:") {
		t.Fatal(output.String())
	}
}
