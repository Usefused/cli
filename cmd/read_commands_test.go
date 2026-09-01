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

func TestWorkspaceServiceOperationsUsesServerPaginationWithoutQuery(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-1","name":"GitHub","slug":"github","base_url":"https://api.github.test","provider":null,"is_owner":true,"servers":[],"auth_configs":[]}}}`))
		case strings.Contains(body.Query, "WorkspaceServices"):
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_id":"svc-1","service_name":"GitHub","service_slug":"github","version":"v1","enabled_versions":[{"service_version_id":"version-1","version":"v1","status":"active"}]}]}}`))
		case strings.Contains(body.Query, "searchEndpoints"):
			sawSearchVars = body.Variables
			_, _ = w.Write([]byte(`{"data":{"searchEndpoints":[{"id":"ep-1","name":"issuesList","path":"/issues","method":"GET","description":"","service_id":"svc-1"}]}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"workspace", "service", "operations", "github", "--version", "v1", "--limit", "5", "--offset", "10"})
	if sawSearchVars["limit"] != float64(5) || sawSearchVars["offset"] != float64(10) || sawSearchVars["q"] != "" {
		t.Fatalf("unexpected unfiltered pagination variables: %#v", sawSearchVars)
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
	server := httptest.NewServer(sdkListTestHandler(t, &sawVariables))
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

func sdkListTestHandler(t *testing.T, sawVariables *map[string]any) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLTestRequest(t, r)
		if !strings.Contains(body.Query, "apps(kind: $kind") {
			t.Fatalf("expected apps query, got %s", body.Query)
		}
		*sawVariables = body.Variables
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"apps":{"total":1,"items":[{"app_family_id":"family-1","app_id":"sdk-1","name":"security","version":"1.0.0","kind":"sdk","status":"inactive","created_at":"2026-07-21T00:00:00Z","selections":[]}]}}}`))
	})
}

func TestSDKServicesUsesKindScopedHumanReferenceAtEngineBoundary(t *testing.T) {
	server := httptest.NewServer(sdkServicesTestHandler(t))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"sdk", "services", "support@2.0.0"})
	if !strings.Contains(out, "github") || !strings.Contains(out, "service-1") || !strings.Contains(out, "2") {
		t.Fatalf("unexpected SDK services output: %q", out)
	}
}

func sdkServicesTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLTestRequest(t, r)
		if strings.Contains(body.Query, "appReference") {
			assertSDKReferenceVariables(t, body.Variables)
			_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"app-1","kind":"app"}}}`))
			return
		}
		if body.Variables["appId"] != "app-1" || !strings.Contains(body.Query, "appServices") {
			t.Fatalf("unexpected app services request: %#v", body)
		}
		_, _ = w.Write([]byte(`{"data":{"appServices":[{"service_id":"service-1","service_slug":"github","service_name":"GitHub","version":"v1","select_all":false,"endpoint_count":2,"webhook_count":1}]}}`))
	})
}

func assertSDKReferenceVariables(t *testing.T, variables map[string]any) {
	t.Helper()
	if variables["reference"] != "support" || variables["version"] != "2.0.0" || variables["kind"] != "sdk" {
		t.Fatalf("unexpected SDK reference: %#v", variables)
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

// TestMCPListUsesEnginePaginationAndFixedKind verifies human output labels stable and pinned URLs.
func TestMCPListUsesEnginePaginationAndFixedKind(t *testing.T) {
	var sawVariables map[string]any
	server := httptest.NewServer(mcpListTestHandler(t, &sawVariables))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"mcp", "list", "--limit", "5", "--offset", "10"})
	assertMCPListVariables(t, sawVariables)
	assertMCPListOutput(t, out)
}

// assertMCPListVariables keeps MCP catalogue filtering scoped and bounded.
func assertMCPListVariables(t *testing.T, variables map[string]any) {
	t.Helper()
	if variables["kind"] != "mcp" || variables["limit"] != float64(5) || variables["offset"] != float64(10) {
		t.Fatalf("unexpected MCP list variables: %#v", variables)
	}
}

// assertMCPListOutput verifies upgrade-safe discovery remains the recommendation.
func assertMCPListOutput(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "support") || !strings.Contains(out, "mcp-1") || !strings.Contains(out, "family-1") ||
		!strings.Contains(out, "MCP_ID") || !strings.Contains(out, "VERSION_ID") ||
		!strings.Contains(out, "STABLE") || !strings.Contains(out, "STABLE_VERSION_ID") ||
		!strings.Contains(out, "DEFAULT_TRANSPORT") || !strings.Contains(out, "streamable_http") ||
		!strings.Contains(out, "STREAMABLE HTTP (STABLE, RECOMMENDED)") || !strings.Contains(out, "https://public.engine.test/mcp/family-1") ||
		!strings.Contains(out, "STREAMABLE HTTP (VERSION-PINNED)") || !strings.Contains(out, "https://public.engine.test/mcp/mcp-1") ||
		!strings.Contains(out, "SSE (STABLE, LEGACY)") || !strings.Contains(out, "https://public.engine.test/mcp/family-1/sse") ||
		!strings.Contains(out, "SSE (VERSION-PINNED, LEGACY)") || !strings.Contains(out, "https://public.engine.test/mcp/mcp-1/sse") {
		t.Fatalf("unexpected MCP list output: %q", out)
	}
}

// TestMCPListJSONRetainsTypedTransportEndpoints verifies automation receives all route choices without prose parsing.
func TestMCPListJSONRetainsTypedTransportEndpoints(t *testing.T) {
	server := httptest.NewServer(mcpListTestHandler(t, new(map[string]any)))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"mcp", "list", "--json"})
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("decode MCP list JSON %q: %v", out, err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("MCP list items = %#v", page.Items)
	}
	item := page.Items[0]
	transportURLs, ok := item["transport_urls"].(map[string]any)
	if item["default_transport"] != "streamable_http" || !ok ||
		item["stable"] != true || item["stable_version_id"] != "mcp-1" ||
		transportURLs["streamable_http"] != "https://public.engine.test/mcp/family-1" ||
		transportURLs["sse"] != "https://public.engine.test/mcp/family-1/sse" ||
		transportURLs["versioned_streamable_http"] != "https://public.engine.test/mcp/mcp-1" ||
		transportURLs["versioned_sse"] != "https://public.engine.test/mcp/mcp-1/sse" {
		t.Fatalf("typed MCP transport JSON = %#v", item)
	}
	if _, exists := item["url"]; exists {
		t.Fatalf("MCP list JSON retained ambiguous url: %#v", item)
	}
	if _, exists := item["mcp_url"]; exists {
		t.Fatalf("MCP list JSON retained ambiguous mcp_url: %#v", item)
	}
}

// mcpListTestHandler returns one MCP version with distinct stable and pinned transport identities.
func mcpListTestHandler(t *testing.T, sawVariables *map[string]any) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeGraphQLTestRequest(t, r)
		if !strings.Contains(body.Query, "apps(kind: $kind") {
			t.Fatalf("expected MCP apps query, got %s", body.Query)
		}
		*sawVariables = body.Variables
		_, _ = w.Write([]byte(`{"data":{"apps":{"total":1,"items":[{"app_family_id":"family-1","app_id":"mcp-1","name":"support","version":"1.0.0","kind":"mcp","status":"active","created_at":"now","default_transport":"streamable_http","stable":true,"stable_version_id":"mcp-1","transport_urls":{"streamable_http":"https://public.engine.test/mcp/family-1","sse":"https://public.engine.test/mcp/family-1/sse","versioned_streamable_http":"https://public.engine.test/mcp/mcp-1","versioned_sse":"https://public.engine.test/mcp/mcp-1/sse"},"selections":[]}]}}}`))
	})
}

func TestMCPDeactivateResolvesOnlyMCPNames(t *testing.T) {
	var sawResolution, sawDeactivate bool
	server := httptest.NewServer(mcpDeactivateTestHandler(t, &sawResolution, &sawDeactivate))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"mcp", "deactivate", "support@1.0.0"})
	if !sawResolution || !sawDeactivate || !strings.Contains(out, "Deactivated MCP server") {
		t.Fatalf("MCP deactivate = resolution:%t deactivate:%t output:%q", sawResolution, sawDeactivate, out)
	}
}

// TestSDKDeactivateResolvesOnlySDKNames proves CLI hard deletion resolves one
// immutable SDK version before invoking the shared Engine lifecycle route.
func TestSDKDeactivateResolvesOnlySDKNames(t *testing.T) {
	var sawResolution, sawDeactivate bool
	server := httptest.NewServer(sdkDeactivateTestHandler(t, &sawResolution, &sawDeactivate))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"sdk", "deactivate", "support@1.0.0"})
	if !sawResolution || !sawDeactivate || !strings.Contains(out, "Deactivated SDK version") {
		t.Fatalf("SDK deactivate = resolution:%t deactivate:%t output:%q", sawResolution, sawDeactivate, out)
	}
}

// sdkDeactivateTestHandler verifies the SDK kind fence and exact DELETE route.
func sdkDeactivateTestHandler(t *testing.T, sawResolution, sawDeactivate *bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Resolution and deletion are the only calls admitted by this lifecycle command.
		switch r.URL.Path {
		case "/engine/graphql":
			body := decodeGraphQLTestRequest(t, r)
			if body.Variables["reference"] != "support" || body.Variables["version"] != "1.0.0" || body.Variables["kind"] != "sdk" {
				t.Fatalf("unexpected SDK reference variables: %#v", body.Variables)
			}
			*sawResolution = true
			_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"sdk-1","kind":"app"}}}`))
		case "/apps/sdk-1/":
			// The lifecycle route is destructive only through its exact DELETE method.
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected SDK deactivate method: %s", r.Method)
			}
			*sawDeactivate = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected SDK deactivate request: %s %s", r.Method, r.URL.Path)
		}
	})
}

func mcpDeactivateTestHandler(t *testing.T, sawResolution, sawDeactivate *bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			body := decodeGraphQLTestRequest(t, r)
			assertMCPReferenceVariables(t, body.Variables)
			*sawResolution = true
			_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"mcp-1","kind":"app"}}}`))
		case "/apps/mcp-1/":
			if r.Method != http.MethodDelete {
				t.Fatalf("unexpected MCP deactivate method: %s", r.Method)
			}
			*sawDeactivate = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected MCP remove request: %s %s", r.Method, r.URL.Path)
		}
	})
}

func assertMCPReferenceVariables(t *testing.T, variables map[string]any) {
	t.Helper()
	if variables["reference"] != "support" || variables["version"] != "1.0.0" || variables["kind"] != "mcp" {
		t.Fatalf("unexpected MCP reference variables: %#v", variables)
	}
}

type graphQLTestRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func decodeGraphQLTestRequest(t *testing.T, request *http.Request) graphQLTestRequest {
	t.Helper()
	var body graphQLTestRequest
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatalf("decode graphql body: %v", err)
	}
	return body
}
