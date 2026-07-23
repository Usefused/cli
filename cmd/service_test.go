package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

// TestServiceShow_PrintsBareSlugForOwnedService covers the common case: a
// service the caller owns should never be shown with a provider prefix.
func TestServiceShow_PrintsBareSlugForOwnedService(t *testing.T) {
	dir := t.TempDir()
	server := newServiceInfoTestServer(t, `{"id":"svc-1","name":"Stripe","slug":"stripe","base_url":"https://api.stripe.com","provider":null,"is_owner":true,"servers":[],"auth_configs":[]}`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "stripe", "show"})
	if !strings.Contains(out, "slug:\tstripe\n") {
		t.Fatalf("expected bare slug in output, got %q", out)
	}
}

// TestServiceShow_QualifiesSlugForNonOwnedService is the fix this test file
// exists for: `service @acme/plunk show` echoing back the bare slug (just
// "plunk") would be actively misleading -- slugs are only unique per-account
// (internal/registry/graph/service_lookup.go), so a bare slug silently
// stops being a valid reference to reuse the moment the caller has their own
// same-named service.
func TestServiceShow_QualifiesSlugForNonOwnedService(t *testing.T) {
	dir := t.TempDir()
	server := newServiceInfoTestServer(t, `{"id":"svc-2","name":"Plunk","slug":"plunk","base_url":"https://api.useplunk.com","provider":{"handle":"acme"},"is_owner":false,"servers":[],"auth_configs":[]}`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"service", "@acme/plunk", "show"})
	if !strings.Contains(out, "slug:\t@acme/plunk\n") {
		t.Fatalf("expected provider-qualified slug in output, got %q", out)
	}
}
