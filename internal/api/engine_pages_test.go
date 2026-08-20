package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

func TestListBucketSummariesPageUsesEngineGraphQL(t *testing.T) {
	var sawPath string
	var sawVariables map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		w.Write([]byte(`{"data":{"bucketSummaryPage":{"total":1,"items":[{"id":"bucket-1","workspace_id":"ws-1","name":"prod","is_default":true,"secret_count":2,"value_count":1,"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"}]}}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "fsk_test")
	page, err := client.ListBucketSummariesPage(api.PageOptions{Limit: 25, Offset: 50})
	if err != nil {
		t.Fatalf("ListBucketSummariesPage: %v", err)
	}
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	if sawVariables["limit"] != float64(25) || sawVariables["offset"] != float64(50) {
		t.Fatalf("unexpected pagination variables: %#v", sawVariables)
	}
	if page.Total != 1 || page.Items[0].Name != "prod" || page.Items[0].SecretCount != 2 {
		t.Fatalf("unexpected page response: %#v", page)
	}
}

func TestSearchEndpointsPageSendsLimitOffset(t *testing.T) {
	var sawVariables map[string]any
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
		if !strings.Contains(body.Query, "searchEndpoints") {
			t.Fatalf("expected searchEndpoints query, got %s", body.Query)
		}
		sawVariables = body.Variables
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"searchEndpoints":[{"id":"ep-1","name":"issuesList","path":"/issues","method":"GET","description":"","service_id":"svc-1"}]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "fsk_test")
	ops, err := client.SearchEndpointsPage("svc-1", "2026-07-01", "issue", api.PageOptions{Limit: 10, Offset: 20})
	if err != nil {
		t.Fatalf("SearchEndpointsPage: %v", err)
	}
	if sawVariables["limit"] != float64(10) || sawVariables["offset"] != float64(20) || sawVariables["q"] != "issue" {
		t.Fatalf("unexpected variables: %#v", sawVariables)
	}
	if len(ops) != 1 || ops[0].Name != "issuesList" {
		t.Fatalf("unexpected operations: %#v", ops)
	}
}

// TestWorkspaceWebhooksAndAppTokensUseEngineGraphQL verifies both reads share the Engine endpoint.
func TestWorkspaceWebhooksAndAppTokensUseEngineGraphQL(t *testing.T) {
	var paths []string
	var queries []string
	srv := httptest.NewServer(workspaceAndTokenHandler(t, &paths, &queries))
	defer srv.Close()

	client := api.NewClient(srv.URL, "fsk_test")
	if webhooks, err := client.ListWorkspaceWebhooks("svc-1"); err != nil || len(webhooks) != 1 {
		t.Fatalf("ListWorkspaceWebhooks = %#v, %v", webhooks, err)
	}
	if tokens, err := client.ListAppTokens("family-1"); err != nil || len(tokens) != 1 || tokens[0].LastUsedAt != nil || tokens[0].ExpiresAt == nil {
		t.Fatalf("ListAppTokens = %#v, %v", tokens, err)
	}
	assertEngineGraphQLPaths(t, paths, queries)
}

// workspaceAndTokenHandler returns exact GraphQL fixtures for two neighboring reads.
func workspaceAndTokenHandler(t *testing.T, paths, queries *[]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		*queries = append(*queries, body.Query)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "workspaceWebhooks"):
			w.Write([]byte(`{"data":{"workspaceWebhooks":[{"label":"repo","slug":"slugaaaaaaaaaaaaaaaaa","created_at":"2026-07-21T00:00:00Z"}]}}`))
		case strings.Contains(body.Query, "appTokens"):
			if !strings.Contains(body.Query, "app_family_id: $appFamilyId") {
				t.Fatalf("app token query is not family scoped: %s", body.Query)
			}
			w.Write([]byte(`{"data":{"appTokens":[{"id":"tok-1","app_family_id":"family-1","name":"agent","allow":["issues.list"],"expires_at":"2026-07-21T01:00:00Z","created_at":"2026-07-21T00:00:00Z","last_used_at":""}]}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}
}

// assertEngineGraphQLPaths rejects a fallback to Registry GraphQL for either read.
func assertEngineGraphQLPaths(t *testing.T, paths, queries []string) {
	t.Helper()
	for _, path := range paths {
		if path != "/engine/graphql" {
			t.Fatalf("expected all reads to use /engine/graphql, paths=%#v queries=%#v", paths, queries)
		}
	}
}

// TestSDKBucketsUseAppFamilyScopeAndCurrentSchema protects the exact Engine GraphQL transport contract.
func TestSDKBucketsUseAppFamilyScopeAndCurrentSchema(t *testing.T) {
	var variables map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode sdk bucket request: %v", err)
		}
		if !strings.Contains(body.Query, "sdkBuckets(app_family_id: $appFamilyId)") || strings.Contains(body.Query, "artifact_id") {
			t.Fatalf("SDK bucket query is not family scoped: %s", body.Query)
		}
		// Bucket intentionally hides workspace ownership, so requesting the retired field rejects the entire query.
		if strings.Contains(body.Query, "workspace_id") {
			t.Fatalf("SDK bucket query requested a field absent from Engine Bucket: %s", body.Query)
		}
		variables = body.Variables
		_, _ = w.Write([]byte(`{"data":{"sdkBuckets":[{"id":"bucket-1","name":"prod","is_default":false,"created_at":"2026-08-05T10:00:00Z","updated_at":"2026-08-05T10:00:00Z"}]}}`))
	}))
	defer server.Close()

	client := api.NewClient(server.URL, "fsk_test")
	buckets, err := client.ListSDKBuckets("family-1")
	if err != nil || len(buckets) != 1 || buckets[0].ID != "bucket-1" {
		t.Fatalf("ListSDKBuckets = %#v, %v", buckets, err)
	}
	if variables["appFamilyId"] != "family-1" {
		t.Fatalf("SDK bucket variables = %#v", variables)
	}
}

