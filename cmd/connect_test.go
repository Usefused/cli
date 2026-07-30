package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestSelectConnectAuthType_AutoSelectsSoleCandidate covers the common case:
// a service declaring exactly one oauth/oidc scheme needs no --type at all.
func TestSelectConnectAuthType_AutoSelectsSoleCandidate(t *testing.T) {
	connectSetType = ""
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{{Name: "oauth", Type: "oauth2"}}}
	authType, err := selectConnectAuthType(info, "jira")
	if err != nil || authType != "oauth" {
		t.Fatalf("expected auto-selected oauth, got %q err=%v", authType, err)
	}
}

// TestSelectConnectAuthType_RejectsServiceWithNoInteractiveScheme guards the
// documented rule that connect only supports oauth/oidc -- basic/api_key/
// bearer/mtls have no browser consent step (see fused-config).
func TestSelectConnectAuthType_RejectsServiceWithNoInteractiveScheme(t *testing.T) {
	connectSetType = ""
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{{Name: "apiKey", Type: "apiKey"}}}
	if _, err := selectConnectAuthType(info, "stripe"); err == nil {
		t.Fatal("expected error for a service with no oauth/oidc scheme")
	}
}

// TestSelectConnectAuthType_RequiresTypeFlagWhenAmbiguous mirrors secret.go's
// existing --type disambiguation for a service offering both oauth and oidc.
func TestSelectConnectAuthType_RequiresTypeFlagWhenAmbiguous(t *testing.T) {
	connectSetType = ""
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{
		{Name: "oauth", Type: "oauth2"},
		{Name: "oidc", Type: "openidconnect"},
	}}
	if _, err := selectConnectAuthType(info, "acme"); err == nil {
		t.Fatal("expected ambiguity error without --type")
	}

	connectSetType = "oidc"
	t.Cleanup(func() { connectSetType = "" })
	authType, err := selectConnectAuthType(info, "acme")
	if err != nil || authType != "oidc" {
		t.Fatalf("expected --type to select oidc, got %q err=%v", authType, err)
	}
}

// TestConnectFieldsFromInline_OnlySendsProvidedKeys is the crux of partial
// update: a key that never appears in the inline value must produce a nil
// pointer (omitted from the JSON request, meaning "leave unchanged" to
// Engine), not a zero-value empty string (which Engine rejects as "blanked
// out").
func TestConnectFieldsFromInline_OnlySendsProvidedKeys(t *testing.T) {
	req := connectFieldsFromInline("oauth", "redirect_uri=https://engine.example.com/connect/callback")
	if req.AuthType == nil || *req.AuthType != "oauth" {
		t.Fatalf("expected auth_type to always be set, got %#v", req.AuthType)
	}
	if req.ClientID != nil || req.ClientSecret != nil {
		t.Fatalf("expected client_id/client_secret to stay nil when not provided, got id=%v secret=%v", req.ClientID, req.ClientSecret)
	}
	if req.RedirectURI == nil || *req.RedirectURI != "https://engine.example.com/connect/callback" {
		t.Fatalf("expected redirect_uri to be set, got %#v", req.RedirectURI)
	}
}

// TestConnectFieldsFromInline_BlankValueIsExplicit proves a key present but
// blank ("client_secret=") is NOT treated the same as an omitted key -- it
// becomes an explicit empty-string pointer, which is what lets Engine tell
// "leave unchanged" apart from "caller tried to blank this out".
func TestConnectFieldsFromInline_BlankValueIsExplicit(t *testing.T) {
	req := connectFieldsFromInline("oauth", "client_secret=")
	if req.ClientSecret == nil || *req.ClientSecret != "" {
		t.Fatalf("expected an explicit empty client_secret pointer, got %#v", req.ClientSecret)
	}
}

// TestValidateConnectArgs covers the action/arity rules a malformed command
// line should fail on before ever reaching the network.
func TestValidateConnectArgs(t *testing.T) {
	connectSetInteractive = false
	t.Cleanup(func() { connectSetInteractive = false })

	if err := validateConnectArgs(nil, []string{"jira"}); err == nil {
		t.Fatal("expected error: missing action")
	}
	if err := validateConnectArgs(nil, []string{"jira", "remove"}); err == nil {
		t.Fatal("expected error: unsupported action")
	}
	if err := validateConnectArgs(nil, []string{"jira", "set", "a=b", "extra"}); err == nil {
		t.Fatal("expected error: too many positional args")
	}
	if err := validateConnectArgs(nil, []string{"jira", "set", "client_id=x"}); err != nil {
		t.Fatalf("expected valid inline form to pass, got %v", err)
	}

	connectSetInteractive = true
	if err := validateConnectArgs(nil, []string{"jira", "set", "client_id=x"}); err == nil {
		t.Fatal("expected error: value arg not allowed in interactive mode")
	}
	if err := validateConnectArgs(nil, []string{"jira", "set"}); err != nil {
		t.Fatalf("expected bare interactive form to pass, got %v", err)
	}
}

// TestValidateConnectArgs_Get proves get has its own, stricter arity rule
// than set -- it never takes a value, inline or interactive, so any trailing
// arg is a mistake worth catching before a network round trip.
func TestValidateConnectArgs_Get(t *testing.T) {
	connectSetInteractive = false
	t.Cleanup(func() { connectSetInteractive = false })

	if err := validateConnectArgs(nil, []string{"jira", "get"}); err != nil {
		t.Fatalf("expected bare get form to pass, got %v", err)
	}
	if err := validateConnectArgs(nil, []string{"jira", "get", "client_id=x"}); err == nil {
		t.Fatal("expected error: get accepts no value argument")
	}
}

