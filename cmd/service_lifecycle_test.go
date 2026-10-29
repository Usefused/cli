package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
)

const lifecycleTestID = "00000000-0000-4000-8000-000000000001"

// executeRegistryLifecycle uses a fresh command tree so confirmation cannot leak between test invocations.
func executeRegistryLifecycle(t *testing.T, endpoint string, args ...string) (string, error) {
	t.Helper()
	oldURL, oldKey := EngineURL, APIKey
	EngineURL, APIKey = endpoint, "fsk_test"
	// Restore shared client configuration even when an assertion aborts this test.
	defer func() { EngineURL, APIKey = oldURL, oldKey }()
	command := newServiceRegistryCommand()
	command.SilenceUsage, command.SilenceErrors = true, true
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	command.SetArgs(args)
	err := command.Execute()
	return out.String(), err
}

// lifecycleMutationHandler distinguishes lookup from mutation and checks the exact scheduled instant.
func lifecycleMutationHandler(t *testing.T, requests *[]string) http.HandlerFunc {
	t.Helper()
	// Inspect actual wire requests so command parsing and client behavior are tested together.
	return func(w http.ResponseWriter, r *http.Request) {
		*requests = append(*requests, r.Method+" "+r.URL.Path)
		// Every request must use the configured Engine control credential.
		if r.Header.Get("x-api-key") != "fsk_test" {
			t.Error("missing Engine credential")
		}
		// Slug lookup uses normal Registry identity resolution without loading workspace membership.
		if r.URL.Path == "/graphql" {
			assertLifecycleLookup(t, r)
			_, _ = io.WriteString(w, `{"data":{"service":{"id":"`+lifecycleTestID+`","name":"Demo","slug":"demo","is_owner":true}}}`)
			return
		}
		// Only scheduling carries a JSON body, with UTC precision matching Registry storage.
		if r.Method == http.MethodPut {
			var body map[string]string
			// A decoding failure must not hide a malformed request behind a successful mock response.
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			// The offset must be converted, not discarded; unrelated fields must stay absent.
			if len(body) != 1 || body["delete_at"] != "2099-05-01T09:00:00.123456Z" || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("unexpected scheduling payload: %v", body)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// assertLifecycleLookup checks the namespace without letting fixture complexity obscure mutation assertions.
func assertLifecycleLookup(t *testing.T, r *http.Request) {
	t.Helper()
	var body struct{ Variables map[string]any }
	// Decode the actual qualifier so the mutation cannot target a same-named owned service.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	// Provider and slug remain separate immutable lookup inputs.
	if body.Variables["id"] != "demo" || body.Variables["provider"] != "acme" {
		t.Errorf("unexpected lookup: %v", body.Variables)
	}
}

// TestServiceRegistryLifecycleFlow verifies real CLI parsing, exact resolution, HTTP mutations and JSON receipts together.
func TestServiceRegistryLifecycleFlow(t *testing.T) {
	var requests []string
	server := httptest.NewServer(lifecycleMutationHandler(t, &requests))
	defer server.Close()
	for _, tc := range []struct {
		action string
		args   []string
		path   string
	}{
		{"deprecate", []string{"deprecate", "@acme/demo", "--delete-at", "2099-05-01T10:00:00.123456789+01:00", "--yes", "--json"}, "PUT /integrations/" + lifecycleTestID + "/lifecycle"},
		{"cancel-deletion", []string{"cancel-deletion", lifecycleTestID, "--json"}, "DELETE /integrations/" + lifecycleTestID + "/lifecycle"},
		{"delete", []string{"delete", lifecycleTestID, "--yes", "--json"}, "DELETE /integrations/" + lifecycleTestID},
	} {
		// Independent command instances mirror separate shell invocations.
		t.Run(tc.action, func(t *testing.T) {
			out, err := executeRegistryLifecycle(t, server.URL, tc.args...)
			// A completed mutation must produce a machine-readable committed receipt.
			if err != nil {
				t.Fatal(err)
			}
			var receipt serviceLifecycleReceipt
			// Reject mixed human text and JSON in agent output.
			if err := json.Unmarshal([]byte(out), &receipt); err != nil {
				t.Fatal(err)
			}
			// Scope and exact identity make a receipt distinguishable from local workspace removal.
			if !receipt.OK || receipt.Scope != "registry" || receipt.ServiceID != lifecycleTestID || receipt.Action != tc.action {
				t.Fatalf("unexpected receipt: %+v", receipt)
			}
			// HTTP paths are deliberately different for cancellation and immediate deletion.
			if requests[len(requests)-1] != tc.path {
				t.Fatalf("requests=%v", requests)
			}
		})
	}
	// No successful mutation needs a follow-up read that might report failure after commit.
	if len(requests) != 4 {
		t.Fatalf("unexpected extra requests: %v", requests)
	}
}

// TestServiceRegistryRejectsInvalidIntentBeforeNetwork protects against accidental immediate deletion and timezone ambiguity.
func TestServiceRegistryRejectsInvalidIntentBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"delete", lifecycleTestID},
		{"deprecate", lifecycleTestID, "--delete-at", "2099-01-01T00:00:00Z"},
		{"deprecate", lifecycleTestID, "--yes"},
		{"deprecate", lifecycleTestID, "--delete-at", "2099-01-01", "--yes"},
		{"deprecate", lifecycleTestID, "--delete-at", "2000-01-01T00:00:00Z", "--yes"},
		{"deprecate", lifecycleTestID, "--delete-at", "2099-01-01T00:00:00", "--yes"},
	} {
		out, err := executeRegistryLifecycle(t, ":invalid-engine-url", args...)
		// Local validation must run before even constructing a network request.
		if err == nil || (!strings.Contains(err.Error(), "--yes") && !strings.Contains(err.Error(), "--delete-at")) || out != "" {
			t.Fatalf("args=%v output=%q err=%v", args, out, err)
		}
	}
}

