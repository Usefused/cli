package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestFormatHTTPAuthorizationErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		want      []string
		doNotWant []string
	}{
		{
			name:   "authentication required",
			status: http.StatusUnauthorized,
			body:   `{"error":"authentication_required"}`,
			want:   []string{"authentication required", "valid Fused credential"},
		},
		{
			name:      "unnamed resource does not invent a name",
			status:    http.StatusForbidden,
			body:      `{"error":"permission_denied","missing":[{"permission":"bucket.values.read","resource_type":"bucket","resource_id":"11111111-1111-1111-1111-111111111111"}]}`,
			want:      []string{"permission denied", "view the selected bucket"},
			doNotWant: []string{"production", "secret", "bucket.values.read", "11111111-1111-1111-1111-111111111111"},
		},
		{
			name:      "safe display name is rendered",
			status:    http.StatusForbidden,
			body:      `{"error":"permission_denied","missing":[{"permission":"service.consume","resource_type":"service","resource_id":"22222222-2222-2222-2222-222222222222","display_name":"GitHub"}]}`,
			want:      []string{`use service "GitHub"`},
			doNotWant: []string{"service.consume", "22222222-2222-2222-2222-222222222222"},
		},
		{
			name:   "empty missing list stays actionable",
			status: http.StatusForbidden,
			body:   `{"error":"permission_denied"}`,
			want:   []string{"permission denied", "workspace administrator"},
		},
		{
			name:      "malformed missing entries stay actionable",
			status:    http.StatusForbidden,
			body:      `{"error":"permission_denied","missing":[null,{}, {"permission":"bucket.read"}, {"resource_type":"bucket","resource_id":"44444444-4444-4444-4444-444444444444"}]}`,
			want:      []string{"permission denied", "workspace administrator"},
			doNotWant: []string{" on ", "bucket.read", "44444444-4444-4444-4444-444444444444"},
		},
		{
			name:      "valid requirements survive malformed siblings",
			status:    http.StatusForbidden,
			body:      `{"error":"permission_denied","missing":[null,{"permission":"service.consume","resource_type":"service","resource_id":"55555555-5555-5555-5555-555555555555"}]}`,
			want:      []string{"use the selected service"},
			doNotWant: []string{"undefined", "null", "service.consume", "55555555-5555-5555-5555-555555555555"},
		},
		{
			name:      "credential-shaped display name is omitted",
			status:    http.StatusForbidden,
			body:      `{"error":"permission_denied","missing":[{"permission":"service.consume","resource_type":"service","resource_id":"66666666-6666-6666-6666-666666666666","display_name":"fsk_never_log"}]}`,
			want:      []string{"permission denied", "use the selected service"},
			doNotWant: []string{"fsk_never_log", "service.consume", "66666666-6666-6666-6666-666666666666"},
		},
		{
			name:      "owning team membership is explained",
			status:    http.StatusForbidden,
			body:      `{"error":"permission_denied","missing":[{"permission":"access.manage","resource_type":"workspace","resource_id":"77777777-7777-7777-7777-777777777777"}]}`,
			want:      []string{"not a member of the owning team", "access administrator"},
			doNotWant: []string{"access.manage", "77777777-7777-7777-7777-777777777777"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := formatHTTPErrorBody(test.status, []byte(test.body))
			for _, value := range test.want {
				if !strings.Contains(message, value) {
					t.Errorf("message %q does not contain %q", message, value)
				}
			}
			for _, value := range test.doNotWant {
				if strings.Contains(strings.ToLower(message), value) {
					t.Errorf("message %q unexpectedly contains %q", message, value)
				}
			}
		})
	}
}

func TestAppOwnerErrorsUseAllowlistedProductMessages(t *testing.T) {
	tests := map[string]string{
		"app owner is immutable":                  "already has an owner",
		"app owner is unavailable":                "workspace administrator",
		"owner team was not found or is archived": "active team slug",
		"app owner authorization denied":          "owning team",
	}
	for code, want := range tests {
		message := formatHTTPErrorBody(http.StatusConflict, []byte(`{"error":`+strconv.Quote(code)+`}`))
		if !strings.Contains(message, want) {
			t.Errorf("message for %q = %q", code, message)
		}
	}
}

