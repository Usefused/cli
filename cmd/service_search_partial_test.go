package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRegistrySearchDoesNotMergeWorkspaceMatches keeps explicit Registry reads independent of workspace membership.
func TestRegistrySearchDoesNotMergeWorkspaceMatches(t *testing.T) {
	sawSearch, sawMembership := false, false
	// Distinct responses make it impossible for exact-name lookup to accidentally satisfy the partial-search assertion.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		// Reject malformed test transport before choosing an authoritative result.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Registry description matches may not share the search text in their name or slug.
		if r.URL.Path == "/graphql" {
			_, _ = w.Write([]byte(`{"data":{"searchServices":[{"id":"registry","name":"Payments","slug":"payments","provider":{"handle":"acme"}}]}}`))
			return
		}
		// CLI must use precisely the same Engine field/argument as the UI.
		if body.Variables["q"] != nil {
			sawSearch = true
			if body.Variables["q"] != "BiLl" || !strings.Contains(body.Query, "q: $q") || body.Variables["names"] != nil {
				t.Errorf("incorrect search request: %#v", body)
			}
			_, _ = w.Write([]byte(`{"data":{"workspaceServicePage":{"total":1,"data":[{"service_id":"local","service_name":"Private Billing","service_slug":"private-billing"}]}}}`))
			return
		}
		sawMembership = true
		_, _ = w.Write([]byte(`{"data":{"workspaceServicePage":{"total":1,"data":[{"service_id":"registry","service_name":"Payments","service_slug":"payments"}]}}}`))
	}))
	defer server.Close()
	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"service", "list", "--registry", "--search", "BiLl", "--json"})
	var results []serviceSearchResult
	// Explicit Registry reads return catalog results without Engine membership calls.
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatal(err)
	}
	if sawSearch || sawMembership || len(results) != 1 {
		t.Fatalf("search=%v membership=%v results=%s", sawSearch, sawMembership, out)
	}
	for _, result := range results {
		// Registry-only output must not claim workspace membership.
		if result.WorkspaceStatus != "" {
			t.Fatalf("unexpected status: %+v", result)
		}
	}
}
