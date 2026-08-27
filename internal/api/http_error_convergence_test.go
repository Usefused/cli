package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip lets focused client tests provide deterministic transport outcomes.
func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type countingReadCloser struct {
	reader io.Reader
	read   int
}

// Read records how much of an adversarial response body the client consumes.
func (body *countingReadCloser) Read(buffer []byte) (int, error) {
	count, err := body.reader.Read(buffer)
	body.read += count
	return count, err
}

// Close satisfies the HTTP response-body contract for the focused reader test.
func (body *countingReadCloser) Close() error {
	return nil
}

// TestControlPlaneTransportErrorsAreTypedSafeAndCauseAware verifies that all
// ordinary request failures share one stable contract without losing context semantics.
func TestControlPlaneTransportErrorsAreTypedSafeAndCauseAware(t *testing.T) {
	tests := []struct {
		name      string
		cause     error
		wantCode  string
		retryable bool
	}{
		{name: "connection", cause: errors.New("dial https://private.engine.test?token=fsk_hidden"), wantCode: "engine_unavailable", retryable: true},
		{name: "cancelled", cause: fmt.Errorf("private request: %w", context.Canceled), wantCode: "request_cancelled"},
		{name: "deadline", cause: fmt.Errorf("private request: %w", context.DeadlineExceeded), wantCode: "request_timed_out", retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, test.cause
			})}}
			request, err := http.NewRequest(http.MethodGet, "https://private.engine.test/control", nil)
			// Request construction is local test setup and must succeed for the transport assertion.
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.doRequest(request)
			var apiErr *APIError
			// Transport failures must remain typed for automation consumers.
			if !errors.As(err, &apiErr) {
				t.Fatalf("error type = %T, want *APIError", err)
			}
			// The stable classifier and retry policy must move together.
			if apiErr.Code != test.wantCode || apiErr.Retryable != test.retryable {
				t.Fatalf("APIError = %#v, want code/retryable %q/%v", apiErr, test.wantCode, test.retryable)
			}
			// Wrapping must preserve context cancellation and deadline matching.
			if !errors.Is(err, test.cause) {
				t.Fatalf("error %v does not retain cause %v", err, test.cause)
			}
			// Rendered errors must not contain private URLs or credentials from net/http.
			if strings.Contains(err.Error(), "private.engine.test") || strings.Contains(err.Error(), "fsk_hidden") {
				t.Fatalf("transport error leaked request context: %q", err)
			}
		})
	}
}

// TestGraphQLHTTPFailuresUseTheBoundedReader proves the HTTP error path cannot
// consume an unbounded proxy or Engine response body.
func TestGraphQLHTTPFailuresUseTheBoundedReader(t *testing.T) {
	body := &countingReadCloser{reader: strings.NewReader(`{"error":{"code":"dependency_failed","message":"Safe dependency diagnosis.","category":"dependency","retryable":true}}` + strings.Repeat(" ", int(maxCLIHTTPErrorBytes)+4096))}
	client := &Client{BaseURL: "https://private.engine.test", HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: body, Header: make(http.Header)}, nil
	})}}
	err := client.GraphQL("query { health }", nil, &struct{}{})
	// The failure must remain classified instead of accepting a partial body as success.
	if err == nil || !strings.Contains(err.Error(), "dependency_failed") {
		t.Fatalf("GraphQL error = %v", err)
	}
	// LimitReader may inspect one sentinel byte beyond the public payload ceiling.
	if body.read > int(maxCLIHTTPErrorBytes)+1 {
		t.Fatalf("GraphQL error reader consumed %d bytes", body.read)
	}
}

// TestSafeServerDetailRedactsOnlySensitiveFragments keeps useful structured
// diagnostics while removing individual credentials, tokens, and URLs.
func TestSafeServerDetailRedactsOnlySensitiveFragments(t *testing.T) {
	input := `provider rejected password=hunter2 at https://private.engine.test/path with Authorization: Bearer abc.def and fsk_visible`
	detail := safeServerDetail(input)
	for _, want := range []string{"provider rejected", "at", "with", "[redacted]", "[redacted URL]", "[redacted token]"} {
		// Surrounding diagnosis and explicit redaction markers should remain visible.
		if !strings.Contains(detail, want) {
			t.Fatalf("detail %q does not contain %q", detail, want)
		}
	}
	for _, secret := range []string{"hunter2", "private.engine.test", "abc.def", "fsk_visible"} {
		// No sensitive fragment may survive the shared sanitizer.
		if strings.Contains(detail, secret) {
			t.Fatalf("detail leaked %q: %q", secret, detail)
		}
	}
	structured := newHTTPError(http.StatusBadRequest, []byte(`{"error":{"code":"request_rejected","message":"provider rejected password=hunter2 at https://private.engine.test/path"}}`))
	// Structured envelope messages use the same fragment redactor as GraphQL
	// diagnostics rather than trusting accidental secret values.
	if !strings.Contains(structured.Error(), "provider rejected") || !strings.Contains(structured.Error(), "[redacted]") || strings.Contains(structured.Error(), "hunter2") || strings.Contains(structured.Error(), "private.engine.test") {
		t.Fatalf("structured message was not safely redacted: %q", structured)
	}
}

