package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// cloudImportTestCapture records headers and payload flags received by the mock Engine import endpoints.
type cloudImportTestCapture struct {
	planTargetHeader  string
	applyTargetHeader string
	planIsPublic      *bool
}

// newMockCloudImportServer serves deterministic plan and apply responses while recording Cloud routing headers.
func newMockCloudImportServer(t *testing.T, planUUID string, capture *cloudImportTestCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route mock requests by path so plan and apply independently verify header propagation.
		switch r.URL.Path {
		case "/integrations/import/plan":
			capture.planTargetHeader = r.Header.Get("X-Fused-Registry-Target")
			var body struct {
				IsPublic *bool `json:"is_public"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			capture.planIsPublic = body.IsPublic
			w.Header().Set("X-Fused-Registry-Source", "cloud")
			_, _ = w.Write([]byte(`{"plan_id":"` + planUUID + `","review_hash":"rev-cloud","slug":"linear","name":"Linear","target_version":"1.0.0","action":"create_service","source_format":"openapi3","repair_mode":"safe","diff":{"added":1}}`))
		case "/integrations/import/apply":
			capture.applyTargetHeader = r.Header.Get("X-Fused-Registry-Target")
			_, _ = w.Write([]byte(`{"status":"applied","plan_id":"` + planUUID + `","operation_id":"` + planUUID + `","phase":"complete","commit_state":"committed","service_id":"22222222-2222-2222-2222-222222222222","service_version_id":"33333333-3333-3333-3333-333333333333","slug":"linear","is_new_service":true,"action":"create_service","version":"1.0.0","revision":1}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestImportPlanCloudRejectsExplicitPrivate verifies --cloud refuses --public=false before any network call.
func TestImportPlanCloudRejectsExplicitPrivate(t *testing.T) {
	specFile := filepath.Join(t.TempDir(), "openapi.json")
	// Persist a minimal valid OpenAPI document so request validation reaches the visibility policy check.
	if err := os.WriteFile(specFile, []byte(`{"openapi":"3.1.0","info":{"title":"Linear","version":"1.0.0"},"paths":{}}`), 0o600); err != nil {
		t.Fatalf("write spec fixture: %v", err)
	}
	falseVal := false
	// Reject private visibility locally because Fused Cloud Registry imports from Hybrid Engines must be public.
	if _, err := buildSpecImportRequest(specFile, importSpecPlanOptions{
		name: "Linear", slug: "linear", cloud: true, isPublic: &falseVal,
	}); err == nil {
		t.Fatal("expected error when combining --cloud with --public=false")
	}
}

// TestImportPlanCloudFlagAndReceiptReplay verifies --cloud public defaulting, header routing, and receipt replay on apply.
func TestImportPlanCloudFlagAndReceiptReplay(t *testing.T) {
	tmpDir := t.TempDir()
	specFile := filepath.Join(tmpDir, "openapi.json")
	receiptFile := filepath.Join(tmpDir, "import.plan.json")
	// Write a minimal OpenAPI fixture so local file loading succeeds prior to HTTP planning.
	if err := os.WriteFile(specFile, []byte(`{"openapi":"3.1.0","info":{"title":"Linear","version":"1.0.0"},"paths":{}}`), 0o600); err != nil {
		t.Fatalf("write spec fixture: %v", err)
	}

	const planUUID = "11111111-1111-1111-1111-111111111111"
	var capture cloudImportTestCapture
	server := newMockCloudImportServer(t, planUUID, &capture)
	defer server.Close()

	t.Setenv("FUSED_ENGINE_URL", server.URL)
	t.Setenv("FUSED_API_KEY", "fsk_test_key")

	var out bytes.Buffer
	planCmd := &cobra.Command{}
	planCmd.SetOut(&out)
	planCmd.SetErr(&out)

	// Plan with --cloud must succeed, send X-Fused-Registry-Target: cloud, and default is_public=true.
	if err := runImportPlan(planCmd, specFile, importSpecPlanOptions{
		name: "Linear", slug: "linear", cloud: true, receiptOut: receiptFile, repairMode: "safe",
	}); err != nil {
		t.Fatalf("runImportPlan(--cloud) failed: %v", err)
	}
	assertCloudPlanAndReceiptReplay(t, receiptFile, &out, &capture)
}

// assertCloudPlanAndReceiptReplay checks recorded plan state and verifies apply replays the receipt's Cloud target.
func assertCloudPlanAndReceiptReplay(t *testing.T, receiptFile string, out *bytes.Buffer, capture *cloudImportTestCapture) {
	t.Helper()
	// Planning with --cloud must transmit the explicit Cloud Registry target header.
	if capture.planTargetHeader != "cloud" {
		t.Fatalf("expected X-Fused-Registry-Target=cloud on plan, got %q", capture.planTargetHeader)
	}
	// Omitting --public alongside --cloud must default is_public=true in the request payload.
	if capture.planIsPublic == nil || !*capture.planIsPublic {
		t.Fatalf("expected --cloud to default is_public=true, got %v", capture.planIsPublic)
	}
	receipt, err := readImportPlanReceiptFile(receiptFile)
	// Receipt persistence must succeed so apply can replay the exact Registry target.
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	// The durable receipt must preserve registry_target=cloud for follow-up apply and status commands.
	if receipt.RegistryTarget != "cloud" {
		t.Fatalf("expected receipt registry_target=cloud, got %q", receipt.RegistryTarget)
	}
	applyCmd := &cobra.Command{}
	applyCmd.SetOut(out)
	applyCmd.SetErr(out)
	// Applying from the receipt without repeating --cloud must still succeed against the Cloud Registry.
	if err := runImportApply(applyCmd, importSpecApplyOptions{receiptPath: receiptFile}); err != nil {
		t.Fatalf("runImportApply failed: %v", err)
	}
	// Apply must replay X-Fused-Registry-Target: cloud from the persisted receipt.
	if capture.applyTargetHeader != "cloud" {
		t.Fatalf("expected X-Fused-Registry-Target=cloud on apply replay, got %q", capture.applyTargetHeader)
	}
}
