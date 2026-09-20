package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDisableManagedAuthUsesWorkspaceControl preserves authentication and pending cleanup details for CLI automation.
func TestDisableManagedAuthUsesWorkspaceControl(t *testing.T) {
	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"disabled","revocation_pending":true}`))
	}))
	defer server.Close()
	result, err := NewClient(server.URL, "fixture-control-key").DisableManagedAuth()
	// A failed request cannot be treated as saved opt-out.
	if err != nil {
		t.Fatal(err)
	}
	// Keep both local state and remote cleanup information available to automation.
	if result.Status != "disabled" || !result.RevocationPending {
		t.Fatalf("unexpected disable status: %+v", result)
	}
	request := <-requests
	// Disable must use the authenticated workspace control route, never the broker administrator surface.
	if request.Method != http.MethodDelete || request.URL.Path != "/workspace/managed-auth" || request.Header.Get("x-api-key") != "fixture-control-key" {
		t.Fatal("disable request did not use the expected authenticated workspace route")
	}
}
