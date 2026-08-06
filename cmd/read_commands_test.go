package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBucketListUsesPagedEngineGraphQL(t *testing.T) {
	var sawPath string
	var sawVariables map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "bucketSummaryPage") {
			t.Fatalf("expected bucketSummaryPage query, got %s", body.Query)
		}
		sawVariables = body.Variables
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"bucketSummaryPage":{"total":2,"items":[{"id":"bucket-1","workspace_id":"ws-1","name":"prod","is_default":true,"secret_count":3,"value_count":1,"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"bucket", "list", "--limit", "1", "--offset", "1"})
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	if sawVariables["limit"] != float64(1) || sawVariables["offset"] != float64(1) {
		t.Fatalf("unexpected variables: %#v", sawVariables)
	}
	if !strings.Contains(out, "prod (default)") || !strings.Contains(out, "bucket-1") || !strings.Contains(out, "NAME") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestServiceOperationsSearchUsesServerPagination(t *testing.T) {
	var sawSearchVars map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "GetServiceInfo"):
			w.Write([]byte(`{"data":{"service":{"id":"svc-1","name":"GitHub","slug":"github","base_url":"https://api.github.test","provider":null,"is_owner":true,"servers":[],"auth_configs":[]}}}`))
		case strings.Contains(body.Query, "searchEndpoints"):
			sawSearchVars = body.Variables
			w.Write([]byte(`{"data":{"searchEndpoints":[{"id":"ep-1","name":"issuesList","path":"/issues","method":"GET","description":"","service_id":"svc-1"}]}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"service", "operations", "github", "--q", "issue", "--limit", "5", "--offset", "10"})
	if sawSearchVars["limit"] != float64(5) || sawSearchVars["offset"] != float64(10) || sawSearchVars["q"] != "issue" {
		t.Fatalf("unexpected search variables: %#v", sawSearchVars)
	}
	if !strings.Contains(out, "issuesList") || !strings.Contains(out, "/issues") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestSecretListUsesBucketScopedPagination(t *testing.T) {
	const bucketID = "11111111-1111-4111-8111-111111111111"
	var sawVariables map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "secretMetaPage") {
			t.Fatalf("expected secretMetaPage query, got %s", body.Query)
		}
		sawVariables = body.Variables
		_, _ = w.Write([]byte(`{"data":{"secretMetaPage":{"total":1,"items":[{"id":"secret-1","bucket_id":"` + bucketID + `","service_id":"svc-1","key_name":"Authorization","credential_type":"bearer","expires_at":"","created_at":"2026-08-01T00:00:00Z","updated_at":"2026-08-01T00:00:00Z"}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"secret", "list", "--bucket", bucketID, "--limit", "5", "--offset", "10"})
	if sawVariables["bucketId"] != bucketID || sawVariables["limit"] != float64(5) || sawVariables["offset"] != float64(10) {
		t.Fatalf("unexpected variables: %#v", sawVariables)
	}
	if !strings.Contains(out, "Authorization") || strings.Contains(out, "value") {
		t.Fatalf("unexpected secret metadata output: %q", out)
	}
}

func TestSDKListUsesEnginePaginationAndFixedKind(t *testing.T) {
	var sawVariables map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "apps(kind: $kind") {
			t.Fatalf("expected apps query, got %s", body.Query)
		}
		sawVariables = body.Variables
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"apps":{"total":1,"items":[{"app_family_id":"family-1","app_id":"sdk-1","name":"security","version":"1.0.0","kind":"sdk","status":"inactive","created_at":"2026-07-21T00:00:00Z","selections":[]}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"sdk", "list", "--limit", "10", "--offset", "20"})
	if sawVariables["limit"] != float64(10) || sawVariables["offset"] != float64(20) || sawVariables["kind"] != "sdk" {
		t.Fatalf("unexpected variables: %#v", sawVariables)
	}
	if !strings.Contains(out, "security") || !strings.Contains(out, "sdk-1") || !strings.Contains(out, "family-1") ||
		!strings.Contains(out, "SDK_ID") || !strings.Contains(out, "VERSION_ID") || !strings.Contains(out, "inactive") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestSDKServicesUsesKindScopedHumanReferenceAtEngineBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if strings.Contains(body.Query, "appReference") {
			if body.Variables["reference"] != "support" || body.Variables["version"] != "2.0.0" || body.Variables["kind"] != "sdk" {
				t.Fatalf("unexpected SDK reference: %#v", body.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"app-1","kind":"app"}}}`))
			return
		}
		if body.Variables["appId"] != "app-1" || !strings.Contains(body.Query, "appServices") {
			t.Fatalf("unexpected app services request: %#v", body)
		}
		_, _ = w.Write([]byte(`{"data":{"appServices":[{"service_id":"service-1","service_slug":"github","service_name":"GitHub","version":"v1","select_all":false,"endpoint_count":2,"webhook_count":1}]}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"sdk", "services", "support@2.0.0"})
	if !strings.Contains(out, "github") || !strings.Contains(out, "service-1") || !strings.Contains(out, "2") {
		t.Fatalf("unexpected SDK services output: %q", out)
	}
}

func TestSDKShowUsesPublicIDLabels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if strings.Contains(body.Query, "appReference") {
			_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"version-1","kind":"app"}}}`))
			return
		}
		if body.Variables["appId"] != "version-1" {
			t.Fatalf("unexpected SDK version lookup: %#v", body.Variables)
		}
		_, _ = w.Write([]byte(`{"data":{"app":{"app_family_id":"sdk-1","app_id":"version-1","name":"security","version":"1.0.0","kind":"sdk","status":"active","created_at":"now","selections":[]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"sdk", "show", "security@1.0.0"})
	for _, expected := range []string{"sdk_id:\tsdk-1", "version_id:\tversion-1"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("SDK show output %q is missing %q", out, expected)
		}
	}
	if strings.Contains(out, "family_id") {
		t.Fatalf("SDK show exposes internal family terminology: %q", out)
	}
}

func TestMCPListUsesEnginePaginationAndFixedKind(t *testing.T) {
	var sawVariables map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "apps(kind: $kind") {
			t.Fatalf("expected MCP apps query, got %s", body.Query)
		}
		sawVariables = body.Variables
		_, _ = w.Write([]byte(`{"data":{"apps":{"total":1,"items":[{"app_family_id":"family-1","app_id":"mcp-1","name":"support","version":"1.0.0","kind":"mcp","status":"active","created_at":"now","selections":[]}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"mcp", "list", "--limit", "5", "--offset", "10"})
	if sawVariables["kind"] != "mcp" || sawVariables["limit"] != float64(5) || sawVariables["offset"] != float64(10) {
		t.Fatalf("unexpected MCP list variables: %#v", sawVariables)
	}
	if !strings.Contains(out, "support") || !strings.Contains(out, "mcp-1") || !strings.Contains(out, "family-1") ||
		!strings.Contains(out, "MCP_ID") || !strings.Contains(out, "VERSION_ID") || !strings.Contains(out, "/mcp/mcp-1/sse") {
		t.Fatalf("unexpected MCP list output: %q", out)
	}
}

func TestMCPDeactivateResolvesOnlyMCPNames(t *testing.T) {
	var sawResolution, sawDeactivate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			var body struct {
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode MCP reference request: %v", err)
			}
			if body.Variables["reference"] != "support" || body.Variables["version"] != "1.0.0" || body.Variables["kind"] != "mcp" {
				t.Fatalf("unexpected MCP reference variables: %#v", body.Variables)
			}
			sawResolution = true
			_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"mcp-1","kind":"app"}}}`))
		case "/apps/mcp-1/":
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected MCP deactivate method: %s", r.Method)
			}
			sawDeactivate = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected MCP remove request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"mcp", "deactivate", "support@1.0.0"})
	if !sawResolution || !sawDeactivate || !strings.Contains(out, "Deactivated MCP server") {
		t.Fatalf("MCP deactivate = resolution:%t deactivate:%t output:%q", sawResolution, sawDeactivate, out)
	}
}
