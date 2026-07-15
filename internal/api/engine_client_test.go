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

func TestServiceVisibilitiesUsesSingleGraphQLBatch(t *testing.T) {
	var sawIDs []interface{}
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		sawQuery = body.Query
		sawIDs, _ = body.Variables["serviceIds"].([]interface{})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"servicesByIds":[{"id":"svc-1","is_owner":true,"is_public":false},{"id":"svc-2","is_owner":false,"is_public":true}]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	visibility, err := client.ServiceVisibilities([]string{"svc-1", "svc-2"})
	if err != nil {
		t.Fatalf("ServiceVisibilities: %v", err)
	}
	if !strings.Contains(sawQuery, "servicesByIds") || len(sawIDs) != 2 {
		t.Fatalf("expected one batched servicesByIds query, query=%q ids=%#v", sawQuery, sawIDs)
	}
	if !visibility["svc-1"].IsOwner || visibility["svc-1"].IsPublic {
		t.Fatalf("unexpected svc-1 visibility: %#v", visibility["svc-1"])
	}
	if visibility["svc-2"].IsOwner || !visibility["svc-2"].IsPublic {
		t.Fatalf("unexpected svc-2 visibility: %#v", visibility["svc-2"])
	}
}

func TestServiceVersionsReturnsServiceIDForSlug(t *testing.T) {
	var sawSlug string
	var sawProvider string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "service_id") {
			t.Fatalf("expected service_id in query, got %s", body.Query)
		}
		sawSlug, _ = body.Variables["serviceId"].(string)
		sawProvider, _ = body.Variables["provider"].(string)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-1","service_id":"svc-1","name":"2026-07-01","status":"public","created_at":"2026-07-16T00:00:00Z"}]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	versions, err := client.ServiceVersions("github")
	if err != nil {
		t.Fatalf("ServiceVersions: %v", err)
	}
	if sawSlug != "github" {
		t.Fatalf("expected slug variable github, got %q", sawSlug)
	}
	if sawProvider != "" {
		t.Fatalf("expected empty provider for bare slug, got %q", sawProvider)
	}
	if len(versions) != 1 || versions[0].ServiceID != "svc-1" {
		t.Fatalf("expected service_id svc-1, got %#v", versions)
	}
}

func TestServiceVersionsSplitsProviderQualifiedSlug(t *testing.T) {
	var sawSlug string
	var sawProvider string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		sawSlug, _ = body.Variables["serviceId"].(string)
		sawProvider, _ = body.Variables["provider"].(string)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-1","service_id":"svc-1","name":"2026-07-01","status":"public","created_at":"2026-07-16T00:00:00Z"}]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	if _, err := client.ServiceVersions("@acme-inc/custom-crm"); err != nil {
		t.Fatalf("ServiceVersions: %v", err)
	}
	if sawSlug != "custom-crm" || sawProvider != "acme-inc" {
		t.Fatalf("expected split provider-qualified slug, got slug=%q provider=%q", sawSlug, sawProvider)
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
