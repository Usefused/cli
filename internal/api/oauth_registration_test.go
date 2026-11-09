package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRegisterTemporaryOAuthClientUsesAnonymousEndpoint proves registration never sends an ambient CLI key.
func TestRegisterTemporaryOAuthClientUsesAnonymousEndpoint(t *testing.T) {
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	calls := 0
	// The fake Engine checks that even an API client with a saved key registers anonymously.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// A saved control credential must never accompany this public registration request.
		if r.URL.Path != "/oauth/register" || r.Method != http.MethodPost || r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected registration request: %s %s", r.Method, r.URL.Path)
		}
		var input TemporaryOAuthClientRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Name != "Local app" || input.RedirectURI != "http://localhost:4321/callback" || len(input.Scopes) != 1 || input.Scopes[0] != "service.read" {
			t.Errorf("registration input = %#v, %v", input, err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"client_id":"foc_test","client_secret":"fos_once","client_id_expires_at":%q}`, expiry.Format(time.RFC3339))
	}))
	defer server.Close()
	client := NewClient(server.URL, "saved-control-key")
	result, err := client.RegisterTemporaryOAuthClient(TemporaryOAuthClientRequest{Name: "Local app", RedirectURI: "http://localhost:4321/callback", Scopes: []string{"service.read"}})
	// Only Engine's complete one-time response may become a CLI receipt.
	if err != nil || calls != 1 || result.ClientID != "foc_test" || result.ClientSecret != "fos_once" || !result.ClientIDExpiresAt.Equal(expiry) {
		t.Fatalf("registration = %#v, calls=%d, err=%v", result, calls, err)
	}
}

// TestOAuthRegistrationScopesReadsPublicMetadata proves the picker needs no control credential or hardcoded scope list.
func TestOAuthRegistrationScopesReadsPublicMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Discovery must read only the public issuer metadata and send no saved API key.
		if r.Method != http.MethodGet || r.Header.Get("X-API-Key") != "" {
			t.Errorf("unexpected scope discovery request: %s %s", r.Method, r.URL.Path)
		}
		// The main and MCP issuers contribute their distinct advertised scopes.
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_, _ = w.Write([]byte(`{"scopes_supported":["service.read","app.sdk.read","service.read"]}`))
		case "/.well-known/oauth-authorization-server/mcp/oauth":
			_, _ = w.Write([]byte(`{"scopes_supported":["mcp.execute"]}`))
		default:
			t.Errorf("unexpected discovery route %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "saved-control-key")
	values, err := client.OAuthRegistrationScopes()
	// Unique alphabetical values make the terminal picker stable across Engine ordering changes.
	if err != nil || len(values) != 3 || values[0] != "app.sdk.read" || values[1] != "mcp.execute" || values[2] != "service.read" {
		t.Fatalf("scope catalog = %v, %v", values, err)
	}
}

// TestOAuthRegistrationScopesAllowsAbsentMCPIssuer keeps local management registration usable on older Engines.
func TestOAuthRegistrationScopesAllowsAbsentMCPIssuer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An absent MCP route does not invalidate the main issuer's scope list.
		if r.URL.Path == "/.well-known/oauth-authorization-server/mcp/oauth" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"scopes_supported":["service.read"]}`))
	}))
	defer server.Close()
	values, err := NewClient(server.URL, "").OAuthRegistrationScopes()
	if err != nil || len(values) != 1 || values[0] != "service.read" {
		t.Fatalf("scope catalog = %v, %v", values, err)
	}
}

// TestRegisterTemporaryOAuthClientRejectsIncompleteResponses keeps partial or malformed pairs out of output.
func TestRegisterTemporaryOAuthClientRejectsIncompleteResponses(t *testing.T) {
	for _, body := range []string{
		`{"client_id":"foc_test","client_secret":"fos_once"}`,
		`{"client_id":"foc_test","client_secret":"fos_once","client_id_expires_at":"invalid"}`,
		`{"client_id":"foc_test","client_secret":"fos_once","client_id_expires_at":"2000-01-01T00:00:00Z"}`,
		`{"client_id":"foc_test","client_secret":"fos_once","client_id_expires_at":"2099-01-01T00:00:00Z"} {}`,
	} {
		// Each malformed response must be rejected without echoing the supplied secret.
		t.Run(body, func(t *testing.T) {
			// Each case returns one independent malformed Engine response.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client := NewClient(server.URL, "")
			_, err := client.RegisterTemporaryOAuthClient(TemporaryOAuthClientRequest{})
			if err == nil || strings.Contains(err.Error(), "fos_once") {
				t.Fatalf("malformed response error = %v", err)
			}
		})
	}
}

// TestRegisterTemporaryOAuthClientDoesNotFollowRedirect keeps registration metadata on the selected Engine origin.
func TestRegisterTemporaryOAuthClientDoesNotFollowRedirect(t *testing.T) {
	forwarded := 0
	// The destination counts any unintended HTTP request after a redirect.
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded++ }))
	defer target.Close()
	// The selected Engine redirects registration to an unrelated origin.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := NewClient(server.URL, "")
	_, err := client.RegisterTemporaryOAuthClient(TemporaryOAuthClientRequest{})
	// A redirect is a failed registration and must not reach another host.
	if err == nil || forwarded != 0 {
		t.Fatalf("redirect result = %v, forwarded=%d", err, forwarded)
	}
}
