package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWhoAmIUsesEffectiveClientCredential(t *testing.T) {
	client := NewClient("https://engine.example", "fsk_effective")
	client.HTTP.Transport = cliLoginRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/auth/whoami" || request.Header.Get("X-API-Key") != "fsk_effective" {
			t.Fatalf("whoami request = %s %s key=%q", request.Method, request.URL.Path, request.Header.Get("X-API-Key"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
			`{"authenticated":true,"account_id":"account","workspace_id":"workspace","subject_id":"subject","subject_kind":"user","display_name":"Martins","credential_id":"credential","credential_source":"managed_cli_login","authentication_method":"api_key","expires_at":"2026-09-01T00:00:00Z"}`,
		))}, nil
	})
	identity, err := client.WhoAmI()
	if err != nil || identity.SubjectID != "subject" || identity.ExpiresAt == nil {
		t.Fatalf("WhoAmI = %#v, %v", identity, err)
	}
}

func TestLogoutCLIUsesCredentialAndRequiresNoContent(t *testing.T) {
	client := NewClient("https://saved.example", "fsk_saved")
	client.HTTP.Transport = cliLoginRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/auth/cli/logout" || request.Header.Get("X-API-Key") != "fsk_saved" {
			t.Fatalf("logout request = %s %s key=%q", request.Method, request.URL.Path, request.Header.Get("X-API-Key"))
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := client.LogoutCLI(); err != nil {
		t.Fatalf("LogoutCLI: %v", err)
	}
}

func TestLogoutCLIMapsUnauthorizedWithoutReflectingBody(t *testing.T) {
	client := NewClient("https://engine.example", "fsk_saved")
	client.HTTP.Transport = cliLoginRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"secret":"fsk_do_not_reflect"}`))}, nil
	})
	if err := client.LogoutCLI(); err != ErrCLILogoutAlreadyInactive {
		t.Fatalf("LogoutCLI error = %v", err)
	}
}
