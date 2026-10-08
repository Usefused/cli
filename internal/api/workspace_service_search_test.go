package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSearchWorkspaceServicesPages verifies the free-text filter is retained beyond the first result page.
func TestSearchWorkspaceServicesPages(t *testing.T) {
	calls := 0
	// The second page contains a unique row, detecting accidental unfiltered continuation requests.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string `json:"query"`
			Variables struct {
				Q      string   `json:"q"`
				Limit  int      `json:"limit"`
				Offset int      `json:"offset"`
				Names  []string `json:"names"`
			} `json:"variables"`
		}
		// Malformed payloads cannot prove shared search semantics.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/engine/graphql" || body.Variables.Q != "bIlL" || body.Variables.Limit != 100 || body.Variables.Offset != calls*100 || len(body.Variables.Names) != 0 || !strings.Contains(body.Query, "q: $q") {
			t.Errorf("unexpected query: %+v", body)
		}
		calls++
		rows := []map[string]string{}
		for i := body.Variables.Offset; i < min(body.Variables.Offset+100, 101); i++ {
			rows = append(rows, map[string]string{"service_id": fmt.Sprintf("svc-%d", i), "service_name": "Billing"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"workspaceServicePage": map[string]any{"data": rows, "total": 101}}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key")
	rows, err := client.SearchWorkspaceServices(" bIlL ")
	// Every advertised match must arrive, without converting search text into exact names.
	if err != nil || calls != 2 || len(rows) != 101 {
		t.Fatalf("rows=%d calls=%d err=%v", len(rows), calls, err)
	}
}