func TestBucketReadinessErrorKeepsActionableMissingAuthentication(t *testing.T) {
	serviceID := "1795007d-37de-4c5c-bafa-07046a25d8f0"
	body := `{"error":{"code":"bucket_credentials_missing","message":"The selected credential set is missing required authentication material.","category":"validation","retryable":false,"details":{"missing":["` + serviceID + ` (basic:basicAuth_username)","` + serviceID + ` (basic:basicAuth_password)"]},"remediation":"Add the required credentials and create the plan again."}}`
	message := formatHTTPErrorBody(http.StatusBadRequest, []byte(body))

	for _, want := range []string{"bucket_credentials_missing", "required authentication", "basicAuth_username", "basicAuth_password", "create the plan again"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message %q does not contain %q", message, want)
		}
	}
}

func TestStructuredEngineErrorReturnsMessageAndTrace(t *testing.T) {
	body := `{"error":{"code":"registry_request_failed","message":"The Registry could not complete SDK generation.","category":"dependency","retryable":true,"details":{"http_status":503},"remediation":"Retry the request.","trace_id":"0123456789abcdef0123456789abcdef"}}`
	message := formatHTTPErrorBody(http.StatusServiceUnavailable, []byte(body))

	for _, want := range []string{"registry_request_failed", "Registry could not complete SDK generation", "Retry the request", "0123456789abcdef0123456789abcdef"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message %q does not contain %q", message, want)
		}
	}
}

func TestStructuredEngineErrorRemainsTypedThroughWrapping(t *testing.T) {
	body := []byte(`{"error":{"code":"registry_request_failed","message":"Registry dependency failed.","category":"dependency","retryable":true,"remediation":"Retry.","trace_id":"trace-1"}}`)
	wrapped := fmt.Errorf("generate SDK: %w", newHTTPError(http.StatusServiceUnavailable, body))
	var apiError *APIError
	if !errors.As(wrapped, &apiError) {
		t.Fatalf("wrapped error lost APIError: %v", wrapped)
	}
	if apiError.Code != "registry_request_failed" || !apiError.Retryable || apiError.TraceID != "trace-1" || apiError.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("APIError = %#v", apiError)
	}
}

func TestPermissionDeniedTypedDetailsRemainProductSafe(t *testing.T) {
	const resourceID = "22222222-2222-2222-2222-222222222222"
	body := []byte(`{"error":"permission_denied","missing":[{"permission":"service.consume","resource_type":"service","resource_id":"` + resourceID + `","display_name":"GitHub"}]}`)
	var apiError *APIError
	if !errors.As(newHTTPError(http.StatusForbidden, body), &apiError) {
		t.Fatal("permission error was not typed")
	}
	encoded, err := json.Marshal(apiError.Details)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `use service \"GitHub\"`) || strings.Contains(text, "service.consume") || strings.Contains(text, resourceID) {
		t.Fatalf("unsafe permission details: %s", text)
	}
}

// TestUnstructuredAndCredentialBearingErrorDetailsAreNotReturned verifies the
// owner diagnostic path never turns arbitrary or credential-shaped input into output.
func TestUnstructuredAndCredentialBearingErrorDetailsAreNotReturned(t *testing.T) {
	const secret = "fsk_never_return_or_record"
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "plain body", status: http.StatusInternalServerError, body: secret, want: "engine_request_failed"},
		{name: "error field", status: http.StatusBadRequest, body: `{"error":"` + secret + `"}`, want: "request_rejected"},
		{name: "message field", status: http.StatusConflict, body: `{"message":"` + secret + `"}`, want: "request_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := formatHTTPErrorBody(test.status, []byte(test.body))
			if !strings.Contains(message, test.want) {
				t.Fatalf("message %q does not contain stable category %q", message, test.want)
			}
			if strings.Contains(message, secret) {
				t.Fatalf("message contains response body credential: %q", message)
			}
		})
	}
}

