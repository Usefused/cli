package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

// TestWorkspacePartialApplyReportsFailureAndResumesSamePlan proves partial completion is nonzero and never triggers replanning.
func TestWorkspacePartialApplyReportsFailureAndResumesSamePlan(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Recovery must post the same saved plan rather than request fresh version resolution.
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
			t.Error("recovery changed plan identity")
		}
		calls++
		// The first result includes one successful service and one failed service.
		if calls == 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"status":"partially_applied","plan_id":"plan-1","services":[{"key":"service:a","status":"succeeded"},{"key":"service:b","status":"failed"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-1"}`))
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "test-key")
	cfg := &configfile.ParsedConfig{ConfigKey: "workspace"}
	receipt := planReceipt{PlanID: "plan-1", SourceHash: "reviewed-hash"}
	var result error
	// Capture the real human output to guard against an unconditional success message.
	output := captureStdout(t, func() { result = applyWorkspaceConfig(client, cfg, receipt, &workspaceApplyPayload{}) })
	// Reject an invalid fixture or outcome rather than accepting unproven recovery behavior.
	if result == nil || !strings.Contains(output, "service:a: succeeded") || !strings.Contains(output, "service:b: failed") || strings.Contains(output, "Successfully applied") {
		t.Fatalf("misleading partial output: %q error=%v", output, result)
	}
	// Reusing the same receipt is the complete retry interface; no new flag is necessary.
	output = captureStdout(t, func() { result = applyWorkspaceConfig(client, cfg, receipt, &workspaceApplyPayload{}) })
	// Reject an invalid fixture or outcome rather than accepting unproven recovery behavior.
	if result != nil || !strings.Contains(output, "Successfully applied") || calls != 2 {
		t.Fatalf("resume failed: %q %v calls=%d", output, result, calls)
	}
}
