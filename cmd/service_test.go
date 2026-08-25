package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// newServiceInfoTestServer serves a fixed GetServiceInfo GraphQL response for
// any /graphql request -- runServiceShow is the only command exercised here,
// so there's no need for the substring-responder map create_test.go's
// newGraphQLTestServer uses for multiple distinct queries.
func newServiceInfoTestServer(t *testing.T, serviceJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"service":` + serviceJSON + `}}`))
	}))
}

func newServiceSearchTestServer(t *testing.T, servicesJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode GraphQL body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/graphql":
			if !strings.Contains(body.Query, "searchServices") {
				t.Fatalf("expected searchServices query, got %q", body.Query)
			}
			if body.Variables["q"] != "billing" {
				t.Fatalf("query variable = %#v, want billing", body.Variables["q"])
			}
			_, _ = w.Write([]byte(`{"data":{"searchServices":` + servicesJSON + `}}`))
		case "/engine/graphql":
			if !strings.Contains(body.Query, "workspaceServices") {
				t.Fatalf("expected workspaceServices query, got %q", body.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":[]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

// TestServiceSearchTreatsIncompleteQualifierAsLexicalText protects provider-only
// queries from being misrouted into exact @provider/slug resolution.
func TestServiceSearchTreatsIncompleteQualifierAsLexicalText(t *testing.T) {
	sawLexicalSearch := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		// A malformed fixture request cannot prove which GraphQL field was used.
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		// Only the legacy lexical field accepts a provider-only search term;
		// the set resolver requires a complete stable service identity.
		if strings.Contains(request.Query, "searchServices") {
			sawLexicalSearch = true
		}
		_, _ = w.Write([]byte(`{"data":{"searchServices":[]}}`))
	}))
	defer server.Close()
	// Provider-only text must complete successfully through lexical search.
	if _, err := searchServiceResults(api.NewClient(server.URL, "test-key"), "@acme"); err != nil {
		t.Fatal(err)
	}
	// The response alone is insufficient because both fields could decode empty.
	if !sawLexicalSearch {
		t.Fatal("provider-only query did not retain lexical search semantics")
	}
}

// TestServiceShow_PrintsBareSlugForOwnedService covers the common case: a
// service the caller owns should never be shown with a provider prefix.
func TestServiceShow_PrintsBareSlugForOwnedService(t *testing.T) {
	dir := t.TempDir()
	server := newServiceInfoTestServer(t, `{"id":"svc-1","name":"Stripe","slug":"stripe","base_url":"https://api.stripe.com","provider":null,"is_owner":true,"servers":[],"auth_configs":[]}`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "show", "stripe"})
	if !strings.Contains(out, "slug:\tstripe\n") {
		t.Fatalf("expected bare slug in output, got %q", out)
	}
}

// TestServiceShow_QualifiesSlugForNonOwnedService is the fix this test file
// exists for: `service show @acme/plunk` echoing back the bare slug (just
// "plunk") would be actively misleading -- slugs are only unique per-account
// (internal/registry/graph/service_lookup.go), so a bare slug silently
// stops being a valid reference to reuse the moment the caller has their own
// same-named service.
func TestServiceShow_QualifiesSlugForNonOwnedService(t *testing.T) {
	dir := t.TempDir()
	server := newServiceInfoTestServer(t, `{"id":"svc-2","name":"Plunk","slug":"plunk","base_url":"https://api.useplunk.com","provider":{"handle":"acme"},"is_owner":false,"servers":[],"auth_configs":[]}`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "show", "@acme/plunk"})
	if !strings.Contains(out, "slug:\t@acme/plunk\n") {
		t.Fatalf("expected provider-qualified slug in output, got %q", out)
	}
}

func TestServiceShow_PrintsServerVariablesAndBasicPasswordMode(t *testing.T) {
	server := newServiceInfoTestServer(t, `{
		"id":"svc-1","name":"Confluence","slug":"confluence","base_url":"https://{your-domain}.atlassian.net",
		"provider":null,"is_owner":true,
		"servers":[{"url":"https://{your-domain}.atlassian.net","description":"tenant","variables":[
			{"name":"your-domain","enum":["example","sandbox"],"required":true},
			{"name":"api-version","default":"v2","required":false}
		]}],
		"auth_configs":[{"name":"basicAuth","type":"http","scheme":"basic","basic_password_mode":"empty"}]
	}`)
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"service", "show", "confluence"})
	for _, want := range []string{
		"variable:\tyour-domain\trequired: true\tenum: example,sandbox",
		"variable:\tapi-version\trequired: false\tdefault: v2",
		"basicAuth (type: http, scheme: basic, basic_password_mode: empty)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("service output missing %q: %q", want, out)
		}
	}
}

func TestServiceShowJSONReturnsReusableSlugAndAuthMetadata(t *testing.T) {
	server := newServiceInfoTestServer(t, `{
		"id":"svc-2","name":"Payments","description":"Payment APIs","slug":"payments","base_url":"https://api.example.test",
		"provider":{"handle":"acme"},"is_owner":false,"servers":[],
		"auth_configs":[{"name":"oauth","type":"oauth2","pkce_required":true,"oauth2_flows":{"authorizationCode":{"scopes":{"payments:write":"Create payments"}}}}]
	}`)
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"service", "show", "@acme/payments", "--json"})
	var result serviceShowResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if result.Slug != "@acme/payments" || result.Description != "Payment APIs" || len(result.AuthConfigs) != 1 {
		t.Fatalf("service JSON = %#v", result)
	}
}

func TestServiceOperationsJSONIncludesDescriptionAndSecurity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.Contains(body.Query, "GetServiceInfo"):
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-1","name":"Payments","description":"Payment APIs","slug":"payments","base_url":"https://api.example.test","provider":null,"is_owner":true,"servers":[],"auth_configs":[]}}}`))
		case strings.Contains(body.Query, "searchEndpoints"):
			_, _ = w.Write([]byte(`{"data":{"searchEndpoints":[{"id":"op-1","name":"createPayment","description":"Create a payment","path":"/payments","method":"POST","service_id":"svc-1","security_requirements":[{"schemes":[{"scheme":"oauth","scopes":["payments:write"]}]}]}]}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"service", "operations", "payments", "--version", "v2", "--q", "create", "--json"})
	var operations []api.Integration
	if err := json.Unmarshal([]byte(out), &operations); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if len(operations) != 1 || operations[0].Description != "Create a payment" || operations[0].SecurityRequirements[0].Schemes[0].Scopes[0] != "payments:write" {
		t.Fatalf("operation summaries = %#v", operations)
	}
}

func TestServiceOperationShowJSONIncludesRequestedContracts(t *testing.T) {
	serviceOperationVersion = ""
	serviceOperationIncludeRequest, serviceOperationIncludeResponses = false, false
	t.Cleanup(func() {
		serviceOperationVersion = ""
		serviceOperationIncludeRequest, serviceOperationIncludeResponses = false, false
	})
	var operationQueries int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.Contains(body.Query, "GetServiceInfo"):
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-1","name":"Payments","description":"Payment APIs","slug":"payments","base_url":"https://api.example.test","provider":null,"is_owner":true,"servers":[],"auth_configs":[]}}}`))
		case strings.Contains(body.Query, "endpointByName"):
			operationQueries++
			if body.Variables["version"] != "v2" || body.Variables["name"] != "createPayment" {
				t.Fatalf("operation variables = %#v", body.Variables)
			}
			if !strings.Contains(body.Query, "request_content") || !strings.Contains(body.Query, "responses") {
				t.Fatalf("optional contracts missing from query: %s", body.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"endpointByName":{"id":"op-1","service_id":"svc-1","name":"createPayment","description":"Create a payment","method":"POST","path":"/payments","deprecated":false,"parameters":[],"request_content":{"required":true,"representations":[{"media_type":"application/json","serialization":"json"}]},"responses":{"201":{"description":"created","representations":[]}},"security_requirements":[{"schemes":[{"scheme":"oauth","scopes":["payments:write"]}]}]}}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"service", "operation", "show", "payments", "createPayment", "--version", "v2", "--json", "--include-request", "--include-responses"})
	var detail api.ServiceOperationDetail
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if operationQueries != 1 || detail.RequestContent == nil || detail.Responses["201"].Description != "created" {
		t.Fatalf("operation queries=%d detail=%#v", operationQueries, detail)
	}
}

