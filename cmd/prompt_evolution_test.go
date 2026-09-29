package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

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
	if err != nil || len(plan.primary.operations) != 2 || plan.primary.extend {
		// Plain capabilities must retain their independent methods and create-only behavior.
		t.Fatalf("unexpected independent plan: %#v %v", plan.primary, err)
	}
}
