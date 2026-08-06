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