// TestServiceRegistryDeletionDenialHasNoReceipt ensures automation cannot mistake an ownership rejection for a successful deletion.
func TestServiceRegistryDeletionDenialHasNoReceipt(t *testing.T) {
	requests := 0
	// Registry remains authoritative even when an exact ID bypasses catalogue lookup.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"only the service owner can delete this service"}`)
	}))
	defer server.Close()
	out, err := executeRegistryLifecycle(t, server.URL, "delete", lifecycleTestID, "--yes", "--json")
	var apiErr *cliapi.APIError
	// Keep typed authorization errors and stop after one attempt with no credential or scope fallback.
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusForbidden || out != "" || requests != 1 {
		t.Fatalf("requests=%d output=%q err=%v", requests, out, err)
	}
}

// TestServiceRegistryLifecycleReadsDeletedIdentity bypasses live catalogue lookup so tombstones remain inspectable by UUID.
func TestServiceRegistryLifecycleReadsDeletedIdentity(t *testing.T) {
	// A deleted service has no GraphQL definition left; only the lifecycle endpoint should be requested.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Exact ID reads must not require workspace membership or a live Registry definition.
		if r.Method != http.MethodGet || r.URL.Path != "/integrations/"+lifecycleTestID+"/lifecycle" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"service_id":"`+lifecycleTestID+`","name":"Demo","delete_at":null,"deleted_at":"2026-01-01T00:00:00Z"}`)
	}))
	defer server.Close()
	out, err := executeRegistryLifecycle(t, server.URL, "lifecycle", lifecycleTestID, "--json")
	var state cliapi.ServiceLifecycle
	// Decode rather than depending on whitespace choices in the shared JSON renderer.
	if decodeErr := json.Unmarshal([]byte(out), &state); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	// Preserve retained names and deletion metadata without rendering a full service definition.
	if err != nil || state.Name != "Demo" || state.DeletedAt == nil || state.DeleteAt != nil {
		t.Fatalf("output=%q err=%v", out, err)
	}
}

// TestServiceRegistryCommandsAreRegistered verifies the actual CLI exposes the independently tested command tree.
func TestServiceRegistryCommandsAreRegistered(t *testing.T) {
	for _, action := range []string{"lifecycle", "delete", "deprecate", "cancel-deletion"} {
		command, _, err := RootCmd.Find([]string{"service", "registry", action})
		// A parent command fallback does not prove that an actionable leaf exists.
		if err != nil || command.CommandPath() != "fused-cli service registry "+action {
			t.Fatalf("missing lifecycle action %s: %v", action, err)
		}
	}
}
