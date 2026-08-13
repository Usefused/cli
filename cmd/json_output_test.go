package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestAgentReadCommandsExposeJSON(t *testing.T) {
	commands := []*cobra.Command{
		serviceSearchCmd, serviceVersionsCmd, serviceShowCmd, serviceOperationsCmd, serviceOperationShowCmd, serviceWebhooksCmd,
		workspaceServicesListCmd, workspaceHasCmd, workspaceServiceVersionsCmd, workspaceServiceOperationsCmd, workspaceServiceWebhooksCmd,
		bucketListCmd, bucketShowCmd, bucketServicesCmd, bucketSecretsCmd, bucketValuesCmd, bucketConnectionsCmd, bucketSDKsCmd,
		secretListCmd, valueListCmd, sdkListCmd, sdkValidateCmd, sdkShowCmd, sdkServicesCmd, sdkBucketsCmd,
		mcpListCmd, mcpValidateCmd, sdkTokenListCmd, mcpTokenListCmd, connectGetCmd, workspaceConnectionResourcesListCmd,
		validateCmd, webhookValidateCmd, whoAmICmd, workspaceAccessListCmd, teamListCmd, teamShowCmd,
		teamEligibleOwnersCmd, teamBuildAccessCmd, teamMemberListCmd, userListCmd, userShowCmd, configGetCmd, configListCmd, skillListCmd, skillPrintCmd,
	}
	for _, command := range commands {
		if command.Flags().Lookup(jsonOutputFlag) == nil {
			t.Errorf("%s does not expose --json", command.CommandPath())
		}
	}
}

func TestMutationCommandsDoNotInheritJSON(t *testing.T) {
	for _, command := range []*cobra.Command{bucketCreateCmd, secretSetCmd, sdkApplyCmd, mcpApplyCmd, workspaceApplyCmd} {
		if command.Flags().Lookup(jsonOutputFlag) != nil {
			t.Errorf("%s unexpectedly exposes --json", command.CommandPath())
		}
	}
}

