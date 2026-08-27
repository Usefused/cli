package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type cliLoginRoundTripper func(*http.Request) (*http.Response, error)

func (f cliLoginRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCLILoginClientDoesNotSendAPIKey(t *testing.T) {
	client := NewClient("https://engine.example", "must-not-be-sent")
	client.HTTP.Transport = cliLoginRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/auth/cli/start" || request.Header.Get("X-API-Key") != "" {
			t.Fatalf("unexpected CLI login request: %s key=%q", request.URL.Path, request.Header.Get("X-API-Key"))
		}
		return &http.Response{
			StatusCode: http.StatusCreated, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"transaction_id":"tx","poll_token":"poll","browser_token":"browser","expires_at":"2026-08-06T02:00:00Z"}`)),
		}, nil
	})
	response, err := client.StartCLILogin(CLILoginStartRequest{CredentialHash: strings.Repeat("a", 64), CredentialPrefix: "fsk_abcd"})
	if err != nil || response.TransactionID != "tx" {
		t.Fatalf("StartCLILogin = %#v, %v", response, err)
	}
}

func TestCLILoginClientMapsPendingWithoutReflectingBody(t *testing.T) {
	client := NewClient("https://engine.example", "")
	client.HTTP.Transport = cliLoginRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusAccepted, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"status":"pending","secret":"do-not-reflect"}`)),
		}, nil
	})
	if _, err := client.PollCLILogin("tx", "poll"); err != ErrCLILoginPending {
		t.Fatalf("PollCLILogin error = %v", err)
	}
}

// TestCLILoginClientRejectsLegacyTopLevelFailureCode proves the removed code-only
// shape cannot bypass the current nested-envelope boundary.
func TestCLILoginClientRejectsLegacyTopLevelFailureCode(t *testing.T) {
	client := NewClient("https://engine.example", "")
	client.HTTP.Transport = cliLoginRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"code":"cli_login_denied","secret":"fsk_do_not_reflect"}`)),
		}, nil
	})
	_, err := client.PollCLILogin("tx", "poll")
	// Legacy codes and unrelated secret fields both remain opaque.
	if err == nil || !strings.Contains(err.Error(), "request_forbidden") || strings.Contains(err.Error(), "cli_login_denied") || strings.Contains(err.Error(), "fsk_do_not_reflect") {
		t.Fatalf("PollCLILogin error = %v", err)
	}
}

// TestCLILoginClientPreservesStructuredFailureContext proves the converged
// Engine envelope keeps reviewed correlation while redacting credential fragments.
func TestCLILoginClientPreservesStructuredFailureContext(t *testing.T) {
	client := NewClient("https://engine.example", "")
	client.HTTP.Transport = cliLoginRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"error":{"code":"cli_login_unavailable","message":"Login failed near password=unsafe-value","category":"dependency","retryable":true,"remediation":"Retry later.","request_id":"request-login"}}`)),
		}, nil
	})
	_, err := client.StartCLILogin(CLILoginStartRequest{})
	// Stable context survives, while the credential-shaped fragment is replaced.
	if err == nil || !strings.Contains(err.Error(), "cli_login_unavailable") || !strings.Contains(err.Error(), "request-login") || strings.Contains(err.Error(), "unsafe-value") {
		t.Fatalf("StartCLILogin error = %v", err)
	}
}

// TestCLILoginClientRejectsUnknownFailureCode keeps arbitrary remote codes out
// of terminal diagnostics and falls back to the stable HTTP classification.
func TestCLILoginClientRejectsUnknownFailureCode(t *testing.T) {
	client := NewClient("https://engine.example", "")
	client.HTTP.Transport = cliLoginRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"code":"attacker_owned_code","secret":"fsk_do_not_reflect"}`)),
		}, nil
	})
	_, err := client.StartCLILogin(CLILoginStartRequest{})
	if err == nil || !strings.Contains(err.Error(), "engine_request_failed") || strings.Contains(err.Error(), "attacker_owned_code") || strings.Contains(err.Error(), "fsk_do_not_reflect") {
		t.Fatalf("StartCLILogin error = %v", err)
	}
}