// TestConnectSet_CreateThenPartialUpdate is the end-to-end path: a first
// call must send every field, and a second call rotating only redirect_uri
// must send just that field (plus the always-included auth_type) -- proving
// the CLI's partial-update support actually reaches the wire correctly, not
// just that the local struct-building helpers look right in isolation.
func TestConnectSet_CreateThenPartialUpdate(t *testing.T) {
	connectSetType = ""
	connectSetInteractive = false
	var requests []map[string]any

	server := connectTargetServer(t, func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode connect-config body: %v", err)
		}
		requests = append(requests, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"cfg-1","bucket_id":"bucket-1","service_id":"svc-jira",
			"auth_type":"oauth","enabled":true,
			"redirect_uri":"https://engine.example.com/connect/callback",
			"has_client_id":true,"has_client_secret":true,
			"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"
		}`))
	})
	defer server.Close()

	dir := t.TempDir()
	runCommandInDirOutput(t, dir, server.URL, []string{
		"connect", "jira", "set",
		"client_id=abc123;client_secret=shh;redirect_uri=https://engine.example.com/connect/callback",
		"--bucket", "customer-accounts",
	})
	runCommandInDirOutput(t, dir, server.URL, []string{
		"connect", "jira", "set",
		"redirect_uri=https://engine.example.com/connect/new-callback",
		"--bucket", "customer-accounts",
	})

	if len(requests) != 2 {
		t.Fatalf("expected 2 connect-config requests, got %d", len(requests))
	}
	create, update := requests[0], requests[1]

	for _, key := range []string{"auth_type", "client_id", "client_secret", "redirect_uri"} {
		if _, ok := create[key]; !ok {
			t.Fatalf("expected create request to include %q, got %#v", key, create)
		}
	}

	if _, ok := update["client_id"]; ok {
		t.Fatalf("expected partial update to omit client_id, got %#v", update)
	}
	if _, ok := update["client_secret"]; ok {
		t.Fatalf("expected partial update to omit client_secret, got %#v", update)
	}
	if update["redirect_uri"] != "https://engine.example.com/connect/new-callback" {
		t.Fatalf("expected partial update to carry the new redirect_uri, got %#v", update)
	}
	if update["auth_type"] != "oauth" {
		t.Fatalf("expected auth_type to still be resolved on a partial update, got %#v", update)
	}
}

// connectTargetServer stands up the same bucket/service resolution GraphQL
// responses both TestConnectSet_CreateThenPartialUpdate and the get tests
// below need, then defers to connectConfigHandler for the connect-config
// path itself -- shared so a get test never has to duplicate the resolution
// fixture just to exercise a different HTTP method on the same endpoint.
func connectTargetServer(t *testing.T, connectConfigHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			body := decodeTestGraphQLBody(t, r)
			switch {
			case strings.Contains(body.Query, "serviceVersions"):
				_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-1","service_id":"svc-jira","name":"2026-07-01","status":"public","created_at":"2026-07-16T00:00:00Z"}]}}`))
			case strings.Contains(body.Query, "GetServiceInfo"):
				_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-jira","name":"Jira","slug":"jira","auth_configs":[{"name":"oauth","type":"oauth2"}]}}}`))
			default:
				t.Fatalf("unexpected registry graphql query: %s", body.Query)
			}
		case r.URL.Path == "/engine/graphql":
			body := decodeTestGraphQLBody(t, r)
			if !strings.Contains(body.Query, "bucketSummaryPage") {
				t.Fatalf("unexpected engine graphql query: %s", body.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"bucketSummaryPage":{"total":1,"items":[{"id":"bucket-1","name":"customer-accounts","is_default":false,"secret_count":0,"value_count":0,"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"}]}}}`))
		case strings.HasSuffix(r.URL.Path, "/connect-config"):
			connectConfigHandler(w, r)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

// TestConnectGet_ReturnsExistingConfig proves get issues a GET (never a PUT,
// so it can never accidentally rotate anything) and prints back exactly the
// safe fields Engine's projection returns.
func TestConnectGet_ReturnsExistingConfig(t *testing.T) {
	server := connectTargetServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected connect get to issue a GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"cfg-1","bucket_id":"bucket-1","service_id":"svc-jira",
			"auth_type":"oauth","enabled":true,
			"redirect_uri":"https://engine.example.com/connect/callback",
			"has_client_id":true,"has_client_secret":true,
			"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"
		}`))
	})
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{
		"connect", "jira", "get", "--bucket", "customer-accounts",
	})
	if !strings.Contains(out, "auth_type=oauth") || !strings.Contains(out, "has_client_secret=true") {
		t.Fatalf("expected connect config fields in output, got %q", out)
	}
}

// TestConnectGet_NotFoundReportsFriendlyError proves a bucket+service with
// nothing registered yet fails with a message pointing at `connect set`,
// not a raw HTTP 404.
func TestConnectGet_NotFoundReportsFriendlyError(t *testing.T) {
	server := connectTargetServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "connect config not found", http.StatusNotFound)
	})
	defer server.Close()

	out := runCommandInDirExpectError(t, t.TempDir(), server.URL, []string{
		"connect", "jira", "get", "--bucket", "customer-accounts",
	})
	if !strings.Contains(out, "no connect config registered") || !strings.Contains(out, "connect jira set") {
		t.Fatalf("expected a friendly not-found message pointing at connect set, got %q", out)
	}
}
