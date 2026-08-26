package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

// TestMCPPlanCredentialDetailsReachHumanAndJSONOutput keeps the real HTTP error
// useful through command wrapping without changing agent-facing field names.
func TestMCPPlanCredentialDetailsReachHumanAndJSONOutput(t *testing.T) {
	// The server provides an existing Engine readiness envelope without live mutations.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"bucket_credentials_missing","message":"Required authentication is missing.","details":{"bucket":{"name":"default"},"missing_credentials":[{"service":"Jira","auth_type":"basic","auth_name":"basicAuth","required_fields":[{"name":"username","secret_key":"basicAuth_username"},{"name":"password","secret_key":"basicAuth_password"}]}]}}}`))
	}))
	defer server.Close()
	_, planErr := cliapi.NewClient(server.URL, "fixture-key").PlanMCPConfig(cliapi.DesiredConfigPlanIntent{Config: json.RawMessage(`{}`)})
	err := fmt.Errorf("failed to plan MCP fused-all-purpose: %w", planErr)
	command := &cobra.Command{Use: "plan"}
	addJSONOutputFlag(command)
	var human bytes.Buffer
	// Human mode must expose the service, scheme, bucket, and exact missing keys.
	if writeErr := writeCommandError(&human, command, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	for _, fragment := range []string{`bucket "default"`, `"Jira" (basic, auth "basicAuth")`, "basicAuth_username", "basicAuth_password"} {
		// The outer command wrapper must not collapse the structured error back to a count.
		if !strings.Contains(human.String(), fragment) {
			t.Fatalf("missing %q: %s", fragment, human.String())
		}
	}
	// The same error must still expose structured requirements, not a formatted string.
	if setErr := command.Flags().Set(jsonOutputFlag, "true"); setErr != nil {
		t.Fatal(setErr)
	}
	var structured bytes.Buffer
	if writeErr := writeCommandError(&structured, command, err); writeErr != nil {
		t.Fatal(writeErr)
	} // Fail rather than accepting incomplete agent output.
	var envelope jsonErrorEnvelope
	if decodeErr := json.Unmarshal(structured.Bytes(), &envelope); decodeErr != nil {
		t.Fatal(decodeErr)
	} // JSON must remain machine-readable.
	assertCredentialJSONRequirement(t, envelope)
}

// assertCredentialJSONRequirement guards the unchanged Engine-owned field shape.
func assertCredentialJSONRequirement(t *testing.T, envelope jsonErrorEnvelope) {
	t.Helper()
	encoded, err := json.Marshal(envelope.Error.Details["missing_credentials"])
	// Use the public typed shape to catch accidental conversion to display prose.
	if err != nil {
		t.Fatal(err)
	}
	var requirements []cliapi.MissingCredentialRequirement
	if err := json.Unmarshal(encoded, &requirements); err != nil {
		t.Fatal(err)
	} // Require exact typed compatibility.
	if len(requirements) != 1 {
		t.Fatalf("requirements = %#v", requirements)
	} // No entry may disappear during human rendering.
	got := requirements[0]
	// Service identity and prompt keys must survive for subsequent explicit remediation.
	if got.Service != "Jira" || got.AuthName != "basicAuth" || len(got.RequiredFields) != 2 || got.RequiredFields[1].SecretKey != "basicAuth_password" {
		t.Fatalf("credential detail = %#v", got)
	}
}
