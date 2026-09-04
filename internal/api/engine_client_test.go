package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestListWorkspaceServices_SendsNameFilterAsGraphQLVariable verifies workspace behavior using the bounded Engine service page contract.
func TestListWorkspaceServices_SendsNameFilterAsGraphQLVariable(t *testing.T) {
	var sawPath string
	var sawNames []interface{}
	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		// Match the bounded membership read used by catalogue and sync commands.
		if !strings.Contains(body.Query, "workspaceServicePage") {
			t.Fatalf("expected workspaceServicePage query, got %s", body.Query)
		}
		sawNames, _ = body.Variables["names"].([]interface{})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"workspaceServicePage":{"data":[],"total":0}}}`))
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

// TestListWorkspaceConnectionProfilesUsesEngineGraphQL verifies sync reads routing policy through GraphQL.
func TestListWorkspaceConnectionProfilesUsesEngineGraphQL(t *testing.T) {
	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		assertEngineQueryContains(t, body.Query, "workspaceConnectionProfiles", "service_version_id", "profile")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"workspaceConnectionProfiles":[{"service_id":"svc-1","service_version_id":"ver-1","auth_type":"oauth","provenance":"workspace","profile":{"auth_type":"oauth"}}]}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	profiles, err := client.ListWorkspaceConnectionProfiles()

	if err != nil {
		t.Fatalf("ListWorkspaceConnectionProfiles: %v", err)
	}
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	assertWorkspaceConnectionProfile(t, profiles)
}

// assertWorkspaceConnectionProfile validates that the flat credential-free projection decodes intact.
func assertWorkspaceConnectionProfile(t *testing.T, profiles []api.WorkspaceConnectionProfile) {
	t.Helper()
	// One fixture row keeps failures attributable to projection rather than ordering.
	if len(profiles) != 1 {
		t.Fatalf("workspace connection profiles = %#v, want one", profiles)
	}
	profile := profiles[0]
	// Service and version identity are required for deterministic YAML placement.
	if profile.ServiceID != "svc-1" || profile.ServiceVersionID != "ver-1" || profile.AuthType != "oauth" {
		t.Fatalf("unexpected workspace connection profile: %#v", profile)
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
	srv := newServiceVisibilitiesServer(t, &sawIDs, &sawQuery)
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	visibility, err := client.ServiceVisibilities([]string{"svc-1", "svc-2"})
	if err != nil {
		t.Fatalf("ServiceVisibilities: %v", err)
	}
	assertServiceVisibilityQuery(t, sawQuery, sawIDs)
	assertServiceVisibilityValues(t, visibility)
}

// newServiceVisibilitiesServer uses the neutral form-signature fixture to
// verify Engine projection without encoding a provider-specific recipe.
func newServiceVisibilitiesServer(t *testing.T, sawIDs *[]interface{}, sawQuery *string) *httptest.Server {
	t.Helper()
	rateLimit := readEngineClientFixture(t, "rate-limit", "v3_dynamic_headers.json")
	retry := readEngineClientFixture(t, "retry", "v3_idempotency_predicates.json")
	signature := readEngineClientFixture(t, "signature", "v1_url_form_signature.json")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		*sawQuery = body.Query
		*sawIDs, _ = body.Variables["serviceIds"].([]interface{})
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"data": map[string]any{"servicesByIds": []any{
			map[string]any{"id": "svc-1", "is_owner": true, "is_public": false, "rate_limit": rateLimit, "retry_config": retry, "pagination": paginationVisibilityFixture(), "incoming_webhook_config": map[string]any{"auth_type": "hmac_signature", "signature_policy": signature}},
			map[string]any{"id": "svc-2", "is_owner": false, "is_public": true, "provider": map[string]any{"handle": "acme"}},
		}}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatal(err)
		}
	}))
}

func readEngineClientFixture(t *testing.T, directory, name string) json.RawMessage {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", directory, name))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func paginationVisibilityFixture() map[string]any {
	return map[string]any{
		"version": 3, "request": []any{},
		"response": map[string]any{
			"items": map[string]any{"path": "$.items"},
			"values": []any{map[string]any{
				"name": "next_cursor", "source": map[string]any{"location": "body", "path": "$.page.next", "value_type": "string"},
			}},
		},
		"continuation": []any{map[string]any{"kind": "token", "state": "cursor", "response_value": "next_cursor"}},
		"termination":  map[string]any{"stop_on_missing_values": []string{"next_cursor"}, "repeated_value": "error"},
		"limits":       map[string]any{"max_pages": 100, "max_items": 100000, "max_bytes": 104857600, "max_duration_ms": 300000},
	}
}

func assertServiceVisibilityQuery(t *testing.T, sawQuery string, sawIDs []interface{}) {
	t.Helper()
	if !strings.Contains(sawQuery, "servicesByIds") || !strings.Contains(sawQuery, "provider { handle }") || len(sawIDs) != 2 {
		t.Fatalf("expected one batched servicesByIds query, query=%q ids=%#v", sawQuery, sawIDs)
	}
	assertQueryContainsFields(t, sawQuery, "pagination v3", []string{"request {", "response {", "continuation {", "termination {", "graphql {", "allowed_origins", "result_aliases", "max_duration_ms", "value_type"})
	assertQueryContainsFields(t, sawQuery, "quota v3", []string{"mode", "identity", "cost {", "fixed_window", "rolling_window", "token_bucket", "concurrency", "response_signals", "cooldown"})
	assertQueryContainsFields(t, sawQuery, "retry v3", []string{"rules {", "operation_kinds", "body_replayability", "idempotency_key", "max_attempts", "retry_after_headers"})
	assertQueryContainsFields(t, sawQuery, "signature policy v1", []string{"signature_policy {", "predicates {", "components {", "secret_ref", "component_separator", "clock_skew_ms", "challenge {"})
	if strings.Contains(sawQuery, "refill_interval_ms burst") {
		t.Fatalf("rate-limit projection requested non-contract token_bucket.burst: %s", sawQuery)
	}
}

func assertQueryContainsFields(t *testing.T, query, label string, fields []string) {
	t.Helper()
	for _, field := range fields {
		if !strings.Contains(query, field) {
			t.Fatalf("%s projection missing %q: %s", label, field, query)
		}
	}
}

func assertServiceVisibilityValues(t *testing.T, visibility map[string]api.ServiceVisibility) {
	t.Helper()
	assertServiceVisibilityFlags(t, visibility)
	assertServiceVisibilityPagination(t, visibility["svc-1"].Pagination)
	assertServiceVisibilityRateLimit(t, visibility["svc-1"].RateLimit)
	assertServiceVisibilityRetry(t, visibility["svc-1"].RetryConfig)
	assertServiceVisibilitySignature(t, visibility["svc-1"].IncomingWebhookConfig)
}

func assertServiceVisibilityFlags(t *testing.T, visibility map[string]api.ServiceVisibility) {
	t.Helper()
	if !visibility["svc-1"].IsOwner || visibility["svc-1"].IsPublic {
		t.Fatalf("unexpected svc-1 visibility: %#v", visibility["svc-1"])
	}
	if visibility["svc-2"].IsOwner || !visibility["svc-2"].IsPublic {
		t.Fatalf("unexpected svc-2 visibility: %#v", visibility["svc-2"])
	}
	if visibility["svc-2"].Provider == nil || visibility["svc-2"].Provider.Handle != "acme" {
		t.Fatalf("unexpected svc-2 provider: %#v", visibility["svc-2"].Provider)
	}
}

func assertServiceVisibilityPagination(t *testing.T, pagination *api.ServicePagination) {
	t.Helper()
	if pagination == nil || len(pagination.Continuation) != 1 || pagination.Response.Values[0].Source.Path != "$.page.next" || pagination.Limits.MaxPages != 100 {
		t.Fatalf("pagination v3 did not decode: %#v", pagination)
	}
}

func assertServiceVisibilityRateLimit(t *testing.T, rateLimit *api.ServiceRateLimit) {
	t.Helper()
	if rateLimit == nil || rateLimit.Policies[0].ResponseSignals.Reset.Format != "unix_seconds" || rateLimit.Cooldown.Headers[0].Name != "Retry-After" {
		t.Fatalf("quota v3 did not decode: %#v", rateLimit)
	}
}

func assertServiceVisibilityRetry(t *testing.T, retry *api.ServiceRetryConfig) {
	t.Helper()
	if retry == nil || retry.Rules[2].Predicates.IdempotencyKey.Header != "Idempotency-Key" || retry.Rules[0].Action.MaxAttempts != 3 {
		t.Fatalf("retry v3 did not decode: %#v", retry)
	}
}

func assertServiceVisibilitySignature(t *testing.T, incoming *api.ServiceIncomingWebhookConfig) {
	t.Helper()
	if incoming == nil || incoming.SignaturePolicy == nil || len(incoming.SignaturePolicy.Rules) != 1 {
		t.Fatalf("signature policy v1 did not decode: %#v", incoming)
	}
	signature := incoming.SignaturePolicy.Rules[0].Verification.Signature
	if signature == nil || len(signature.Components) != 2 || signature.Components[1].Join != "concat_name_value" {
		t.Fatalf("signature recipe changed: %#v", incoming.SignaturePolicy)
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

// TestServiceVersionsReturnsServiceIDForSlug verifies bare references omit provider while retaining the stable service identity.
func TestServiceVersionsReturnsServiceIDForSlug(t *testing.T) {
	var sawSlug string
	var sawProvider string
	srv := newPaginationServiceVersionsServer(t, &sawSlug, &sawProvider)
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	versions, err := client.ServiceVersions("github")
	if err != nil {
		t.Fatalf("ServiceVersions: %v", err)
	}
	assertPaginationServiceVersion(t, sawSlug, sawProvider, versions)
}

// newPaginationServiceVersionsServer captures reference variables so tests enforce Registry's omitted-versus-qualified provider contract.
func newPaginationServiceVersionsServer(t *testing.T, sawSlug, sawProvider *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		*sawSlug, _ = body.Variables["serviceId"].(string)
		provider, supplied := body.Variables["provider"].(string)
		// Bare references must omit provider entirely because Registry rejects an explicit empty namespace.
		if supplied {
			*sawProvider = provider
		} else {
			*sawProvider = "<omitted>"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-1","service_id":"svc-1","name":"2026-07-01","status":"public","created_at":"2026-07-16T00:00:00Z","pagination":{"version":3,"request":[],"response":{"items":{"path":"$.values"},"values":[{"name":"next_link","source":{"location":"link","name":"Link","relation":"next","value_type":"url"}}]},"continuation":[{"kind":"rfc_link","state":"next_url","response_value":"next_link","origin":{"mode":"same_origin"}}],"termination":{"stop_on_missing_values":["next_link"],"repeated_value":"stop"},"limits":{"max_pages":25,"max_items":10000,"max_bytes":10485760,"max_duration_ms":60000}}}]}}`))
	}))
}

