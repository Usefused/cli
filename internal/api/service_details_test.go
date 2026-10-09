package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUpdateServiceDetailsPreservesOmittedFields verifies CLI/UI share the partial-update contract.
func TestUpdateServiceDetailsPreservesOmittedFields(t *testing.T) {
	// Capture the wire request and return the API's successful no-content response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Display edits must not silently target contract import or identity routes.
		if r.Method != http.MethodPatch || r.URL.Path != "/integrations/service-id/details" || r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		// Decode the actual payload so omitted fields cannot become accidental clears.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		// Clearing the description must leave the name absent.
		if len(body) != 1 || body["description"] != "" {
			t.Errorf("body=%v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	empty := ""
	// A successful save must accept 204 without attempting to decode JSON.
	if err := NewClient(server.URL, "test-key").UpdateServiceDetails("service-id", nil, &empty); err != nil {
		t.Fatal(err)
	}
}
