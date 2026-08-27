package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

const jsonOutputFlag = "json"

type jsonPage struct {
	Items  any `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type jsonErrorEnvelope struct {
	OK    bool            `json:"ok"`
	Error jsonErrorResult `json:"error"`
}

type jsonErrorResult struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Category    string         `json:"category"`
	Retryable   bool           `json:"retryable"`
	Remediation string         `json:"remediation,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	TraceID     string         `json:"trace_id,omitempty"`
	HTTPStatus  int            `json:"http_status,omitempty"`
	Phase       string         `json:"phase,omitempty"`
	OperationID string         `json:"operation_id,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	CommitState string         `json:"commit_state,omitempty"`
	Recovery    string         `json:"recovery,omitempty"`
	Command     string         `json:"command"`
}

// addJSONOutputFlag keeps structured output command-local: mutation commands
// should not advertise a format they cannot provide as a stable receipt.
func addJSONOutputFlag(commands ...*cobra.Command) {
	for _, command := range commands {
		command.Flags().Bool(jsonOutputFlag, false, "Print output as JSON")
	}
}

func wantsJSON(cmd *cobra.Command) bool {
	value, err := cmd.Flags().GetBool(jsonOutputFlag)
	return err == nil && value
}

func writeJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(nonNilJSONSlice(value))
}

func writeJSONPage(cmd *cobra.Command, items any, total int, flags listFlags) error {
	return writeJSON(cmd, jsonPage{
		Items: nonNilJSONSlice(items), Total: total,
		Limit: normalCLIListLimit(flags.Limit), Offset: flags.Offset,
	})
}

func nonNilJSONSlice(value any) any {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		// Agents should see an iterable empty collection, not an ambiguous null,
		// when an API legitimately returns no rows.
		return reflect.MakeSlice(reflected.Type(), 0, 0).Interface()
	}
	return value
}

func validationResult(kind string, count int) map[string]any {
	return map[string]any{"valid": true, "kind": kind, "config_count": count}
}

func writeCommandError(out io.Writer, cmd *cobra.Command, err error) error {
	if cmd == nil || !wantsJSON(cmd) {
		_, writeErr := fmt.Fprintln(out, err)
		return writeErr
	}
	return json.NewEncoder(out).Encode(jsonErrorEnvelope{Error: classifyCommandError(cmd, err)})
}

// classifyCommandError maps typed command failures into stable agent-facing
// fields without requiring callers to parse human prose.
func classifyCommandError(cmd *cobra.Command, err error) jsonErrorResult {
	// Human and JSON output share the same typed error; only the presentation
	// changes, so debugging detail cannot drift between interactive and agent use.
	result := jsonErrorResult{
		Code: "command_failed", Message: err.Error(), Category: "cli", Command: cmd.CommandPath(),
		Remediation: "Review the message and run '" + cmd.CommandPath() + " --help' if the required input is unclear.",
	}
	var invokeError *sdkInvokeError
	if errors.As(err, &invokeError) {
		result.Code, result.Message, result.Category = invokeError.code, invokeError.message, invokeError.category
		result.Details = invokeError.details
		return result
	}
	var applyError *sdkApplyStageError
	if errors.As(err, &applyError) {
		result.Details = applyError.jsonDetails()
	}
	var unknownApply *importApplyOutcomeUnknownError
	if errors.As(err, &unknownApply) {
		return classifyUnknownImportApply(result, unknownApply)
	}
	var strictError *cliapi.SpecImportStrictError
	if errors.As(err, &strictError) {
		result.Code, result.Message, result.Category = strictError.Code, strictError.Message, "validation"
		result.HTTPStatus = strictError.HTTPStatus
		result.Remediation = "Resolve the reported diagnostics and create the plan again."
		result.Details = map[string]any{"diagnostics": strictError.Diagnostics}
		return result
	}
	var discoveryError *discoveryFailureError
	// Discovery terminal failures carry only locally bounded Registry diagnostics,
	// so both human and JSON callers receive the same actionable explanation.
	if errors.As(err, &discoveryError) {
		result.Code, result.Message, result.Category = "discovery_session_failed", discoveryError.Error(), "dependency"
		result.Remediation = "Review the terminal diagnostic, then resume or restart discovery after correcting the reported condition."
		result.Details = discoveryError.jsonDetails()
		return result
	}
	var apiError *cliapi.APIError
	var workspaceApply *workspaceServiceApplyOutcomeError
	// Composite activation has sibling outcome state that the wrapped one-target
	// API error cannot represent, so classify it before the generic API branch.
	if errors.As(err, &workspaceApply) {
		result.Code, result.Category = safeWorkspaceOutcomeToken(workspaceApply.code, workspaceServiceApplyErrorCode), "partial"
		result.Message = "One or more requested workspace services were not activated."
		result.Remediation = "Run the exact recovery command after reviewing the failed target's commit possibility."
		result.Phase, result.RequestID = safeWorkspaceOutcomeToken(workspaceApply.phase, workspaceServiceApplyPhase), safeWorkspaceRequestID(workspaceApply.requestID)
		result.CommitState, result.Recovery = safeWorkspaceOutcomeToken(workspaceApply.failedCommitState, "unknown"), safeWorkspaceRecoveryCommand(workspaceApply.recovery)
		result.Details = map[string]any{
			"committed": safeWorkspaceServiceRefs(workspaceApply.committed), "failed": safeWorkspaceServiceRef(workspaceApply.failed),
			"failed_commit_possible": workspaceApply.failedCommitPossible,
			"unattempted":            safeWorkspaceServiceRefs(workspaceApply.unattempted),
		}
		return result
	}
	if errors.As(err, &apiError) {
		result.Code, result.Message = apiError.Code, apiError.Message
		result.Category, result.Retryable = apiError.Category, apiError.Retryable
		result.Remediation, result.TraceID, result.HTTPStatus = apiError.Remediation, apiError.TraceID, apiError.HTTPStatus
		// Import operation recovery remains top-level so agents do not parse a
		// generic details bag to decide whether a commit is possible.
		result.Phase, result.OperationID, result.RequestID = apiError.Phase, apiError.OperationID, apiError.RequestID
		result.CommitState, result.Recovery = apiError.CommitState, apiError.Recovery
		result.Details = mergeJSONDetails(result.Details, apiErrorJSONDetails(apiError))
		return result
	}
	return classifyGenericCommandError(result, err)
}

// classifyGenericCommandError handles local usage and context outcomes after
// every richer typed command and API contract has declined the error.
func classifyGenericCommandError(result jsonErrorResult, err error) jsonErrorResult {
	// Cobra argument errors are caller-correctable and should not look like CLI faults.
	if isCommandUsageError(err.Error()) {
		result.Code, result.Category = "invalid_arguments", "validation"
		return result
	}
	// Deadlines are retryable dependency outcomes with an explicit timeout recovery.
	if errors.Is(err, context.DeadlineExceeded) {
		result.Code, result.Category, result.Retryable = "request_timeout", "dependency", true
		result.Remediation = "Increase --timeout or retry the command."
	}
	// Explicit cancellation remains distinct from timeouts for automation policy.
	if errors.Is(err, context.Canceled) {
		result.Code, result.Category = "request_cancelled", "cancelled"
	}
	return result
}

// classifyUnknownImportApply replaces generic retry advice with the durable
// status recovery contract while distinguishing deadlines from other proof loss.
func classifyUnknownImportApply(result jsonErrorResult, unknownApply *importApplyOutcomeUnknownError) jsonErrorResult {
	result.Code, result.Category = "import_apply_outcome_unknown", "indeterminate"
	result.Message = "The import apply response did not prove whether the reviewed plan committed."
	// Timeout wording and duration are meaningful only when the transport
	// actually crossed its deadline; resets and malformed proofs must not lie.
	if unknownApply.timedOut {
		result.Message = "The import apply response timed out after the server may have committed the reviewed plan."
		result.Details = map[string]any{"timeout_ms": unknownApply.timeout.Milliseconds()}
	}
	result.Remediation = unknownApply.remediation()
	result.Phase, result.OperationID = "registry_apply", safeImportOperationID(unknownApply.operationID)
	result.CommitState = "unknown"
	result.Recovery = "fused-cli import status " + result.OperationID
	return result
}

// mergeJSONDetails combines CLI stage context with authoritative Engine details.
func mergeJSONDetails(base, additional map[string]any) map[string]any {
	if len(base) == 0 && len(additional) == 0 {
		return nil
	}
	merged := make(map[string]any, len(base)+len(additional))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range additional {
		merged[key] = value
	}
	return merged
}

// apiErrorJSONDetails preserves every reviewed diagnostic field without
// exposing arbitrary response JSON that the API client deliberately rejected.
func apiErrorJSONDetails(apiError *cliapi.APIError) map[string]any {
	details := map[string]any{}
	if apiError.Details.Bucket != nil {
		details["bucket"] = apiError.Details.Bucket
	}
	if len(apiError.Details.MissingCredentials) > 0 {
		details["missing_credentials"] = apiError.Details.MissingCredentials
	}
	if len(apiError.Details.RequiredPermissions) > 0 {
		details["required_permissions"] = apiError.Details.RequiredPermissions
	}
	if apiError.Details.ServerDetail != "" {
		details["server_detail"] = apiError.Details.ServerDetail
	}
	if apiError.Details.ApplyLeaseExpiresAt != "" {
		details["apply_lease_expires_at"] = apiError.Details.ApplyLeaseExpiresAt
	}
	if apiError.Details.RetryAfterSeconds > 0 {
		details["retry_after_seconds"] = apiError.Details.RetryAfterSeconds
	}
	if apiError.Details.Stage != "" {
		details["stage"] = apiError.Details.Stage
	}
	if apiError.Details.DependencyHTTPStatus > 0 {
		details["dependency_http_status"] = apiError.Details.DependencyHTTPStatus
	}
	putAPIErrorJSONDetail(details, "service_id", apiError.Details.ServiceID)
	putAPIErrorJSONDetail(details, "service_version_id", apiError.Details.ServiceVersionID)
	putAPIErrorJSONDetail(details, "workspace_outcome", apiError.Details.WorkspaceOutcome)
	if len(details) == 0 {
		return nil
	}
	return details
}

// putAPIErrorJSONDetail preserves a non-empty reviewed Engine field without repeating projection guards.
func putAPIErrorJSONDetail(details map[string]any, key, value string) {
	// Empty optional fields stay absent so agents can distinguish missing proof
	// from a present but false-looking value.
	if value != "" {
		details[key] = value
	}
}

func isCommandUsageError(message string) bool {
	for _, prefix := range []string{"accepts ", "requires at least ", "requires at most ", "unknown flag:", "unknown command ", "required flag(s)"} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}
