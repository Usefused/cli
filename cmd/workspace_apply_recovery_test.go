package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
)

// TestWorkspaceApplyAuthFailureRecoversWithExactFile exercises the real CLI
// apply boundary and proves Engine metadata survives local path enrichment.
func TestWorkspaceApplyAuthFailureRecoversWithExactFile(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace auth's config.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  source:
    service_id: "00000000-0000-4000-8000-000000000001"
    versions: [{version: "2026-08-01"}]
  target:
    service_id: "00000000-0000-4000-8000-000000000002"
    versions: [{version: "2026-08-01"}]
buckets:
  default:
    service_config:
      target:
        auth:
          auth_type: bearer
          auth_name: targetBearer
          ref: ${bucket.auth.source.sourceBearer}
`)
	var applyCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Production-warning discovery is read-only and remains separate from the
		// single apply request whose recovery contract this test exercises.
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
			return
		}
		// A recovery command must not be executed implicitly after the rejection.
		if r.URL.Path != "/workspace/config/apply" {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
		applyCalls++
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"workspace_auth_reference_invalid","message":"The workspace auth reference is invalid.","category":"conflict","retryable":false,"phase":"apply_admission","operation_id":"11111111-1111-4111-8111-111111111111","request_id":"request-1","commit_state":"not_committed"}}`))
	}))
	defer server.Close()

	output := runCommandInDirExpectError(t, dir, server.URL, []string{
		"workspace", "apply", "--plan-id", "11111111-1111-4111-8111-111111111111", "-f", path,
	})
	wantRecovery := workspacePlanRecoveryCommand(path)
	// Human output must carry the exact quoted file plus the Engine's proof that
	// another mutation has not already committed.
	for _, want := range []string{wantRecovery, "Phase: apply_admission.", "Commit state: not_committed.", "Operation: 11111111-1111-4111-8111-111111111111."} {
		if !strings.Contains(output, want) {
			t.Fatalf("workspace apply error %q is missing %q", output, want)
		}
	}
	if applyCalls != 1 {
		t.Fatalf("apply calls = %d, want 1", applyCalls)
	}
}

// TestEnrichWorkspaceApplyRecoveryPreservesSlimJSONContract verifies local
// command enrichment does not replace Engine-owned machine fields.
func TestEnrichWorkspaceApplyRecoveryPreservesSlimJSONContract(t *testing.T) {
	path := filepath.Join("configs", "workspace auth.yaml")
	apiError := &cliapi.APIError{
		Code: "workspace_auth_contract_drift", Message: "Auth contracts differ.", Category: "conflict",
		Phase: "apply_admission", OperationID: "11111111-1111-4111-8111-111111111111",
		RequestID: "request-1", CommitState: "not_committed", HTTPStatus: http.StatusConflict,
	}
	err := enrichWorkspaceApplyRecovery(errors.New("not used"), path)
	// Untyped errors cannot be upgraded merely because the CLI has a config path.
	if err == nil || err.Error() != "not used" {
		t.Fatalf("untyped error changed: %v", err)
	}

	enriched := enrichWorkspaceApplyRecovery(apiError, path)
	result := classifyCommandError(workspaceApplyCmd, enriched)
	wantRecovery := workspacePlanRecoveryCommand(path)
	// Stable automation fields remain exact while only recovery gains local context.
	gotFields := []any{result.Code, result.Phase, result.OperationID, result.RequestID, result.CommitState, result.HTTPStatus, result.Recovery}
	wantFields := []any{apiError.Code, apiError.Phase, apiError.OperationID, apiError.RequestID, apiError.CommitState, apiError.HTTPStatus, wantRecovery}
	if !reflect.DeepEqual(gotFields, wantFields) {
		encoded, _ := json.Marshal(result)
		t.Fatalf("enriched workspace error = %s", encoded)
	}
	// Copy-on-enrich prevents surprising mutation of the API client's error.
	if apiError.Recovery != "" {
		t.Fatalf("source API recovery changed to %q", apiError.Recovery)
	}
}

// TestEnrichWorkspaceApplyRecoveryRejectsUnsafeOutcomes ensures code matches
// alone cannot produce a replay command without authoritative commit proof.
func TestEnrichWorkspaceApplyRecoveryRejectsUnsafeOutcomes(t *testing.T) {
	for _, test := range []struct {
		name        string
		phase       string
		commitState string
		path        string
	}{
		{name: "unknown commit", phase: "apply_admission", commitState: "unknown", path: "workspace.yaml"},
		{name: "wrong phase", phase: "workspace_write", commitState: "not_committed", path: "workspace.yaml"},
		{name: "unsafe path", phase: "apply_admission", commitState: "not_committed", path: "workspace\nconfig.yaml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			apiError := &cliapi.APIError{
				Code: "workspace_auth_reference_invalid", Message: "Rejected.",
				Phase: test.phase, CommitState: test.commitState,
			}
			enriched := enrichWorkspaceApplyRecovery(apiError, test.path)
			var got *cliapi.APIError
			if !errors.As(enriched, &got) || got.Recovery != "" {
				t.Fatalf("unsafe outcome recovery changed: %#v", got)
			}
		})
	}
}
