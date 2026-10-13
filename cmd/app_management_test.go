package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSharedAppListTypeFilters keeps filtering on Engine before pagination for all four delivery types.
func TestSharedAppListTypeFilters(t *testing.T) {
	for _, test := range []struct{ public, engine string }{{"sdk", "sdk"}, {"api", "api"}, {"mcp", "mcp"}, {"unified-app", "unified_app"}} {
		t.Run(test.public, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				request := decodeGraphQLTestRequest(t, r)
				if request.Variables["kind"] != test.engine || request.Variables["limit"] != float64(5) || request.Variables["offset"] != float64(10) {
					t.Errorf("unexpected filters: %#v", request.Variables)
				} // A filtered page must retain the original bounds.
				fmt.Fprint(w, `{"data":{"appFamilies":{"total":0,"items":[]}}}`)
			}))
			defer server.Close()
			output := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"app", "list", "--type", test.public, "--limit", "5", "--offset", "10", "--json"})
			if calls != 1 || !json.Valid([]byte(output)) {
				t.Fatalf("calls=%d output=%s", calls, output)
			} // No client-side merge or additional catalog crawl.
		})
	}
}

// TestSharedAppPlanRoutesByConfig exercises the actual command and preserves each Engine plan contract.
func TestSharedAppPlanRoutesByConfig(t *testing.T) {
	for _, test := range []struct{ kind, extra, route string }{{"sdk", "language: typescript", "sdk"}, {"sdk", "language: typescript\ngenerate: false", "sdk"}, {"mcp", "description: Test tools", "mcp"}, {"unified_app", "source: 'export default buildUnifiedApp({});'", "unified-app"}} {
		t.Run(test.kind+test.extra, func(t *testing.T) {
			dir := t.TempDir()
			path := writeSprintConfig(t, dir, "app.yaml", fmt.Sprintf("apiVersion: fused/v1\nkind: %s\nname: tools\nversion: 1.0.0\nbucket: default\n%s\nservices:\n  crm:\n    version: v1\n    operations: [createCustomer]\n", test.kind, test.extra))
			plans := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					fmt.Fprint(w, `{"environment":"development"}`)
					return
				} // Health preflight is read-only.
				if r.URL.Path != "/"+test.route+"-config/plan" {
					t.Errorf("wrong route %s", r.URL.Path)
					http.Error(w, "unexpected", 400)
					return
				} // The config must determine the route.
				plans++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				} // Keep malformed requests visible.
				json.NewEncoder(w).Encode(map[string]any{"plan_id": "plan-shared", "config_key": body["config_key"], "source_hash": body["source_hash"], "summary": map[string]any{}})
			}))
			defer server.Close()
			output := runCommandInDirOutput(t, dir, server.URL, []string{"app", "plan", "-f", path, "--json"})
			if plans != 1 || !json.Valid([]byte(output)) {
				t.Fatalf("plans=%d output=%s", plans, output)
			} // Each app creates exactly its own plan receipt.
		})
	}
}

// TestSharedAppApplyRejectsUnsupportedDownloadBeforeMutation protects the new shared apply boundary.
func TestSharedAppApplyRejectsUnsupportedDownloadBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "mcp.yaml", "apiVersion: fused/v1\nkind: mcp\nname: tools\nversion: 1.0.0\ndescription: Test tools\nbucket: default\nservices:\n  crm:\n    version: v1\n    operations: [createCustomer]\n")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "must not mutate", 500) }))
	defer server.Close()
	output := runCommandInDirExpectError(t, dir, server.URL, []string{"app", "apply", "-f", path, "--download"})
	if calls != 0 || !strings.Contains(output, "only for generated SDK apps") {
		t.Fatalf("calls=%d error=%s", calls, output)
	} // Reject before plan consumption or apply.
	// Shared flag state is reset explicitly because another command test may follow.
	sdkApplyDownload = false
}

// TestSharedMCPApplyJSONPreservesTokenAndTransport verifies one confirmed structured receipt without package semantics.
func TestSharedMCPApplyJSONPreservesTokenAndTransport(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "mcp.yaml", "apiVersion: fused/v1\nkind: mcp\nname: tools\nversion: 1.0.0\ndescription: Test tools\nbucket: default\nservices:\n  crm:\n    version: v1\n    operations: [createCustomer]\n")
	applies := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path { // Only the selected MCP plan and apply endpoints are authorized.
		case "/health":
			fmt.Fprint(w, `{"environment":"development"}`)
		case "/mcp-config/plan":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"plan_id": "plan-mcp", "config_key": body["config_key"], "source_hash": body["source_hash"], "summary": map[string]any{}})
		case "/mcp-config/apply":
			applies++
			fmt.Fprint(w, `{"status":"applied","plan_id":"plan-mcp","app_family_id":"family","app_id":"version","execution_token":"fixture-only-token","transport_urls":{"streamable_http":"https://engine.example/mcp/family"}}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer server.Close()
	runCommandInDirOutput(t, dir, server.URL, []string{"app", "plan", "-f", path, "--json"})
	output := runCommandInDirOutput(t, dir, server.URL, []string{"app", "apply", "-f", path, "--json"})
	var receipt []map[string]any
	if err := json.Unmarshal([]byte(output), &receipt); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, output)
	} // No prose may corrupt the receipt.
	if applies != 1 || len(receipt) != 1 || receipt[0]["execution_token"] != "fixture-only-token" || receipt[0]["transport_urls"] == nil {
		t.Fatalf("applies=%d output=%s", applies, output)
	} // Do not drop credentials or transport data.
}
