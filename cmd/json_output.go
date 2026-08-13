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

func classifyCommandError(cmd *cobra.Command, err error) jsonErrorResult {
	// Human and JSON output share the same typed error; only the presentation
	// changes, so debugging detail cannot drift between interactive and agent use.
	result := jsonErrorResult{
		Code: "command_failed", Message: err.Error(), Category: "cli", Command: cmd.CommandPath(),
		Remediation: "Review the message and run '" + cmd.CommandPath() + " --help' if the required input is unclear.",
	}
	var strictError *cliapi.SpecImportStrictError
	if errors.As(err, &strictError) {
		result.Code, result.Message, result.Category = strictError.Code, strictError.Message, "validation"
		result.HTTPStatus = strictError.HTTPStatus
		result.Remediation = "Resolve the reported diagnostics and create the plan again."
		result.Details = map[string]any{"diagnostics": strictError.Diagnostics}
		return result
	}
	var apiError *cliapi.APIError
	if errors.As(err, &apiError) {
		result.Code, result.Message = apiError.Code, apiError.Message
		result.Category, result.Retryable = apiError.Category, apiError.Retryable
		result.Remediation, result.TraceID, result.HTTPStatus = apiError.Remediation, apiError.TraceID, apiError.HTTPStatus
		result.Details = apiErrorJSONDetails(apiError)
		return result
	}
	if isCommandUsageError(err.Error()) {
		result.Code, result.Category = "invalid_arguments", "validation"
		return result
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.Code, result.Category, result.Retryable = "request_timeout", "dependency", true
		result.Remediation = "Increase --timeout or retry the command."
	}
	if errors.Is(err, context.Canceled) {
		result.Code, result.Category = "request_cancelled", "cancelled"
	}
	return result
}

// apiErrorJSONDetails preserves every reviewed diagnostic field without
// exposing arbitrary response JSON that the API client deliberately rejected.
func apiErrorJSONDetails(apiError *cliapi.APIError) map[string]any {
	details := map[string]any{}
	if len(apiError.Details.Missing) > 0 {
		details["missing"] = apiError.Details.Missing
	}
	if apiError.Details.ServerDetail != "" {
		details["server_detail"] = apiError.Details.ServerDetail
	}
	if len(details) == 0 {
		return nil
	}
	return details
}

func isCommandUsageError(message string) bool {
	for _, prefix := range []string{"accepts ", "requires at least ", "requires at most ", "unknown flag:", "unknown command ", "required flag(s)"} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}
