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
	connectSetType, connectSetAuthName = "", ""
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{{Name: "oauth", Type: "oauth2"}}}
	authType, authName, err := selectConnectAuthType(info, "jira")
	if err != nil || authType != "oauth" || authName != "oauth" {
		t.Fatalf("expected auto-selected oauth scheme, got %q/%q err=%v", authType, authName, err)
	}
}

// TestSelectConnectAuthType_RejectsServiceWithNoInteractiveScheme guards the
// documented rule that connect only supports oauth/oidc -- basic/api_key/
// bearer/mtls have no browser consent step (see fused-config).
func TestSelectConnectAuthType_RejectsServiceWithNoInteractiveScheme(t *testing.T) {
	connectSetType, connectSetAuthName = "", ""
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{{Name: "apiKey", Type: "apiKey"}}}
	if _, _, err := selectConnectAuthType(info, "stripe"); err == nil {
		t.Fatal("expected error for a service with no oauth/oidc scheme")
	}
}

// TestSelectConnectAuthType_RequiresTypeFlagWhenAmbiguous mirrors secret.go's
// existing --type disambiguation for a service offering both oauth and oidc.
func TestSelectConnectAuthType_RequiresTypeFlagWhenAmbiguous(t *testing.T) {
	connectSetType, connectSetAuthName = "", ""
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{
		{Name: "oauth", Type: "oauth2"},
		{Name: "oidc", Type: "openidconnect"},
	}}
	if _, _, err := selectConnectAuthType(info, "acme"); err == nil {
		t.Fatal("expected ambiguity error without --type")
	}

	connectSetType = "oidc"
	t.Cleanup(func() { connectSetType, connectSetAuthName = "", "" })
	authType, authName, err := selectConnectAuthType(info, "acme")
	if err != nil || authType != "oidc" || authName != "oidc" {
		t.Fatalf("expected --type to select oidc, got %q/%q err=%v", authType, authName, err)
	}
}

func TestSelectConnectAuthTypeRequiresExactNameForTwoOAuthSchemes(t *testing.T) {
	connectSetType, connectSetAuthName = "oauth", ""
	t.Cleanup(func() { connectSetType, connectSetAuthName = "", "" })
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{
		{Name: "adminOAuth", Type: "oauth2"},
		{Name: "userOAuth", Type: "oauth2"},
	}}

	if _, _, err := selectConnectAuthType(info, "acme"); err == nil || !strings.Contains(err.Error(), "--auth-name") {
		t.Fatalf("expected same-type ambiguity error, got %v", err)
	}
	connectSetAuthName = "userOAuth"
	authType, authName, err := selectConnectAuthType(info, "acme")
	if err != nil || authType != "oauth" || authName != "userOAuth" {
		t.Fatalf("expected exact OAuth scheme, got %q/%q err=%v", authType, authName, err)
	}
}

// TestConnectFieldsFromInline_OnlySendsProvidedKeys is the crux of partial
// update: a key that never appears in the inline value must produce a nil
// pointer (omitted from the JSON request, meaning "leave unchanged" to
// Engine), not a zero-value empty string (which Engine rejects as "blanked
// out").
func TestConnectFieldsFromInline_OnlySendsProvidedKeys(t *testing.T) {
	req, err := connectFieldsFromInline("oauth", "jiraOAuth", "redirect_uri=https://engine.example.com/connect/callback")
	// Valid connect assignments should survive the parser unchanged.
	if err != nil {
		t.Fatalf("parse connect fields: %v", err)
	}
	if req.AuthType == nil || *req.AuthType != "oauth" {
		t.Fatalf("expected auth_type to always be set, got %#v", req.AuthType)
	}
	if req.ClientID != nil || req.ClientSecret != nil {
		t.Fatalf("expected client_id/client_secret to stay nil when not provided, got id=%v secret=%v", req.ClientID, req.ClientSecret)
	}
	if req.AuthName == nil || *req.AuthName != "jiraOAuth" {
		t.Fatalf("expected auth_name to always be set, got %#v", req.AuthName)
	}
	if req.RedirectURI == nil || *req.RedirectURI != "https://engine.example.com/connect/callback" {
		t.Fatalf("expected redirect_uri to be set, got %#v", req.RedirectURI)
	}
}