// TestAuthConnectionPageSelectsManagedRefreshMetadata protects the CLI's safe
// operator view while rejecting accidental credential or lease projections.
func TestAuthConnectionPageSelectsManagedRefreshMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode auth connection request: %v", err)
		}
		assertManagedRefreshConnectionQuery(t, body.Query)
		_, _ = w.Write([]byte(`{"data":{"authConnectionPage":{"total":1,"items":[{"id":"connection-1","service_version_id":"version-1","auth_name":"OAuth2","last_refresh_attempt_at":"2026-08-20T09:00:00Z","last_refreshed_at":"2026-08-20T09:01:00Z","refresh_retry_not_before":"2026-08-20T10:00:00Z","refresh_state":"ok"}]}}}`))
	}))
	defer server.Close()

	client := api.NewClient(server.URL, "fsk_test")
	page, err := client.ListAuthConnectionPage("bucket-1", "service-1", "user-1", api.PageOptions{Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListAuthConnectionPage = %#v, %v", page, err)
	}
	connection := page.Items[0]
	if connection.ServiceVersionID != "version-1" || connection.AuthName != "OAuth2" || connection.LastRefreshedAt != "2026-08-20T09:01:00Z" || connection.RefreshRetryNotBefore != "2026-08-20T10:00:00Z" {
		t.Fatalf("unexpected managed refresh metadata: %#v", connection)
	}
}

// assertManagedRefreshConnectionQuery checks the complete safe field contract
// and keeps credential/lease storage inaccessible to the CLI response.
func assertManagedRefreshConnectionQuery(t *testing.T, query string) {
	t.Helper()
	for _, field := range []string{"service_version_id", "auth_name", "refresh_token_expires_at", "last_refresh_attempt_at", "last_refreshed_at", "refresh_retry_not_before"} {
		if !strings.Contains(query, field) {
			t.Fatalf("auth connection query omitted %s", field)
		}
	}
	for _, forbidden := range []string{"access_token", "refresh_lease_token", "refresh_lease_expires_at", "encrypted_dek"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("auth connection query selected private field %s", forbidden)
		}
	}
}
