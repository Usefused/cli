package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestActivateMCPServer_PostsToEngine verifies that calling ActivateMCPServer
// sends a POST to the Engine's activate endpoint and sets the correct headers.
func TestActivateMCPServer_PostsToEngine(t *testing.T) {
	const testSDKID = "sdk-123"
	const expectedMCPURL = "http://engine/mcp/stream"

	var reqMethod, reqPath, authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMethod = r.Method
		reqPath = r.URL.Path
		authHeader = r.Header.Get("x-api-key")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(api.MCPActivateResult{
			MCPURL: expectedMCPURL,
		})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	res, err := client.ActivateMCPServer(testSDKID)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqMethod != "POST" {
		t.Errorf("expected POST, got %s", reqMethod)
	}
	if reqPath != "/engine/sdks/"+testSDKID+"/activate" {
		t.Errorf("expected path /engine/sdks/%s/activate, got %s", testSDKID, reqPath)
	}
	if authHeader != "test-key" {
		t.Errorf("expected auth header test-key, got %s", authHeader)
	}
	if res.MCPURL != expectedMCPURL {
		t.Errorf("expected MCP URL %q, got %q", expectedMCPURL, res.MCPURL)
	}
}

// TestActivateMCPServer_HandlesError verifies that non-200 responses return an error.
func TestActivateMCPServer_HandlesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "engine failed"}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	_, err := client.ActivateMCPServer("sdk-123")

	if err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "engine failed") && !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain status or body, got: %v", err)
	}
}

func TestListWorkspaceServices_EscapesNameFilter(t *testing.T) {
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	if _, err := client.ListWorkspaceServices("E2E Service Name"); err != nil {
		t.Fatalf("ListWorkspaceServices: %v", err)
	}
	if rawQuery != "names=E2E+Service+Name" {
		t.Fatalf("expected escaped names query, got %q", rawQuery)
	}
}

func TestGenerateSDK_UnwrapsJSONErrorBody(t *testing.T) {
	const serverMessage = "service Stripe Billing is not activated in this workspace. Run 'fused-cli workspace service add stripe-billing' to activate it."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":` + strconv.Quote(serverMessage) + `}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	_, err := client.GenerateSDK(api.GenerateSDKRequest{Name: "security-sdk"})

	if err == nil {
		t.Fatal("expected error on 403 response, got nil")
	}
	got := err.Error()
	if !strings.Contains(got, serverMessage) {
		t.Fatalf("expected unwrapped server message, got %q", got)
	}
	if strings.Contains(got, `{"error"`) {
		t.Fatalf("expected raw JSON wrapper to be removed, got %q", got)
	}
}

func TestUpdateWorkspacePlanActionPatchesConfigPlanActions(t *testing.T) {
	var reqMethod, reqPath string
	var reqBody struct {
		Actions []map[string]any `json:"actions"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMethod = r.Method
		reqPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"updated","plan_id":"plan-123","revision":2}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	actions := []map[string]any{
		{"id": "keep", "decision": ""},
		{"id": "remove", "requires_decision": true},
	}
	if err := client.UpdateWorkspacePlanAction("plan-123", actions, "remove", "force_remove"); err != nil {
		t.Fatalf("UpdateWorkspacePlanAction: %v", err)
	}

	if reqMethod != http.MethodPatch {
		t.Fatalf("expected PATCH, got %s", reqMethod)
	}
	if reqPath != "/config/plans/plan-123/actions" {
		t.Fatalf("expected /config/plans/plan-123/actions, got %s", reqPath)
	}
	if got := reqBody.Actions[1]["decision"]; got != "force_remove" {
		t.Fatalf("expected updated decision, got %#v in %#v", got, reqBody.Actions)
	}
}