// TestConnectFieldsFromInlineRejectsBlankValue proves an unset shell expansion
// cannot become an explicit empty credential patch.
func TestConnectFieldsFromInlineRejectsBlankValue(t *testing.T) {
	_, err := connectFieldsFromInline("oauth", "jiraOAuth", "client_secret=")
	// Empty connect fields must identify the local input problem rather than build a patch.
	if err == nil || !strings.Contains(err.Error(), "empty value") {
		t.Fatalf("expected an actionable empty-value error, got %v", err)
	}
}

// TestConnectFieldsFromInlineRejectsUnknownField prevents a misspelled
// credential name from becoming a successful no-op patch.
func TestConnectFieldsFromInlineRejectsUnknownField(t *testing.T) {
	_, err := connectFieldsFromInline("oauth", "jiraOAuth", "client_secert=mistyped")
	// The diagnostic must identify the exact unsupported key before any mutation can be authorized.
	if err == nil || !strings.Contains(err.Error(), `invalid connect field "client_secert"`) {
		t.Fatalf("expected an actionable unknown-field error, got %v", err)
	}
}

// TestConnectSetRejectsMalformedInlineInputBeforeRequests verifies command
// preflight runs before bucket and service metadata resolution.
func TestConnectSetRejectsMalformedInlineInputBeforeRequests(t *testing.T) {
	previousInteractive, previousStdin := connectSetInteractive, connectSetValueStdin
	previousBucket := connectSetBucketID
	t.Cleanup(func() {
		connectSetInteractive, connectSetValueStdin = previousInteractive, previousStdin
		connectSetBucketID = previousBucket
		RootCmd.SetIn(nil)
	})
	connectSetInteractive, connectSetValueStdin = false, true

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	RootCmd.SetIn(strings.NewReader("client_id=valid;client_secret="))
	out := runCommandInDirExpectError(t, t.TempDir(), server.URL, []string{
		"connect", "set", "jira", "--value-stdin",
		"--bucket", "11111111-1111-4111-8111-111111111111",
	})
	// The local diagnostic should tell users to check shell expansion or choose interactive input.
	if !strings.Contains(out, `key "client_secret" has an empty value`) || !strings.Contains(out, "shell variables") {
		t.Fatalf("expected actionable local validation error, got %q", out)
	}
	// No metadata or mutation request may occur after malformed credential input.
	if requests != 0 {
		t.Fatalf("malformed connect input sent %d request(s)", requests)
	}
}

// TestValidateConnectArgs covers the action/arity rules a malformed command
// line should fail on before ever reaching the network.
func TestValidateConnectArgs(t *testing.T) {
	connectSetInteractive, connectSetValueStdin = false, false
	connectSetType, connectSetAuthName = "", ""
	t.Cleanup(func() {
		connectSetInteractive = false
		connectSetValueStdin = false
		connectSetType, connectSetAuthName = "", ""
	})

	if err := validateConnectSetArgs(connectSetCmd, []string{"jira"}); err == nil {
		t.Fatal("expected an explicit input mode")
	}
	connectSetValueStdin = true
	if err := validateConnectSetArgs(connectSetCmd, []string{"jira"}); err != nil {
		t.Fatalf("expected stdin form to pass, got %v", err)
	}
	if err := validateConnectSetArgs(connectSetCmd, []string{"jira", "client_id=x"}); err == nil {
		t.Fatal("connect config values in argv must be rejected")
	}
	connectSetType, connectSetAuthName = "", "jiraOAuth"
	if err := validateConnectSetArgs(connectSetCmd, []string{"jira"}); err == nil || !strings.Contains(err.Error(), "--auth-name requires --type") {
		t.Fatalf("expected auth-name/type validation, got %v", err)
	}
	connectSetType, connectSetAuthName = "", ""
	connectSetInteractive, connectSetValueStdin = true, false
	if err := validateConnectSetArgs(connectSetCmd, []string{"jira"}); err != nil {
		t.Fatalf("expected interactive form to pass, got %v", err)
	}
}

