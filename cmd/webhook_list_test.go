package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
)

// TestWebhookListSearchUsesOneEnginePage covers the real command flags without Registry/service catalogue requests.
func TestWebhookListSearchUsesOneEnginePage(t *testing.T) {
	previousFlags, previousQuery, previousService := webhookListFlags, webhookListQuery, webhookListServiceID
	// Command globals must not leak the chosen page into another test invocation.
	t.Cleanup(func() {
		webhookListFlags, webhookListQuery, webhookListServiceID = previousFlags, previousQuery, previousService
	})
	calls := 0
	// A single page response carries a public URL different from the CLI's Engine endpoint.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := decodeTestGraphQLBody(t, r)
		// Search must reach the shared resolver as server-side filters and exact page bounds.
		if r.URL.Path != "/engine/graphql" || !strings.Contains(body.Query, "workspaceWebhookPage(") || body.Variables["search"] != "invoice" || body.Variables["limit"] != float64(5) || body.Variables["offset"] != float64(10) || body.Variables["serviceId"] != "11111111-1111-4111-8111-111111111111" {
			t.Errorf("unexpected listing request: %+v", body)
		}
		_, _ = w.Write([]byte(`{"data":{"workspaceWebhookPage":{"total":11,"items":[{"label":"invoice","callback_url":"https://public.example/webhook/invoice","delivery_mode":"direct","signature":"set"}]}}}`))
	}))
	defer server.Close()
	output := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"webhook", "list", "--q", "invoice", "--service-id", "11111111-1111-4111-8111-111111111111", "--limit", "5", "--offset", "10", "--json"})
	var page cliapi.WorkspaceWebhookPage
	// JSON includes filtered totals for automation and preserves canonical Engine destinations.
	if err := json.Unmarshal([]byte(output), &page); err != nil || calls != 1 || page.Total != 11 || len(page.Items) != 1 || page.Items[0].CallbackURL != "https://public.example/webhook/invoice" {
		t.Fatalf("output=%s calls=%d error=%v", output, calls, err)
	}
}
