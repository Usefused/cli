package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListSDKExecutionEventsIncludesRESTIngress verifies SDK activity does not hide REST execution receipts.
func TestListSDKExecutionEventsIncludesRESTIngress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(serveSDKActivityRESTReceipt(t)))
	defer server.Close()

	page, err := NewClient(server.URL, "fsk_test").ListSDKExecutionEvents("app-1", AppExecutionEventOptions{
		IncludeAllVersions: true,
		PageOptions:        PageOptions{Limit: 10, Offset: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSDKActivityRESTPage(t, page)
}

// serveSDKActivityRESTReceipt validates the outgoing query before returning one REST-backed SDK receipt.
func serveSDKActivityRESTReceipt(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		// The SDK activity client must use Engine's account- and app-scoped GraphQL boundary.
		if r.URL.Path != "/engine/graphql" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		// A malformed client request cannot prove the transport filter was removed.
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		assertSDKActivityRequest(t, request.Query, request.Variables)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"appExecutionEvents":{"total":1,"items":[{"id":"receipt-1","app_family_id":"family-1","app_id":"app-1","app_version":"1.0.0","app_kind":"sdk","transport":"rest","direction":"outbound","operation":"probe","status":"success","latency_ms":31,"attempt_count":1,"auth_scheme_types":[],"auth_scheme_count":0,"started_at":"2026-08-17T12:00:00Z","ended_at":"2026-08-17T12:00:00Z","timings":[]}]}}}`))
	}
}

// assertSDKActivityRequest proves app scoping remains while transport scoping is absent.
func assertSDKActivityRequest(t *testing.T, query string, variables map[string]any) {
	t.Helper()
	// The canonical app receipt field remains the only activity source.
	if !strings.Contains(query, "appExecutionEvents") {
		t.Fatalf("query = %s", query)
	}
	// App identity already scopes the result to an SDK, while omitting transport includes REST ingress.
	if strings.Contains(query, "transport:") {
		t.Fatalf("query = %s", query)
	}
	// The exact app must remain the authorization and version boundary.
	if variables["appId"] != "app-1" {
		t.Fatalf("variables = %#v", variables)
	}
	// Family expansion must survive the broader transport view.
	if variables["includeAllVersions"] != true {
		t.Fatalf("variables = %#v", variables)
	}
}

// assertSDKActivityRESTPage verifies the client decodes the formerly hidden receipt.
func assertSDKActivityRESTPage(t *testing.T, page *AppExecutionEventPage) {
	t.Helper()
	// The server count and decoded page must agree before inspecting the receipt.
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page = %#v", page)
	}
	// A REST receipt is part of SDK activity because the resolved app owns both SDK ingress transports.
	if page.Items[0].ID != "receipt-1" || page.Items[0].Transport != "rest" {
		t.Fatalf("page = %#v", page)
	}
}
