package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

// TestAppAuthDiagnosticSurvivesCLI proves the existing HTTP, terminal, and JSON
// paths preserve Engine-owned explanations without a second auth formatter.
func TestAppAuthDiagnosticSurvivesCLI(t *testing.T) {
	// This credential-free fixture mirrors the shared SDK/MCP validation envelope.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"auth_selection_incompatible","message":"service \"Jira\" (config key \"jira\") selects auth.type=\"oauth\", auth.name=\"OAuth2\", but 7 selected operation(s) do not support it. Operations within the same service can require different authentication methods.","category":"validation","retryable":false,"details":{"service_id":"22222222-2222-4222-8222-222222222222","server_detail":"Incompatible operations (up to 5 shown): \"getUserEmail\" requires (\"basicAuth\" (basic))"},"remediation":"Replace select_all: true with an explicit list of compatible operations, then plan again."}}`))
	}))
	defer server.Close()
	client := cliapi.NewClient(server.URL, "fixture-key")
	// Both commands must preserve the reviewed message rather than diagnose auth independently.
	for _, plan := range []func(cliapi.DesiredConfigPlanIntent) (*cliapi.SDKConfigPlanResponse, error){client.PlanSDKConfig, client.PlanMCPConfig} {
		_, err := plan(cliapi.DesiredConfigPlanIntent{Config: json.RawMessage(`{}`)})
		assertAppAuthDiagnostic(t, fmt.Errorf("failed to plan app: %w", err))
	}
}

// assertAppAuthDiagnostic checks human output and machine projection from the
// same typed cause, including concrete scheme names and operation requirements.
func assertAppAuthDiagnostic(t *testing.T, err error) {
	t.Helper()
	var apiErr *cliapi.APIError
	// Stable classification must survive all ordinary plan wrappers.
	if !errors.As(err, &apiErr) || apiErr.Code != "auth_selection_incompatible" {
		t.Fatalf("untyped auth failure: %v", err)
	}
	// Terminal users need the selection, affected operations, and a correction in one place.
	for _, want := range []string{`"Jira"`, `auth.name="OAuth2"`, "7 selected operation(s)", `"getUserEmail"`, `"basicAuth" (basic)`, "explicit list of compatible operations"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %s", want, err)
		} // Check wrapped output, not just the bare API error.
	}
	assertAppAuthDiagnosticJSON(t, err, apiErr)
}

// assertAppAuthDiagnosticJSON verifies agents receive the same Engine-owned
// explanation as terminal users through the existing command error projection.
func assertAppAuthDiagnosticJSON(t *testing.T, err error, apiErr *cliapi.APIError) {
	t.Helper()
	command := &cobra.Command{Use: "plan"}
	addJSONOutputFlag(command)
	// JSON is the existing agent-facing error contract; no new command path is necessary.
	if flagErr := command.Flags().Set(jsonOutputFlag, "true"); flagErr != nil {
		t.Fatal(flagErr)
	}
	var out bytes.Buffer
	// Use the production error writer to cover CLI field admission as well as HTTP decoding.
	if writeErr := writeCommandError(&out, command, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	var envelope jsonErrorEnvelope
	// Decoding ensures the assertion covers the emitted wire shape, not internal structs.
	if decodeErr := json.Unmarshal(out.Bytes(), &envelope); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	// Agents must receive the same explanatory fields as terminal users.
	if envelope.Error.Message != apiErr.Message || envelope.Error.Details["server_detail"] != apiErr.Details.ServerDetail || envelope.Error.Remediation != apiErr.Remediation {
		t.Fatalf("lost JSON diagnostic: %#v", envelope.Error)
	}
}