// TestConnectSet_CreateThenPartialUpdate is the end-to-end path: a first
// call must send every field, and a second call rotating only redirect_uri
// must send just that field (plus the always-included auth_type) -- proving
// the CLI's partial-update support actually reaches the wire correctly, not
// just that the local struct-building helpers look right in isolation.
func TestConnectSet_CreateThenPartialUpdate(t *testing.T) {
	connectSetType = ""
	connectSetAuthName = ""
	connectSetInteractive = false
	connectSetValueStdin = true
	t.Cleanup(func() {
		connectSetValueStdin = false
		connectSetAuthName = ""
		RootCmd.SetIn(nil)
	})
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
			"auth_type":"oauth","auth_name":"oauth","enabled":true,
			"redirect_uri":"https://engine.example.com/connect/callback",
			"has_client_id":true,"has_client_secret":true,
			"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"
		}`))
	})
	defer server.Close()

	dir := t.TempDir()
	RootCmd.SetIn(strings.NewReader("client_id=abc123;client_secret=shh;redirect_uri=https://engine.example.com/connect/callback"))
	runCommandInDirOutput(t, dir, server.URL, []string{
		"connect", "set", "jira", "--value-stdin",
		"--bucket", "11111111-1111-4111-8111-111111111111",
	})
	RootCmd.SetIn(strings.NewReader("redirect_uri=https://engine.example.com/connect/new-callback"))
	runCommandInDirOutput(t, dir, server.URL, []string{
		"connect", "set", "jira", "--value-stdin",
		"--bucket", "11111111-1111-4111-8111-111111111111",
	})

	if len(requests) != 2 {
		t.Fatalf("expected 2 connect-config requests, got %d", len(requests))
	}
	create, update := requests[0], requests[1]

	for _, key := range []string{"auth_type", "auth_name", "client_id", "client_secret", "redirect_uri"} {
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

// connectTargetServer stands up the same service-resolution GraphQL
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
			"auth_type":"oauth","auth_name":"oauth","enabled":true,
			"redirect_uri":"https://engine.example.com/connect/callback",
			"has_client_id":true,"has_client_secret":true,
			"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"
		}`))
	})
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{
		"connect", "get", "jira", "--bucket", "11111111-1111-4111-8111-111111111111",
	})
	if !strings.Contains(out, "auth_type=oauth") || !strings.Contains(out, "auth_name=oauth") || !strings.Contains(out, "has_client_secret=true") {
		t.Fatalf("expected connect config fields in output, got %q", out)
	}
}

// TestConnectGet_NotFoundReportsFriendlyError proves a bucket+service with
// nothing registered yet fails with a message pointing at `connect set`,
// not a raw HTTP 404.
func TestConnectGet_NotFoundReportsFriendlyError(t *testing.T) {
	server := connectTargetServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"connect_config_not_found","message":"No connect configuration exists for this bucket and service.","category":"not_found","retryable":false,"remediation":"Create it with fused-cli connect set."}}`))
	})
	defer server.Close()

	out := runCommandInDirExpectError(t, t.TempDir(), server.URL, []string{
		"connect", "get", "jira", "--bucket", "11111111-1111-4111-8111-111111111111",
	})
	if !strings.Contains(out, "no connect config registered") || !strings.Contains(out, "connect set jira") {
		t.Fatalf("expected a friendly not-found message pointing at connect set, got %q", out)
	}
}
