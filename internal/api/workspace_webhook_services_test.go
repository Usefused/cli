package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListWebhookWorkspaceServicesRequestsServerFilter verifies the CLI delegates webhook-event filtering to Engine's has_webhooks argument.
func TestListWebhookWorkspaceServicesRequestsServerFilter(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode graphql request: %v", err)
		}
		gotQuery, gotVars = payload.Query, payload.Variables
		_, _ = w.Write([]byte(`{"data":{"workspaceServicePage":{"total":1,"data":[{"service_id":"svc-stripe","service_name":"Stripe","service_slug":"@stripe/stripe"}]}}}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTP: server.Client()}
	services, err := client.ListWebhookWorkspaceServices()
	// Backend-filtered pagination must return the authorized webhook-capable services cleanly.
	if err != nil {
		t.Fatalf("ListWebhookWorkspaceServices: %v", err)
	}
	if len(services) != 1 || services[0].ServiceID != "svc-stripe" {
		t.Fatalf("unexpected services: %+v", services)
	}
	// GraphQL query and variables must explicitly request server-side has_webhooks filtering.
	if !strings.Contains(gotQuery, "has_webhooks: $hasWebhooks") || gotVars["hasWebhooks"] != true {
		t.Fatalf("expected has_webhooks: true in query=%q vars=%#v", gotQuery, gotVars)
	}
}
