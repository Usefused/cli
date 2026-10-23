package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type secretExportRoundTripper func(*http.Request) (*http.Response, error)

// RoundTrip lets tests inspect secret transport without opening sockets or touching a real credential store.
func (f secretExportRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSecretExportClient keeps opt-in defaults, policy mutation, and explicit plaintext retrieval separate.
func TestSecretExportClient(t *testing.T) {
	var requests []map[string]any
	client := secretExportTestClient(t, &requests)
	// Legacy callers must serialize a private default without opting in accidentally.
	if err := client.UpsertBucketSecret("bucket", "key", "synthetic-secret", nil); err != nil {
		t.Fatal(err)
	}
	// Explicit provider-key consent travels in the same write as the new value.
	if err := client.UpsertSecret("service", "key", "api_key", "synthetic-secret", "bucket", nil, true); err != nil {
		t.Fatal(err)
	}
	// Policy changes are independent from secret replacement and never carry a value.
	if err := client.SetBucketSecretExportable("bucket", "secret", false); err != nil {
		t.Fatal(err)
	}
	value, err := client.RevealBucketSecret("bucket", "secret")
	// Retrieval must preserve exact bytes, including intentional trailing newlines.
	if err != nil || value != "synthetic-secret\n" {
		t.Fatal("reveal did not preserve exact bytes")
	}
	// The three mutation contracts must keep defaults, opt-in, and metadata-only policy updates intact.
	if requests[0]["exportable"] != false || requests[1]["exportable"] != true || len(requests[2]) != 1 {
		t.Fatal("incorrect export policy payload")
	}
}

// TestSecretRevealClientSuppressesErrorMaterial ensures failures never echo server bodies containing secret-shaped data.
func TestSecretRevealClientSuppressesErrorMaterial(t *testing.T) {
	client := &Client{BaseURL: "https://engine.test", HTTP: &http.Client{Transport: secretExportRoundTripper(
		// A malicious or broken intermediary response must remain outside CLI error text.
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{"value":"synthetic-secret"}`))}, nil
		})}}
	value, err := client.RevealBucketSecret("bucket", "secret")
	// Failed disclosure returns no value and no raw server response.
	if value != "" || err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("retrieval error exposed material")
	}
}

// secretExportTestClient records only synthetic transport calls without sockets or live credentials.
func secretExportTestClient(t *testing.T, requests *[]map[string]any) *Client {
	t.Helper()
	return &Client{BaseURL: "https://engine.test", APIKey: "synthetic-control-key", HTTP: &http.Client{Transport: secretExportRoundTripper(
		// Capture synthetic request bodies while returning only fixture responses.
		func(r *http.Request) (*http.Response, error) {
			body := map[string]any{}
			// Reveal has no request body; only mutation requests carry JSON.
			if r.ContentLength > 0 {
				_ = json.NewDecoder(r.Body).Decode(&body)
			}
			*requests = append(*requests, body)
			// Provider secrets must never be placed in a path or query string.
			if strings.Contains(r.URL.String(), "synthetic-secret") {
				t.Fatal("secret appeared in URL")
			}
			status, response := 204, ""
			// Reveal deliberately uses POST so cookie-backed clients retain CSRF protection.
			if strings.HasSuffix(r.URL.Path, "/reveal") {
				if r.Method != "POST" {
					t.Fatal("reveal was not POST")
				}
				status, response = 200, `{"value":"synthetic-secret\n"}`
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
		})}}
}
