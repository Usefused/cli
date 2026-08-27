package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAppFamilyReferenceMissingNamesTheAdapter keeps absent SDKs and MCPs actionable and kind-specific.
func TestAppFamilyReferenceMissingNamesTheAdapter(t *testing.T) {
	// An authoritative missing result is the only basis for missing-app guidance.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"appFamilyReference":null},"errors":[{"message":"resource was not found","extensions":{"code":"FUSED_RESOURCE_NOT_FOUND"}}]}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fixture-key")
	// Both adapters share the same safety boundary and retain their public product labels.
	for _, kind := range []string{"mcp", "sdk"} {
		id, err := client.resolveAppFamilyReference("google-workspace-mcp", kind)
		var apiErr *APIError
		// No opaque identity may survive a rejected resolution.
		if id != "" || !errors.As(err, &apiErr) || apiErr.Code != "resource_not_found" {
			t.Fatalf("resolution = %q, %v", id, err)
		}
		// Permission-filtered absence must not disclose whether an inaccessible app exists.
		if !strings.Contains(apiErr.Message, strings.ToUpper(kind)+` "google-workspace-mcp"`) || !strings.Contains(apiErr.Message, "not accessible") || !strings.Contains(apiErr.Remediation, "fused-cli "+kind+" list") {
			t.Fatalf("missing-app diagnostic = %v", err)
		}
	}
}

// TestAppFamilyReferenceRejectsIncompleteSuccess prevents null identities from reaching token mutations.
func TestAppFamilyReferenceRejectsIncompleteSuccess(t *testing.T) {
	// A malformed success is not proof that the target app is missing.
	for _, body := range []string{`{"data":{"appFamilyReference":null}}`, `{"data":{}}`, `{"data":{"appFamilyReference":{"kind":"app_family"}}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { // Return one incomplete contract shape.
			_, _ = w.Write([]byte(body))
		}))
		id, err := NewClient(server.URL, "fixture-key").ResolveMCPFamilyReference("missing")
		server.Close()
		// Preserve the response-shape failure instead of inventing a not-found result.
		if id != "" || err != errGraphQLDataMalformed {
			t.Fatalf("incomplete resolution = %q, %v", id, err)
		}
	}
}

// TestAppFamilyReferenceErrorsPreserveFailuresAndRedactNames prevents unsafe guidance or input echo.
func TestAppFamilyReferenceErrorsPreserveFailuresAndRedactNames(t *testing.T) {
	// Authentication, permission, ambiguity, and transport failures remain their own errors.
	for _, err := range []error{errors.New("transport unavailable"), &APIError{Code: "permission_denied"}, errGraphQLResourceAmbiguous} {
		if got := appFamilyReferenceError(err, "missing", "mcp"); got != err { // Identity proves no reclassification occurred.
			t.Fatalf("failure changed: %v", got)
		}
	}
	// Terminal and credential-shaped names must not be reflected in the public error.
	for _, name := range []string{"fsk_secret", "\x1b[31mforged", "hidden\u202ename"} {
		if got := appFamilyReferenceError(errGraphQLResourceNotFound, name, "mcp"); strings.Contains(got.Error(), name) { // The fallback still names the adapter.
			t.Fatalf("unsafe reference echoed: %v", got)
		}
	}
}
