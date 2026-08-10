package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

func TestListWorkspaceServices_SendsNameFilterAsGraphQLVariable(t *testing.T) {
	var sawPath string
	var sawNames []interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "workspaceServices") {
			t.Fatalf("expected workspaceServices query, got %s", body.Query)
		}
		sawNames, _ = body.Variables["names"].([]interface{})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"workspaceServices":[]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	if _, err := client.ListWorkspaceServices("E2E Service Name"); err != nil {
		t.Fatalf("ListWorkspaceServices: %v", err)
	}
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	if len(sawNames) != 1 || sawNames[0] != "E2E Service Name" {
		t.Fatalf("expected names variable with preserved spaces, got %#v", sawNames)
	}
}

// TestListWorkspaceConnectConfigsUsesEngineGraphQL verifies sync reads bucket
// connect state through the authenticated GraphQL surface rather than REST.
func TestListWorkspaceConnectConfigsUsesEngineGraphQL(t *testing.T) {
	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		assertEngineQueryContains(t, body.Query, "workspaceConnectConfigs", "profiles", "auth_name")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"workspaceConnectConfigs":[{"bucket_id":"bucket-1","bucket_name":"customers","service_id":"svc-1","auth_type":"oauth","auth_name":"primaryOAuth","enabled":true,"redirect_uri":"https://engine.example.com/callback","has_client_id":true,"has_client_secret":true,"profiles":[{"service_version_id":"ver-1","auth_type":"oauth","provenance":"workspace","profile":{"auth_type":"oauth"}}]}]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	configs, err := client.ListWorkspaceConnectConfigs()

	if err != nil {
		t.Fatalf("ListWorkspaceConnectConfigs: %v", err)
	}
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	assertWorkspaceConnectConfig(t, configs)
}

func assertWorkspaceConnectConfig(t *testing.T, configs []api.WorkspaceConnectConfig) {
	t.Helper()
	if len(configs) != 1 {
		t.Fatalf("workspace connect configs = %#v, want one", configs)
	}
	config := configs[0]
	if config.BucketName != "customers" || config.AuthName != "primaryOAuth" || !config.HasClientSecret {
		t.Fatalf("unexpected workspace connect config: %#v", config)
	}
	if len(config.Profiles) != 1 || config.Profiles[0].AuthType != "oauth" {
		t.Fatalf("unexpected workspace profile: %#v", config.Profiles)
	}
}

func assertEngineQueryContains(t *testing.T, query string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(query, value) {
			t.Errorf("query does not contain %q: %s", value, query)
		}
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
		w.Write([]byte(`{"data":{"servicesByIds":[{"id":"svc-1","is_owner":true,"is_public":false,"rate_limit":{"version":2,"policies":[{"name":"minute_quota","unit":"quota_units","scope":"connection","default_cost":1,"operation_costs":{"rest:GET:/drive/v3/files/{}":10},"algorithm":"fixed_window","fixed_window":{"limit":10000,"duration_ms":60000},"response_headers":{"remaining":"X-Quota-Remaining","reset":{"name":"X-Quota-Reset","format":"unix_milliseconds"}}}]},"pagination":{"version":2,"type":"cursor","cursor":{"request":{"location":"query","name":"after"},"next":{"location":"body","path":"page.next","value_type":"string"}},"items_path":"items","limits":{"max_pages":100,"max_items":100000,"max_bytes":104857600,"max_duration_ms":300000}}},{"id":"svc-2","is_owner":false,"is_public":true,"provider":{"handle":"acme"}}]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	visibility, err := client.ServiceVisibilities([]string{"svc-1", "svc-2"})
	if err != nil {
		t.Fatalf("ServiceVisibilities: %v", err)
	}
	if !strings.Contains(sawQuery, "servicesByIds") || !strings.Contains(sawQuery, "provider { handle }") || len(sawIDs) != 2 {
		t.Fatalf("expected one batched servicesByIds query, query=%q ids=%#v", sawQuery, sawIDs)
	}
	for _, field := range []string{"cursor {", "offset {", "page_number {", "next_url {", "items_path", "max_duration_ms", "value_type"} {
		if !strings.Contains(sawQuery, field) {
			t.Fatalf("pagination v2 projection missing %q: %s", field, sawQuery)
		}
	}
	for _, field := range []string{"operation_costs", "fixed_window", "token_bucket", "response_headers", "retry_after"} {
		if !strings.Contains(sawQuery, field) {
			t.Fatalf("rate-limit v2 projection missing %q: %s", field, sawQuery)
		}
	}
	if strings.Contains(sawQuery, "refill_interval_ms burst") {
		t.Fatalf("rate-limit projection requested non-contract token_bucket.burst: %s", sawQuery)
	}
	if !visibility["svc-1"].IsOwner || visibility["svc-1"].IsPublic {
		t.Fatalf("unexpected svc-1 visibility: %#v", visibility["svc-1"])
	}
	if visibility["svc-2"].IsOwner || !visibility["svc-2"].IsPublic {
		t.Fatalf("unexpected svc-2 visibility: %#v", visibility["svc-2"])
	}
	if visibility["svc-2"].Provider == nil || visibility["svc-2"].Provider.Handle != "acme" {
		t.Fatalf("unexpected svc-2 provider: %#v", visibility["svc-2"].Provider)
	}
	if pagination := visibility["svc-1"].Pagination; pagination == nil || pagination.Cursor == nil || pagination.Cursor.Next.Path != "page.next" || pagination.Limits.MaxPages != 100 {
		t.Fatalf("pagination v2 did not decode: %#v", pagination)
	}
	if rateLimit := visibility["svc-1"].RateLimit; rateLimit == nil || rateLimit.Policies[0].OperationCosts["rest:GET:/drive/v3/files/{}"] != 10 {
		t.Fatalf("rate-limit v2 did not decode: %#v", rateLimit)
	}
}

func TestServiceVisibilitiesRejectsLegacyRateLimitResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"servicesByIds":[{"id":"svc-1","rate_limit":{"strategy":"fixed_window","requests_per_second":10}}]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	if _, err := client.ServiceVisibilities([]string{"svc-1"}); err == nil {
		t.Fatal("legacy Registry rate-limit response must not be accepted")
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
		w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-1","service_id":"svc-1","name":"2026-07-01","status":"public","created_at":"2026-07-16T00:00:00Z","pagination":{"version":2,"type":"next_url","next_url":{"next":{"location":"link","relation":"next","value_type":"string"}},"items_path":"values","limits":{"max_pages":25,"max_items":10000,"max_bytes":10485760,"max_duration_ms":60000}}}]}}`))
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
	if pagination := versions[0].Pagination; pagination == nil || pagination.NextURL == nil || pagination.NextURL.Next.Relation != "next" {
		t.Fatalf("version pagination v2 did not decode: %#v", pagination)
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
