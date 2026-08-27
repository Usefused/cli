package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestGetConnectConfigMapsOnlyExactAbsentConfig verifies a reviewed missing
// registration is the sole 404 converted to the friendly sentinel.
func TestGetConnectConfigMapsOnlyExactAbsentConfig(t *testing.T) {
	client := NewClient("https://engine.example", "fsk_saved")
	client.HTTP.Transport = cliLoginRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"connect_config_not_found","message":"No connect configuration exists for this bucket and service.","category":"not_found","retryable":false}}`))}, nil
	})
	_, err := client.GetConnectConfig("bucket", "service")
	if !errors.Is(err, ErrConnectConfigNotFound) {
		t.Fatalf("GetConnectConfig error = %v", err)
	}
}

// TestGetConnectConfigPreservesBucketNotFound verifies the CLI does not hide a
// bad bucket selection behind the unset-registration message.
func TestGetConnectConfigPreservesBucketNotFound(t *testing.T) {
	client := NewClient("https://engine.example", "fsk_saved")
	client.HTTP.Transport = cliLoginRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"bucket_not_found","message":"The selected bucket was not found.","category":"not_found","retryable":false,"remediation":"Choose a bucket from fused-cli bucket list."}}`))}, nil
	})
	_, err := client.GetConnectConfig("bucket", "service")
	if err == nil || errors.Is(err, ErrConnectConfigNotFound) || !strings.Contains(err.Error(), "bucket_not_found") {
		t.Fatalf("GetConnectConfig error = %v", err)
	}
}

// TestGetConnectConfigDoesNotMaskServerFailure verifies a malformed status/code
// pairing cannot turn an Engine outage into the friendly absent-config sentinel.
func TestGetConnectConfigDoesNotMaskServerFailure(t *testing.T) {
	client := NewClient("https://engine.example", "fsk_saved")
	client.HTTP.Transport = cliLoginRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"connect_config_not_found","message":"No connect configuration exists.","category":"not_found","retryable":false}}`))}, nil
	})
	_, err := client.GetConnectConfig("bucket", "service")
	// Only an actual 404 is evidence that the registration is absent.
	if err == nil || errors.Is(err, ErrConnectConfigNotFound) || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("GetConnectConfig error = %v", err)
	}
}