func TestFormatSecurityRequirementsPreservesAlternativeAndSchemeOrder(t *testing.T) {
	requirements := api.SecurityRequirements{
		{Schemes: []api.SecurityRequirement{
			{Scheme: "wiseOAuth", Scopes: []string{"transfers:read", "transfers:write"}},
			{Scheme: "wiseMTLS", Scopes: []string{}},
		}},
		{Schemes: []api.SecurityRequirement{{Scheme: "chargebeeBasic", Scopes: []string{}}}},
		{Schemes: []api.SecurityRequirement{}},
	}
	if got, want := formatSecurityRequirements(requirements), "wiseOAuth[transfers:read,transfers:write] + wiseMTLS OR chargebeeBasic OR anonymous"; got != want {
		t.Fatalf("security requirements = %q, want %q", got, want)
	}
}

func TestServiceSearch_PrintsReusableAccountScopedSlugs(t *testing.T) {
	dir := t.TempDir()
	server := newServiceSearchTestServer(t, `[
		{"id":"svc-owned","name":"Billing API","slug":"billing","provider":{"handle":"my-team"},"is_owner":true,"is_public":false},
		{"id":"svc-public","name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "search", "--q", "billing"})
	if !strings.Contains(out, "Billing API") || !strings.Contains(out, "billing") {
		t.Fatalf("expected owned service with bare slug, got %q", out)
	}
	if !strings.Contains(out, "Acme Billing") || !strings.Contains(out, "@acme/billing") {
		t.Fatalf("expected public service with provider-qualified slug, got %q", out)
	}
}

func TestServiceSearch_JSONHasStableReusableFields(t *testing.T) {
	dir := t.TempDir()
	server := newServiceSearchTestServer(t, `[
		{"id":"svc-public","name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "search", "--q", "billing", "--json"})
	var results []serviceSearchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, out)
	}
	if len(results) != 1 || results[0].Slug != "@acme/billing" || results[0].ServiceID != "svc-public" || results[0].IsPublic == nil || !*results[0].IsPublic || results[0].WorkspaceStatus != serviceWorkspaceAvailable {
		t.Fatalf("unexpected search output: %#v", results)
	}
}

func TestServiceSearchShowsEnabledAndAvailableRegistryServices(t *testing.T) {
	dir := t.TempDir()
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"svc-owned","service_name":"Billing API","service_slug":"billing"}
	]`, `[
		{"id":"svc-public","name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true},
		{"id":"svc-owned","name":"Billing API","slug":"billing","provider":{"handle":"mine"},"is_owner":true,"is_public":false}
	]`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "search", "--q", "billing"})
	if !strings.Contains(out, "Billing API") || !strings.Contains(out, serviceWorkspaceEnabled) {
		t.Fatalf("expected enabled workspace result, got %q", out)
	}
	if !strings.Contains(out, "@acme/billing") || !strings.Contains(out, serviceWorkspaceAvailable) {
		t.Fatalf("expected Registry result available to add, got %q", out)
	}
	if strings.Index(out, "Billing API") > strings.Index(out, "Acme Billing") {
		t.Fatalf("enabled workspace result should be listed first, got %q", out)
	}
}

func TestServiceSearchIncludesExactWorkspaceOnlyResult(t *testing.T) {
	dir := t.TempDir()
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"svc-workspace","service_name":"Private Billing","service_slug":"private-billing"}
	]`, `[]`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "search", "--q", "private-billing", "--json"})
	var results []serviceSearchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("decode combined search output: %v\n%s", err, out)
	}
	if len(results) != 1 || results[0].ServiceID != "svc-workspace" || results[0].WorkspaceStatus != serviceWorkspaceEnabled {
		t.Fatalf("unexpected workspace-only result: %#v", results)
	}
	if strings.Contains(out, `"is_owner"`) || strings.Contains(out, `"is_public"`) {
		t.Fatalf("workspace-only result must not invent Registry visibility metadata: %s", out)
	}
}

