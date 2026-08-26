package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

// TestConfigResolutionDiagnosticReachesJSON proves the MCP HTTP error survives
// command wrapping and the CLI's reviewed JSON projection unchanged.
func TestConfigResolutionDiagnosticReachesJSON(t *testing.T) {
	// The fixture reproduces the Engine response without persisting any plan.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"config_service_unresolved","message":"Service references could not be resolved.","category":"validation","retryable":false,"details":{"server_detail":"Unresolved service references: \"google-drive\"."},"remediation":"Run fused-cli workspace services list to check available service slugs."}}`))
	}))
	defer server.Close()
	_, planErr := cliapi.NewClient(server.URL, "fixture-key").PlanMCPConfig(cliapi.DesiredConfigPlanIntent{Config: json.RawMessage(`{}`)})
	command := &cobra.Command{Use: "plan"}
	addJSONOutputFlag(command)
	// Exercise the same structured-output switch as the real plan command.
	if err := command.Flags().Set(jsonOutputFlag, "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// Stage wrapping must preserve the underlying Engine error for JSON projection.
	if err := writeCommandError(&out, command, fmt.Errorf("failed to plan MCP fixture: %w", planErr)); err != nil {
		t.Fatal(err)
	}
	var envelope jsonErrorEnvelope
	// Assert decoded fields, not a human string that could hide machine regressions.
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	assertConfigResolutionJSON(t, envelope)
}

// assertConfigResolutionJSON keeps the failure selector and actionable detail
// available to agents without exposing unreviewed remote response fields.
func assertConfigResolutionJSON(t *testing.T, envelope jsonErrorEnvelope) {
	t.Helper()
	err := envelope.Error
	// Input failures must remain non-retryable HTTP 400 validation errors.
	if envelope.OK || err.Code != "config_service_unresolved" || err.Category != "validation" || err.HTTPStatus != 400 || err.Retryable {
		t.Fatalf("JSON error = %#v", envelope)
	}
	// The JSON detail and remediation must be as useful as terminal output.
	if err.Details["server_detail"] != `Unresolved service references: "google-drive".` || err.Remediation != "Run fused-cli workspace services list to check available service slugs." {
		t.Fatalf("JSON lost diagnostic: %#v", err)
	}
}
