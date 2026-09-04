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
)

// TestImportJSONPlanThenApply proves JSON planning retains the exact receipt consumed by plain apply.
func TestImportJSONPlanThenApply(t *testing.T) {
	t.Chdir(t.TempDir())
	planCalls, applyCalls := 0, 0
	var applied map[string]string
	// Serve both boundaries so the test detects accidental replanning or changed review identity.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Only the planned receipt may authorize the subsequent apply request.
		switch r.URL.Path {
		case "/integrations/import/plan":
			planCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plan_id": importApplyTestPlanID, "review_hash": "review-json", "source_hash": "source-json",
				"slug": "widgets", "name": "Widgets", "action": "create_service", "target_version": "1.0",
				"target_type": "endpoints", "is_new_service": true,
			})
		case "/integrations/import/apply":
			applyCalls++
			// A malformed apply request cannot count as a successful round trip.
			if err := json.NewDecoder(r.Body).Decode(&applied); err != nil {
				t.Error(err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "applied", "plan_id": importApplyTestPlanID, "operation_id": importApplyTestPlanID,
				"phase": "complete", "commit_state": "committed", "slug": "widgets", "version": "1.0",
				"service_id":         "22222222-2222-4222-8222-222222222222",
				"service_version_id": "33333333-3333-4333-8333-333333333333",
				"is_new_service":     true, "action": "create_service", "revision": 1,
			})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	setImportTestAPI(t, server.URL)
	command := &cobra.Command{}
	command.SetContext(t.Context())
	output := &strings.Builder{}
	command.SetOut(output)
	// No receipt flag is supplied: machine-readable output must preserve the ordinary workflow.
	if err := runImportPlan(command, "", importSpecPlanOptions{
		name: "Widgets", slug: "widgets", url: "https://example.test/openapi.json", jsonOut: true,
	}); err != nil {
		t.Fatal(err)
	}
	var response api.SpecImportPlanResponse
	// Decode the entire stdout value to reject human receipt messages mixed into JSON.
	if err := json.Unmarshal([]byte(output.String()), &response); err != nil {
		t.Fatalf("plan stdout is not a single JSON response: %v", err)
	}
	receipt, err := readImportPlanReceiptFile(defaultImportReceiptPath)
	// The receipt must bind the exact reviewed response and Engine before apply is attempted.
	if err != nil || receipt.PlanID != response.PlanID || receipt.ReviewHash != response.ReviewHash || receipt.EngineURL != server.URL {
		t.Fatalf("receipt mismatch: %+v, error: %v", receipt, err)
	}
	// Empty options exercise the same default receipt resolution as plain import apply.
	if err := runImportApply(command, importSpecApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	// A single plan and exact review hash demonstrate that no recovery fallback was required.
	if planCalls != 1 || applyCalls != 1 || applied["plan_id"] != response.PlanID || applied["review_hash"] != response.ReviewHash {
		t.Fatalf("unexpected round trip: plans=%d applies=%d request=%v", planCalls, applyCalls, applied)
	}
}

// TestImportJSONReceiptDestinations keeps explicit output isolated and prevents false success on failed writes.
func TestImportJSONReceiptDestinations(t *testing.T) {
	t.Chdir(t.TempDir())
	server, _ := newImportPlanTestServer(t)
	defer server.Close()
	setImportTestAPI(t, server.URL)
	command := &cobra.Command{}
	command.SetContext(t.Context())
	output := &strings.Builder{}
	command.SetOut(output)
	opts := importSpecPlanOptions{name: "Widgets", slug: "widgets", url: "https://example.test/openapi.json", jsonOut: true, receiptOut: "custom/plan.json"}
	// A chosen receipt path must remain supported without also changing the default plan.
	if err := runImportPlan(command, "", opts); err != nil {
		t.Fatal(err)
	}
	_, err := readImportPlanReceiptFile(opts.receiptOut)
	// Verify the custom receipt is usable, not merely that its directory exists.
	if err != nil {
		t.Fatal(err)
	}
	// Explicit output must not replace an unrelated default receipt.
	if _, err := os.Stat(defaultImportReceiptPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected default receipt: %v", err)
	}
	// A file in place of the state directory deterministically simulates an unwritable receipt destination.
	if err := os.WriteFile(filepath.Dir(filepath.Dir(defaultImportReceiptPath)), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	opts.receiptOut = ""
	output.Reset()
	// Planning must fail before printing success JSON when its durable receipt cannot be saved.
	if err := runImportPlan(command, "", opts); err == nil || output.Len() != 0 {
		t.Fatalf("receipt write failure was hidden: error=%v stdout=%q", err, output.String())
	}
}
