package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestApplyWorkspaceConfigRetainsPartialResults verifies structured receipts survive the deliberately non-success HTTP response.
func TestApplyWorkspaceConfigRetainsPartialResults(t *testing.T) {
	for _, planID := range []string{"plan-1", "different-plan"} {
		// A response for a different plan must never be accepted as this request's recovery proof.
		t.Run(planID, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The exact existing endpoint and reviewed source remain unchanged on resume.
				if r.URL.Path != "/workspace/config/apply" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				var request map[string]any
				// Reject an invalid fixture or outcome rather than accepting unproven recovery behavior.
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				// Reject an invalid fixture or outcome rather than accepting unproven recovery behavior.
				if request["plan_id"] != "plan-1" || request["source_hash"] != "reviewed-hash" {
					t.Error("apply identity changed")
				}
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(ConfigApplyResponse{Status: "partially_applied", PlanID: planID, Services: []WorkspaceApplyServiceResult{{Key: "service:a", Status: "succeeded"}, {Key: "service:b", Status: "failed", ErrorCode: "service_apply_failed"}}})
			}))
			defer server.Close()
			result, err := NewClient(server.URL, "test-key").ApplyWorkspaceConfig("plan-1", "reviewed-hash", nil, nil)
			// Only matching receipts are returned as structured partial outcomes.
			if planID != "plan-1" {
				// Reject an invalid fixture or outcome rather than accepting unproven recovery behavior.
				if err == nil || result != nil {
					t.Fatal("accepted another plan's results")
				}
				return
			}
			// Reject an invalid fixture or outcome rather than accepting unproven recovery behavior.
			if err != nil || result.Status != "partially_applied" || len(result.Services) != 2 {
				t.Fatalf("lost partial outcomes: %#v %v", result, err)
			}
		})
	}
}
