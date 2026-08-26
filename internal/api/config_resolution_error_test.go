package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const unresolvedServiceResponse = `{"error":{"code":"config_service_unresolved","message":"One or more configured service references could not be resolved unambiguously in this workspace.","category":"validation","retryable":false,"details":{"server_detail":"Unresolved service references (up to 5 shown): \"google-drive\"."},"remediation":"Run ` + "`fused-cli workspace services list`" + ` to check available service slugs, update the config, and plan again."}}`

// TestConfigPlanPreservesUnresolvedServiceDiagnostic checks the real HTTP client
// and wrapping shared by SDK/MCP/webhook, not just the standalone error parser.
func TestConfigPlanPreservesUnresolvedServiceDiagnostic(t *testing.T) {
	// Return the reviewed Engine contract without requiring a live Engine or credential.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(unresolvedServiceResponse))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fixture-key")
	intent := DesiredConfigPlanIntent{ConfigKey: "mcp:fixture:1.0.0", Config: json.RawMessage(`{"services":{"google-drive":{}}}`)}
	plans := []func(DesiredConfigPlanIntent) (*SDKConfigPlanResponse, error){client.PlanSDKConfig, client.PlanMCPConfig, client.PlanWebhookConfig}
	// Every app kind uses the same diagnostic and must retain its typed cause.
	for _, plan := range plans {
		_, err := plan(intent)
		var apiErr *APIError
		// Wrapping must not hide structured status, category, or retry semantics from agents.
		if !errors.As(err, &apiErr) {
			t.Fatalf("untyped plan error: %v", err)
		}
		assertUnresolvedServiceError(t, apiErr, err.Error())
	}
}

// assertUnresolvedServiceError protects both readable context and machine fields.
func assertUnresolvedServiceError(t *testing.T, apiErr *APIError, message string) {
	t.Helper()
	// Missing references are validation failures, not missing grants or retryable outages.
	if apiErr.Code != "config_service_unresolved" || apiErr.Category != "validation" || apiErr.HTTPStatus != 400 || apiErr.Retryable {
		t.Fatalf("typed error = %#v", apiErr)
	}
	// The user must see which key failed and how to discover its replacement.
	for _, wanted := range []string{`"google-drive"`, "fused-cli workspace services list", "HTTP 400"} {
		// Human-readable wrappers must preserve the actionable Engine detail.
		if !strings.Contains(message, wanted) {
			t.Fatalf("missing %q in %s", wanted, message)
		}
	}
	// Validation must never suggest that granting a role will fix the submitted key.
	if strings.Contains(message, "workspace administrator") {
		t.Fatalf("misleading access advice: %s", message)
	}
}