func TestServiceSearchKeepsProviderCollisionsDistinct(t *testing.T) {
	dir := t.TempDir()
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"svc-acme","service_name":"Acme Billing","service_slug":"@acme/billing"}
	]`, `[
		{"id":"svc-other","name":"Other Billing","slug":"billing","provider":{"handle":"other"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "search", "--q", "@other/billing", "--json"})
	var results []serviceSearchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ServiceID != "svc-other" || results[0].WorkspaceStatus != serviceWorkspaceAvailable {
		t.Fatalf("provider collision contaminated search results: %#v", results)
	}
}

func TestServiceSearchStopsWhenWorkspaceStatusIsUnauthorized(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/graphql" {
			_, _ = w.Write([]byte(`{"data":{"searchServices":[{"id":"svc-public","name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true}]}}`))
			return
		}
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"service", "search", "--q", "billing"})
	if !strings.Contains(strings.ToLower(errText), "forbidden") && !strings.Contains(errText, "403") {
		t.Fatalf("expected workspace permission failure, got %q", errText)
	}
}

func TestServiceSearchReportsEmptyCombinedResult(t *testing.T) {
	server := newServiceSearchTestServer(t, `[]`)
	defer server.Close()
	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"service", "search", "--q", "billing"})
	if !strings.Contains(out, `No workspace or Registry services found for query "billing".`) {
		t.Fatalf("unexpected empty result message %q", out)
	}
}

func TestServiceSearch_RejectsEmptyQuery(t *testing.T) {
	dir := t.TempDir()
	serviceSearchQuery = ""
	errText := runCommandInDirExpectError(t, dir, "http://127.0.0.1:1", []string{"service", "search", "--q", "   "})
	if !strings.Contains(errText, "--q must not be empty") {
		t.Fatalf("expected empty-query error, got %q", errText)
	}
}