// TestValidationErrorDetailsAreBoundedAndStructured verifies only the reviewed
// string fields from actionable validation responses reach terminal output.
func TestValidationErrorDetailsAreBoundedAndStructured(t *testing.T) {
	longDetail := strings.Repeat("a", 1100)
	tests := []struct {
		name      string
		body      string
		want      string
		doNotWant string
	}{
		{name: "error string", body: `{"error":"invalid x-fused-pagination: unknown field items_path"}`, want: "unknown field items_path"},
		{name: "message fallback", body: `{"message":"request schema is invalid"}`, want: "request schema is invalid"},
		{name: "nested error", body: `{"error":{"debug":"internal detail"}}`, doNotWant: "internal detail"},
		{name: "structured detail", body: `{"error":{"code":"request_rejected","message":"request rejected","details":{"server_detail":"schema dialect is unsupported"}}}`, want: "schema dialect is unsupported"},
		{name: "structured credential", body: `{"error":{"code":"request_rejected","message":"request rejected","details":{"server_detail":"fsk_do_not_return"}}}`, doNotWant: "fsk_do_not_return"},
		{name: "bounded detail", body: `{"error":` + strconv.Quote(longDetail) + `}`, want: "…", doNotWant: strings.Repeat("a", 1025)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := formatHTTPErrorBody(http.StatusBadRequest, []byte(test.body))
			if test.want != "" && !strings.Contains(message, test.want) {
				t.Fatalf("message %q does not contain %q", message, test.want)
			}
			if test.doNotWant != "" && strings.Contains(message, test.doNotWant) {
				t.Fatalf("message %q unexpectedly contains %q", message, test.doNotWant)
			}
		})
	}
}

func TestGraphQLDecodeErrorsDoNotReturnRemoteContent(t *testing.T) {
	const secret = "fsk_never_return_or_record"
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed envelope", body: `{"data":{"secret":"` + secret, want: "graphql_response_malformed"},
		{name: "remote graphql message", body: `{"errors":[{"message":"` + secret + `"}]}`, want: "graphql_request_rejected"},
		{name: "malformed data", body: `{"data":{"value":"` + secret + `"}}`, want: "graphql_data_malformed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output struct {
				Value int `json:"value"`
			}
			err := decodeGraphQLData([]byte(test.body), &output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want stable category %q", err, test.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error contains GraphQL credential: %q", err)
			}
		})
	}
}

func TestGraphQLDecodeUsesOnlyAllowlistedSafeErrorCodes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "resource not found", body: `{"errors":[{"message":"untrusted","extensions":{"code":"FUSED_RESOURCE_NOT_FOUND"}}]}`, want: "resource_not_found"},
		{name: "ambiguous resource", body: `{"errors":[{"message":"untrusted","extensions":{"code":"FUSED_RESOURCE_AMBIGUOUS"}}]}`, want: "use the full UUID"},
		{name: "unknown code", body: `{"errors":[{"message":"fsk_never_return","extensions":{"code":"REMOTE_FAILURE"}}]}`, want: "graphql_request_rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := decodeGraphQLData([]byte(test.body), &struct{}{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "untrusted") || strings.Contains(err.Error(), "fsk_never_return") {
				t.Fatalf("error contains remote GraphQL message: %q", err)
			}
		})
	}
}

func TestEngineGraphQLFormatsPermissionDeniedContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"permission_denied","missing":[{"permission":"connection.manage","resource_type":"bucket","resource_id":"33333333-3333-3333-3333-333333333333"}]}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fsk_test")

	err := client.EngineGraphQL(`mutation { example }`, nil, &struct{}{})

	if err == nil {
		t.Fatal("expected permission denial")
	}
	message := err.Error()
	for _, want := range []string{"HTTP 403", "permission denied", "manage connections in the selected bucket"} {
		if !strings.Contains(message, want) {
			t.Errorf("error %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "connection.manage") || strings.Contains(message, "33333333-3333-3333-3333-333333333333") {
		t.Fatalf("normal denial leaked advanced diagnostics: %q", message)
	}
}
