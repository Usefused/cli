package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestApplicationListsShowUniqueApplications verifies the public table and JSON omit immutable version rows.
func TestApplicationListsShowUniqueApplications(t *testing.T) {
	for _, kind := range []string{"mcp", "sdk"} {
		// Both command adapters must expose the same unique-application semantics.
		t.Run(kind, func(t *testing.T) { testApplicationListShowsUniqueApplications(t, kind) })
	}
}

// testApplicationListShowsUniqueApplications verifies the table and JSON for one application kind.
func testApplicationListShowsUniqueApplications(t *testing.T, kind string) {
	// Engine returns one grouped application rather than immutable-version rows.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLTestRequest(t, r)
		// Application grouping still reads only the authorized MCP catalogue.
		if body.Variables["kind"] != kind {
			t.Errorf("unexpected kind: %#v", body.Variables)
		}
		_, _ = w.Write([]byte(`{"data":{"appFamilies":{"total":1,"items":[{"app_family_id":"family-1","name":"support:tools","version_count":2,"latest_version":"2","latest_version_id":"version-2"}]}}}`))
	}))
	defer server.Close()
	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{kind, "list"})
	// The application appears once and all exact version inspection stays in mcp versions.
	if strings.Count(out, "support:tools") != 1 || strings.Contains(out, "VERSION_ID") || strings.Contains(out, "VERSION-PINNED") || !strings.Contains(out, "VERSIONS") || !strings.Contains(out, "LATEST_VERSION") {
		t.Fatalf("list table: %s", out)
	}
	out = runCommandInDirOutput(t, t.TempDir(), server.URL, []string{kind, "list", "--json"})
	var page struct {
		Total int              `json:"total"`
		Items []map[string]any `json:"items"`
	}
	// JSON must remain parseable without prose mixed into the envelope.
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	// Counts and identities must match the human application listing.
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0]["version_count"] != float64(2) || page.Items[0]["name"] != "support:tools" || page.Items[0]["latest_version"] != "2" {
		t.Fatalf("JSON: %s", out)
	}
	// A family row cannot masquerade as a particular immutable app.
	if _, exists := page.Items[0]["app_id"]; exists {
		t.Fatalf("version identity leaked into application row: %s", out)
	}
}