func TestWriteJSONPageUsesStablePaginationEnvelope(t *testing.T) {
	command := &cobra.Command{}
	var out bytes.Buffer
	command.SetOut(&out)
	if err := writeJSONPage(command, []map[string]string{{"id": "one"}}, 3, listFlags{Limit: 1, Offset: 2}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Items  []map[string]string `json:"items"`
		Total  int                 `json:"total"`
		Limit  int                 `json:"limit"`
		Offset int                 `json:"offset"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON page: %v", err)
	}
	if len(result.Items) != 1 || result.Total != 3 || result.Limit != 1 || result.Offset != 2 {
		t.Fatalf("JSON page = %#v", result)
	}
}

func TestWriteJSONUsesEmptyArrayForNilSlices(t *testing.T) {
	command := &cobra.Command{}
	var out bytes.Buffer
	command.SetOut(&out)
	var rows []string
	if err := writeJSON(command, rows); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[]\n" {
		t.Fatalf("empty JSON slice = %q", out.String())
	}
}

func TestBucketListJSONUsesAPIPageWithoutHumanSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"bucketSummaryPage":{"total":2,"items":[{"id":"bucket-1","workspace_id":"ws-1","name":"prod","is_default":true,"secret_count":3,"value_count":1,"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"bucket", "list", "--limit", "1", "--offset", "1", "--json"})
	var page jsonPage
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("decode bucket JSON %q: %v", out, err)
	}
	if page.Total != 2 || page.Limit != 1 || page.Offset != 1 {
		t.Fatalf("bucket JSON page = %#v", page)
	}
}

func TestWriteCommandErrorPreservesStructuredEngineFields(t *testing.T) {
	// JSON output exposes the same reviewed server detail as human output so an
	// agent can diagnose validation failures without parsing prose.
	command := &cobra.Command{Use: "inspect"}
	addJSONOutputFlag(command)
	if err := command.Flags().Set(jsonOutputFlag, "true"); err != nil {
		t.Fatal(err)
	}
	apiError := &cliapi.APIError{
		Code: "bucket_credentials_missing", Message: "Required authentication material is missing.",
		Category: "validation", Remediation: "Add credentials and plan again.", TraceID: "trace-1", HTTPStatus: http.StatusBadRequest,
	}
	apiError.Details.Missing = []string{"basicAuth_username", "basicAuth_password"}
	apiError.Details.ServerDetail = `unknown field "items_path"`

	var out bytes.Buffer
	if err := writeCommandError(&out, command, fmt.Errorf("plan failed: %w", apiError)); err != nil {
		t.Fatal(err)
	}
	var envelope jsonErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error JSON %q: %v", out.String(), err)
	}
	if envelope.OK || envelope.Error.Code != apiError.Code || envelope.Error.Message != apiError.Message ||
		envelope.Error.TraceID != "trace-1" || envelope.Error.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("error envelope = %#v", envelope)
	}
	missing, ok := envelope.Error.Details["missing"].([]any)
	if !ok || len(missing) != 2 {
		t.Fatalf("missing details = %#v", envelope.Error.Details)
	}
	if envelope.Error.Details["server_detail"] != apiError.Details.ServerDetail {
		t.Fatalf("server detail = %#v", envelope.Error.Details)
	}
}

func TestWriteCommandErrorStructuresLocalFailures(t *testing.T) {
	command := &cobra.Command{Use: "inspect"}
	addJSONOutputFlag(command)
	_ = command.Flags().Set(jsonOutputFlag, "true")
	var out bytes.Buffer
	if err := writeCommandError(&out, command, errors.New("--version is required")); err != nil {
		t.Fatal(err)
	}
	var envelope jsonErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "command_failed" || envelope.Error.Category != "cli" || envelope.Error.Message != "--version is required" {
		t.Fatalf("local error envelope = %#v", envelope)
	}
}

func TestWriteCommandErrorPreservesStrictImportDiagnostics(t *testing.T) {
	command := &cobra.Command{Use: "import-plan"}
	addJSONOutputFlag(command)
	_ = command.Flags().Set(jsonOutputFlag, "true")
	strictError := &cliapi.SpecImportStrictError{
		HTTPStatus: http.StatusUnprocessableEntity, Code: "strict_import_rejected", Message: "Contract diagnostics must be resolved.",
		Diagnostics: []cliapi.SpecImportDiagnostic{{Code: "missing_operation_id", Severity: "error", Path: "/payments"}},
	}
	var out bytes.Buffer
	if err := writeCommandError(&out, command, strictError); err != nil {
		t.Fatal(err)
	}
	var envelope jsonErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	diagnostics, ok := envelope.Error.Details["diagnostics"].([]any)
	if envelope.Error.Code != "strict_import_rejected" || !ok || len(diagnostics) != 1 {
		t.Fatalf("strict import envelope = %#v", envelope)
	}
}

func TestWriteCommandErrorKeepsHumanOutput(t *testing.T) {
	command := &cobra.Command{Use: "inspect"}
	var out bytes.Buffer
	if err := writeCommandError(&out, command, errors.New("plain failure")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "plain failure\n" {
		t.Fatalf("human error output = %q", out.String())
	}
}

func TestWriteCommandErrorClassifiesCobraUsageFailures(t *testing.T) {
	command := &cobra.Command{Use: "inspect"}
	addJSONOutputFlag(command)
	_ = command.Flags().Set(jsonOutputFlag, "true")
	var out bytes.Buffer
	if err := writeCommandError(&out, command, errors.New("accepts 1 arg(s), received 0")); err != nil {
		t.Fatal(err)
	}
	var envelope jsonErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "invalid_arguments" || envelope.Error.Category != "validation" {
		t.Fatalf("usage error envelope = %#v", envelope)
	}
}

func TestJSONReadCommandRendersStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"registry_request_failed","message":"Registry dependency failed.","category":"dependency","retryable":true,"remediation":"Retry the request.","trace_id":"trace-1"}}`))
	}))
	defer server.Close()

	oldEngineURL, oldAPIKey, oldConfigFile := EngineURL, APIKey, ConfigFile
	t.Cleanup(func() {
		EngineURL, APIKey, ConfigFile = oldEngineURL, oldAPIKey, oldConfigFile
		resetHelpFlags(RootCmd)
	})
	EngineURL, APIKey, ConfigFile = server.URL, "fsk_test", ""
	resetHelpFlags(RootCmd)
	var stdout, cobraErr, rendered bytes.Buffer
	RootCmd.SetOut(&stdout)
	RootCmd.SetErr(&cobraErr)
	RootCmd.SetArgs([]string{"service", "show", "github", "--json"})
	executed, commandErr := RootCmd.ExecuteC()
	if commandErr == nil {
		t.Fatal("expected service show to fail")
	}
	if err := writeCommandError(&rendered, executed, commandErr); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || cobraErr.Len() != 0 {
		t.Fatalf("failure polluted normal output: stdout=%q stderr=%q", stdout.String(), cobraErr.String())
	}
	var envelope jsonErrorEnvelope
	if err := json.Unmarshal(rendered.Bytes(), &envelope); err != nil {
		t.Fatalf("decode rendered error %q: %v", rendered.String(), err)
	}
	if envelope.Error.Code != "registry_request_failed" || !envelope.Error.Retryable || envelope.Error.Command != "fused-cli service show" {
		t.Fatalf("rendered error = %#v", envelope)
	}
}
