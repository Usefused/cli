package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListSDKExecutionEventsUsesCanonicalEngineActivityField verifies receipt query routing.
func TestListSDKExecutionEventsUsesCanonicalEngineActivityField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/engine/graphql" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(request.Query, "appExecutionEvents") || !strings.Contains(request.Query, `transport: "sdk"`) {
			t.Fatalf("query = %s", request.Query)
		}
		if request.Variables["appId"] != "app-1" || request.Variables["includeAllVersions"] != true {
			t.Fatalf("variables = %#v", request.Variables)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"appExecutionEvents":{"total":1,"items":[{"id":"receipt-1","app_family_id":"family-1","app_id":"app-1","app_version":"1.0.0","app_kind":"sdk","transport":"sdk","direction":"outbound","operation":"probe","status":"success","latency_ms":31,"attempt_count":1,"auth_scheme_types":[],"auth_scheme_count":0,"started_at":"2026-08-17T12:00:00Z","ended_at":"2026-08-17T12:00:00Z","timings":[]}]}}}`))
	}))
	defer server.Close()

	page, err := NewClient(server.URL, "fsk_test").ListSDKExecutionEvents("app-1", AppExecutionEventOptions{
		IncludeAllVersions: true,
		PageOptions:        PageOptions{Limit: 10, Offset: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "receipt-1" {
		t.Fatalf("page = %#v", page)
	}
}
