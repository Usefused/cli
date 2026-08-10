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
		if r.URL.Path != "/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode GraphQL body: %v", err)
		}
		if !strings.Contains(body.Query, "searchServices") {
			t.Fatalf("expected searchServices query, got %q", body.Query)
		}
		if body.Variables["q"] != "billing" {
			t.Fatalf("query variable = %#v, want billing", body.Variables["q"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"searchServices":` + servicesJSON + `}}`))
	}))
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
	serviceSearchJSON = false

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
	serviceSearchJSON = false

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "search", "--q", "billing", "--json"})
	var results []serviceSearchResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, out)
	}
	if len(results) != 1 || results[0].Slug != "@acme/billing" || results[0].ServiceID != "svc-public" || !results[0].IsPublic {
		t.Fatalf("unexpected search output: %#v", results)
	}
}

func TestServiceSearch_RejectsEmptyQuery(t *testing.T) {
	dir := t.TempDir()
	serviceSearchQuery = ""
	serviceSearchJSON = false
	errText := runCommandInDirExpectError(t, dir, "http://127.0.0.1:1", []string{"service", "search", "--q", "   "})
	if !strings.Contains(errText, "--q must not be empty") {
		t.Fatalf("expected empty-query error, got %q", errText)
	}
}
