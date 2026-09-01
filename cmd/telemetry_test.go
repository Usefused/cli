package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRecordAppliedChangeEmitsSecretSafeAuditEvent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := provider.Tracer("test").Start(context.Background(), "apply")

	recordAppliedChange(ctx, "fused-cli sdk apply", "sdk")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || len(spans[0].Events) != 1 || spans[0].Events[0].Name != "cli_change_applied" {
		t.Fatalf("expected one apply audit event, got %#v", spans)
	}
	attributes := map[string]interface{}{}
	for _, value := range spans[0].Events[0].Attributes {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	if attributes["user_action"] != "fused-cli sdk apply" || attributes["resource_kind"] != "sdk" {
		t.Fatalf("unexpected apply audit attributes: %#v", attributes)
	}
}

func TestWithTelemetryDoesNotRecordRemoteErrorMessage(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	remoteErr := &cliapi.APIError{Code: "registry_request_failed", Message: "fsk_never_record", Retryable: true}
	run := WithTelemetry("cli.test", func(*cobra.Command, []string) error { return remoteErr })
	command := &cobra.Command{Use: "test"}
	command.SetContext(context.Background())
	if err := run(command, nil); !errors.Is(err, remoteErr) {
		t.Fatalf("wrapped error = %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 || len(spans[0].Events) != 0 {
		t.Fatalf("remote error was recorded as an event: %#v", spans)
	}
	for _, value := range spans[0].Attributes {
		if value.Value.AsString() == "fsk_never_record" {
			t.Fatalf("remote error message leaked to OTEL: %#v", spans[0].Attributes)
		}
	}
}

func TestRecordTelemetryErrorUsesSafeStructuredFields(t *testing.T) {
	// Owner-visible diagnostics stay local to CLI output; OTEL retains only
	// bounded fields suitable for cross-tenant aggregation and auditing.
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	_, span := provider.Tracer("test").Start(context.Background(), "read")
	apiError := &cliapi.APIError{
		Code: "registry_request_failed", Message: "do not attach this remote message", Retryable: true,
	}
	apiError.Details.ServerDetail = `unknown field "items_path" from user input`
	recordTelemetryError(span, apiError)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Description != "registry_request_failed" {
		t.Fatalf("unexpected error span: %#v", spans)
	}
	attributes := map[string]interface{}{}
	for _, value := range spans[0].Attributes {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	if attributes["error.code"] != "registry_request_failed" || attributes["error.retryable"] != true {
		t.Fatalf("error attributes = %#v", attributes)
	}
	if _, exposed := attributes["error.message"]; exposed {
		t.Fatalf("remote error message was attached: %#v", attributes)
	}
	if _, exposed := attributes["error.server_detail"]; exposed {
		t.Fatalf("server detail was attached: %#v", attributes)
	}
}

// TestRecordTelemetryErrorUsesVersionRequiredCode proves a versionless import
// remains diagnosable without exporting its source, version, or remediation.
func TestRecordTelemetryErrorUsesVersionRequiredCode(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	_, span := provider.Tracer("test").Start(context.Background(), "import-plan")
	apiError := &cliapi.APIError{
		Code: "import_version_required", Message: "private-source-name", Category: "validation",
		Remediation: "use-private-version", Retryable: false,
	}
	recordTelemetryError(span, apiError)
	span.End()
	spans := exporter.GetSpans()
	// The typed contract may export only the bounded code and retryability.
	if len(spans) != 1 || spans[0].Status.Description != "import_version_required" || len(spans[0].Events) != 0 {
		t.Fatalf("typed import span = %#v", spans)
	}
	attributes := map[string]interface{}{}
	// Attribute collection makes every emitted value available to the denylist.
	for _, value := range spans[0].Attributes {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	if attributes["error.code"] != "import_version_required" || attributes["error.retryable"] != false {
		t.Fatalf("typed import attributes = %#v", attributes)
	}
	// Remote message and remediation must never become telemetry dimensions.
	encoded := fmt.Sprint(attributes)
	if strings.Contains(encoded, "private-source-name") || strings.Contains(encoded, "use-private-version") {
		t.Fatalf("import telemetry leaked remote text: %#v", attributes)
	}
}

// TestRecordTelemetryErrorMarksUnknownImportApplyNonRetryable keeps agent
// automation from translating an ambiguous one-shot mutation into a retry.
func TestRecordTelemetryErrorMarksUnknownImportApplyNonRetryable(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	_, span := provider.Tracer("test").Start(context.Background(), "import-apply")
	recordTelemetryError(span, &importApplyOutcomeUnknownError{
		cause: context.DeadlineExceeded, timeout: 20 * time.Minute, operationID: "11111111-1111-4111-8111-111111111111",
	})
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Description != "import_apply_outcome_unknown" {
		t.Fatalf("unknown apply span = %#v", spans)
	}
	attributes := map[string]interface{}{}
	for _, value := range spans[0].Attributes {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	if attributes["error.code"] != "import_apply_outcome_unknown" || attributes["error.retryable"] != false {
		t.Fatalf("unknown apply attributes = %#v", attributes)
	}
}

// TestRecordTelemetryErrorMarksUnknownSDKApplyNonRetryable proves the top-level ambiguity contract overrides its retryable transport cause.
func TestRecordTelemetryErrorMarksUnknownSDKApplyNonRetryable(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	_, span := provider.Tracer("test").Start(context.Background(), "sdk-apply")
	retryableCause := &cliapi.APIError{Code: "request_timed_out", Message: "Engine request timed out", Retryable: true}
	recordTelemetryError(span, &sdkApplyOutcomeUnknownError{
		cause: retryableCause, planID: "plan-1", configKey: "sdk:security:1.2.0", sdkName: "security", version: "1.2.0",
	})
	span.End()

	spans := exporter.GetSpans()
	// Span status must describe the mutation ambiguity rather than the wrapped timeout.
	if len(spans) != 1 || spans[0].Status.Description != "sdk_apply_outcome_unknown" {
		t.Fatalf("unknown SDK apply span = %#v", spans)
	}
	attributes := map[string]interface{}{}
	// Attribute collection exposes the complete emitted dimension set to the secret-safety assertion.
	for _, value := range spans[0].Attributes {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	// Only the stable non-retryable classifier is exported; local identity and remote prose stay out of telemetry.
	if attributes["error.code"] != "sdk_apply_outcome_unknown" || attributes["error.retryable"] != false || strings.Contains(fmt.Sprint(attributes), "security") {
		t.Fatalf("unknown SDK apply attributes = %#v", attributes)
	}
}

// TestRecordTelemetryErrorMarksCompositeWorkspaceApplyNonRetryable verifies the
// stable partial code is emitted without service refs or remote failure text.
func TestRecordTelemetryErrorMarksCompositeWorkspaceApplyNonRetryable(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	_, span := provider.Tracer("test").Start(context.Background(), "workspace-add")
	recordTelemetryError(span, &workspaceServiceApplyOutcomeError{
		code: workspaceServiceApplyErrorCode, cause: errors.New("private remote detail"),
	})
	span.End()
	spans := exporter.GetSpans()
	// The partial classifier, not the wrapped remote detail, owns span status.
	if len(spans) != 1 || spans[0].Status.Description != workspaceServiceApplyErrorCode {
		t.Fatalf("workspace apply span = %#v", spans)
	}
	attributes := map[string]interface{}{}
	// Only the bounded classifier fields belong in cross-request telemetry.
	for _, value := range spans[0].Attributes {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	// Retryability stays false because only the exact recovery command may replay
	// the failed/unattempted suffix safely.
	if attributes["error.code"] != workspaceServiceApplyErrorCode || attributes["error.retryable"] != false || strings.Contains(fmt.Sprint(attributes), "private remote detail") {
		t.Fatalf("workspace apply telemetry attributes = %#v", attributes)
	}
}

func TestRecordAppliedChangeIfSkipsNoOpMutation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := provider.Tracer("test").Start(context.Background(), "mutation")

	recordAppliedChangeIf(ctx, "team.update", "team", false)
	span.End()

	if got := countAppliedChangeEvents(exporter.GetSpans()); got != 0 {
		t.Fatalf("applied change event count = %d, want 0", got)
	}
}

// TestRecordGeneratedBindingCountEmitsOnlySafeCardinality proves smart init
// records its bounded decision without attaching generated binding metadata.
func TestRecordGeneratedBindingCountEmitsOnlySafeCardinality(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := provider.Tracer("test").Start(context.Background(), "scaffold")

	recordGeneratedBindingCount(ctx, 2)
	span.End()

	spans := exporter.GetSpans()
	// One integer is the complete telemetry contract for generated bindings.
	if len(spans) != 1 || len(spans[0].Attributes) != 1 || string(spans[0].Attributes[0].Key) != "scaffold.generated_binding_count" || spans[0].Attributes[0].Value.AsInt64() != 2 {
		t.Fatalf("scaffold attributes = %#v", spans)
	}
}

func countAppliedChangeEvents(spans tracetest.SpanStubs) int {
	count := 0
	for _, span := range spans {
		for _, event := range span.Events {
			if event.Name == "cli_change_applied" {
				count++
			}
		}
	}
	return count
}
