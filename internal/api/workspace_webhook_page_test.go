package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestWorkspaceWebhookPageUsesUIContract checks filtering, authentication, and canonical destinations on one Engine request.
func TestWorkspaceWebhookPageUsesUIContract(t *testing.T) {
	calls := 0
	// The fixture rejects catalogue lookups and reads of the legacy per-service query.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Query     string
			Variables map[string]any
		}
		// Inspect actual serialization before evaluating filter and transport invariants.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		// CLI and UI must share authorization, filters, and page boundaries at the same resolver.
		if r.URL.Path != "/engine/graphql" || r.Header.Get("x-api-key") != "fixture" || !strings.Contains(body.Query, "workspaceWebhookPage(") || strings.Contains(body.Query, "workspaceWebhooks(") || body.Variables["serviceId"] != "service-1" || body.Variables["search"] != "invoice" || body.Variables["limit"] != float64(20) || body.Variables["offset"] != float64(40) {
			t.Errorf("unexpected request: %+v", body)
		}
		_, _ = w.Write([]byte(`{"data":{"workspaceWebhookPage":{"total":43,"items":[{"service_id":"service-1","service_name":"Billing","service_ref":"@billing/payments","label":"invoice","slug":"internal","callback_url":"https://public.example/webhook/events","delivery_mode":"direct","signature":"set"},{"label":"managed","callback_url":"","delivery_mode":"managed"}]}}}`))
	}))
	defer server.Close()
	page, err := api.NewClient(server.URL, "fixture").ListWorkspaceWebhookPage("service-1", " invoice ", api.PageOptions{Limit: 20, Offset: 40})
	// A page read must not fetch subsequent pages or replace Engine's public destination.
	if err != nil || calls != 1 || page.Total != 43 || len(page.Items) != 2 || page.Items[0].CallbackURL != "https://public.example/webhook/events" || page.Items[1].CallbackURL != "" {
		t.Fatalf("page=%+v err=%v calls=%d", page, err, calls)
	}
}

// TestWorkspaceWebhookPageRejectsMissingData ensures transport and schema failures never become empty success.
func TestWorkspaceWebhookPageRejectsMissingData(t *testing.T) {
	for _, response := range []string{`{"data":{}}`, `{"data":{"workspaceWebhookPage":null}}`, `{"data":{"workspaceWebhookPage":{"items":[]}}}`, `{"data":{"workspaceWebhookPage":{"total":0}}}`, `{"errors":[{"message":"denied"}]}`} {
		// Each malformed result gets its own client and must fail without retries.
		t.Run(response, func(t *testing.T) {
			calls := 0
			// Return only the chosen schema failure; no real Engine credentials are needed.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte(response)) }))
			defer server.Close()
			page, err := api.NewClient(server.URL, "fixture").ListWorkspaceWebhookPage("", "", api.PageOptions{})
			// Invalid pages are not usable even when HTTP delivery succeeded.
			if err == nil || page != nil || calls != 1 {
				t.Fatalf("page=%+v err=%v calls=%d", page, err, calls)
			}
		})
	}
}
