package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestApplicationsUseEngineGrouping ensures both adapters request one page of logical applications.
func TestApplicationsUseEngineGrouping(t *testing.T) {
	for _, kind := range []string{"mcp", "sdk"} {
		// SDK and MCP listing must share the Engine-owned grouping contract.
		t.Run(kind, func(t *testing.T) {
			calls := 0
			// Return already grouped metadata to prove the CLI forwards family pagination unchanged.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Query     string         `json:"query"`
					Variables map[string]any `json:"variables"`
				}
				// Malformed transport requests must not manufacture a successful fixture response.
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				// The requested page refers to applications, never offsets in immutable version rows.
				if !strings.Contains(req.Query, "appFamilies(kind:") || req.Variables["kind"] != kind || req.Variables["limit"] != float64(1) || req.Variables["offset"] != float64(4) {
					t.Errorf("unexpected request: %#v", req)
				}
				_, _ = w.Write([]byte(`{"data":{"appFamilies":{"total":8,"items":[{"app_family_id":"family-a","name":"alpha:tools","version_count":101}]}}}`))
			}))
			defer server.Close()
			page, err := NewClient(server.URL, "test-key").ListApplications(kind, PageOptions{Limit: 1, Offset: 4})
			// No additional pages may be fetched or regrouped to compute an application total locally.
			if err != nil || calls != 1 || page.Total != 8 || len(page.Items) != 1 || page.Items[0].VersionCount != 101 || page.Items[0].Name != "alpha:tools" {
				t.Fatalf("page: %#v, calls %d, error %v", page, calls, err)
			}
		})
	}
}

// TestAppVersionsResolvesFamilyBeforeReading ensures targeted history uses canonical MCP identity and version pagination.
func TestAppVersionsResolvesFamilyBeforeReading(t *testing.T) {
	for _, kind := range []string{"mcp", "sdk"} {
		// Resolution must keep SDK and MCP history behind their respective kind boundaries.
		t.Run(kind, func(t *testing.T) { testAppVersionsResolvesFamilyBeforeReading(t, kind) })
	}
}

// testAppVersionsResolvesFamilyBeforeReading checks canonical family resolution before history reads.
func testAppVersionsResolvesFamilyBeforeReading(t *testing.T, kind string) {
	// Return distinct resolution and history responses so a local substring filter cannot satisfy the test.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		// A malformed wire contract must be visible as a test failure.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		// Family resolution must be kind-scoped and preserve punctuation in the app name.
		if strings.Contains(req.Query, "ResolveAppFamilyReference") {
			// Cross-kind resolution would permit SDKs on an MCP command.
			if req.Variables["kind"] != kind || req.Variables["reference"] != "alpha:tools" {
				t.Errorf("resolution: %#v", req.Variables)
			}
			_, _ = fmt.Fprintf(w, `{"data":{"appFamilyReference":{"id":"family-a","kind":%q}}}`, kind)
			return
		}
		// History reads must use the resolved stable identity.
		if req.Variables["appFamilyId"] != "family-a" || !strings.Contains(req.Query, "appVersions(app_family_id:") {
			t.Errorf("history: %#v", req)
		}
		_, _ = w.Write([]byte(`{"data":{"appVersions":[{"app_id":"new","version":"2"},{"app_id":"old","version":"1"}]}}`))
	}))
	defer server.Close()
	page, err := NewClient(server.URL, "test-key").ListAppVersions(kind, "alpha:tools", PageOptions{Limit: 1, Offset: 1})
	// A targeted version offset must retain exact version identity and the complete family total.
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].AppID != "old" {
		t.Fatalf("versions: %#v, %v", page, err)
	}
}