// assertPaginationServiceVersion checks identity variables and decoded version metadata together.
func assertPaginationServiceVersion(t *testing.T, sawSlug, sawProvider string, versions []api.ServiceVersion) {
	t.Helper()
	if sawSlug != "github" {
		t.Fatalf("expected slug variable github, got %q", sawSlug)
	}
	// The sentinel proves the provider key was absent rather than present with an empty value.
	if sawProvider != "<omitted>" {
		t.Fatalf("expected omitted provider for bare slug, got %q", sawProvider)
	}
	if len(versions) != 1 || versions[0].ServiceID != "svc-1" {
		t.Fatalf("expected service_id svc-1, got %#v", versions)
	}
	if pagination := versions[0].Pagination; pagination == nil || pagination.Response.Values[0].Source.Relation != "next" || pagination.Continuation[0].Origin.Mode != "same_origin" {
		t.Fatalf("version pagination v3 did not decode: %#v", pagination)
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

// TestServiceVersionSummariesAvoidHeavyContractProjection guards the latency
// boundary: list calls must not request documentation or full policy objects.
func TestServiceVersionSummariesAvoidHeavyContractProjection(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		query = body.Query
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"contract_version":2,"required_capabilities":[],"id":"ver-1","service_id":"svc-1","name":"2026-08-14","status":"public","created_at":"2026-08-14T00:00:00Z","is_public":true}]}}`))
	}))
	defer srv.Close()

	versions, err := api.NewClient(srv.URL, "test-key").ServiceVersionSummaries("billing")
	if err != nil {
		t.Fatal(err)
	}
	assertLeanServiceVersionSummary(t, query, versions)
}

// assertLeanServiceVersionSummary checks both the required compatibility
// fields and the absence of expensive unrelated selections.
func assertLeanServiceVersionSummary(t *testing.T, query string, versions []api.ServiceVersionSummary) {
	t.Helper()
	for _, forbidden := range []string{"documentation", "rate_limit", "retry_config", "pagination", "incoming_webhook_config"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("summary query requested %s: %s", forbidden, query)
		}
	}
	if len(versions) != 1 || versions[0].ID != "ver-1" || versions[0].ContractVersion != 2 || versions[0].RequiredCapabilities == nil {
		t.Fatalf("summary response = %#v", versions)
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
