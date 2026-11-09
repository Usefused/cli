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