// TestStructuredDiagnosticsRejectControlsAndPrivateKeys proves every rendered
// current-envelope prose field shares the terminal and credential boundary.
func TestStructuredDiagnosticsRejectControlsAndPrivateKeys(t *testing.T) {
	body := []byte(`{"error":{"code":"request_rejected","message":"\u001b[31mspoofed","category":"validation","remediation":"-----BEGIN PRIVATE KEY-----never-print","recovery":"fused-cli retry --token=fsk_hidden","trace_id":"trace\u001b[2J"}}`)
	err := newHTTPError(http.StatusBadRequest, body)
	message := err.Error()
	// Unsafe remote prose and metadata must fall back or disappear without reaching terminal output.
	for _, forbidden := range []string{"\x1b", "spoofed", "PRIVATE KEY", "never-print", "fsk_hidden"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("structured diagnostic leaked %q: %q", forbidden, message)
		}
	}
	var apiErr *APIError
	// JSON output consumes these typed fields directly, so sanitization must happen during decoding rather than only in Error().
	if !errors.As(err, &apiErr) || apiErr.Remediation != "" || apiErr.Recovery == "" || apiErr.TraceID != "" {
		t.Fatalf("structured fields were not sanitized: %#v", apiErr)
	}
	if strings.Contains(apiErr.Recovery, "fsk_hidden") || !strings.Contains(apiErr.Recovery, "[redacted]") {
		t.Fatalf("recovery command was not fragment-redacted: %q", apiErr.Recovery)
	}
}

// TestGraphQLResolverErrorsAggregateSafelyAndClassifyDeterministically verifies
// multi-field failures retain every safe diagnosis under one stable local policy.
func TestGraphQLResolverErrorsAggregateSafelyAndClassifyDeterministically(t *testing.T) {
	first := graphQLResolverError{Message: "account lookup failed for password=hunter2"}
	first.Extensions.Code = graphQLCodeResourceNotFound
	second := graphQLResolverError{Message: "catalogue unavailable at https://private.engine.test/graphql"}
	second.Extensions.Code = graphQLCodeInternalServer

	assertResult := func(t *testing.T, resolverErrors []graphQLResolverError) {
		t.Helper()
		err := safeGraphQLRequestErrors(resolverErrors)
		var apiErr *APIError
		// Resolver order must never alter the typed dependency outcome.
		if !errors.As(err, &apiErr) || apiErr.Code != "graphql_dependency_failed" || !apiErr.Retryable {
			t.Fatalf("GraphQL error = %#v", err)
		}
		for _, want := range []string{"account lookup failed", "catalogue unavailable", "[redacted]", "[redacted URL]"} {
			// Every useful resolver context must survive aggregation.
			if !strings.Contains(apiErr.Details.ServerDetail, want) {
				t.Fatalf("aggregate %q does not contain %q", apiErr.Details.ServerDetail, want)
			}
		}
		for _, secret := range []string{"hunter2", "private.engine.test"} {
			// Aggregation must not reintroduce content removed by per-message sanitization.
			if strings.Contains(apiErr.Details.ServerDetail, secret) {
				t.Fatalf("aggregate leaked %q: %q", secret, apiErr.Details.ServerDetail)
			}
		}
	}
	assertResult(t, []graphQLResolverError{first, second})
	assertResult(t, []graphQLResolverError{second, first})

	long := graphQLResolverError{Message: strings.Repeat("useful ", 400)}
	long.Extensions.Code = graphQLCodeInternalServer
	var bounded *APIError
	// The helper always returns APIError, and the assertion documents the typed bound.
	if !errors.As(safeGraphQLRequestErrors([]graphQLResolverError{long, long}), &bounded) {
		t.Fatal("bounded GraphQL error was not typed")
	}
	// One ellipsis rune may follow the 1024-rune public diagnostic ceiling.
	if utf8.RuneCountInString(bounded.Details.ServerDetail) > 1025 {
		t.Fatalf("aggregate detail has %d runes", utf8.RuneCountInString(bounded.Details.ServerDetail))
	}
}
