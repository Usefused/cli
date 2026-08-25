package cmd

import (
	"context"
	"errors"
	"os"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

// InitTelemetry sets up OpenTelemetry for the CLI.
// We initialize a basic provider. A local exporter can be attached later via environment variables.
func InitTelemetry() func(context.Context) error {
	res, _ := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName("fused-cli"),
			semconv.ServiceVersion(Version),
		),
	)

	// Using a simple provider; exporters can be configured via standard OTEL environment variables.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown
}

// WithTelemetry wraps a Cobra command execution with an OpenTelemetry span.
// It should be used for user/agent triggered mutative executions (e.g. apply, add-service)
// for debugging/auditing purposes.
func WithTelemetry(spanName string, runE func(cmd *cobra.Command, args []string) error) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		ctx, span := otel.Tracer("fused-cli").Start(ctx, spanName)
		defer span.End()

		// Overwrite both contexts so API clients created by existing command
		// helpers still inherit cancellation and tracing without a second,
		// divergent client-construction path.
		cmd.SetContext(ctx)
		previousExecutionContext := executionContext
		executionContext = ctx
		defer func() { executionContext = previousExecutionContext }()

		err := runE(cmd, args)
		if err != nil {
			recordTelemetryError(span, err)
		}
		return err
	}
}

// recordTelemetryError projects typed failures into bounded searchable span state.
func recordTelemetryError(span trace.Span, err error) {
	code := "command_failed"
	retryable := false
	var unknownApply *importApplyOutcomeUnknownError
	if errors.As(err, &unknownApply) {
		// Why: a lost response after a one-shot mutation is not safe for agents
		// to retry, even though ordinary request timeouts remain retryable.
		code = "import_apply_outcome_unknown"
	}
	var strictError *cliapi.SpecImportStrictError
	if errors.As(err, &strictError) {
		code = strictError.Code
	}
	var apiError *cliapi.APIError
	if errors.As(err, &apiError) {
		code, retryable = apiError.Code, apiError.Retryable
	}
	var workspaceApply *workspaceServiceApplyOutcomeError
	// Composite activation owns the top-level failure classification because an
	// underlying per-service API code omits already-committed sibling mutations.
	if errors.As(err, &workspaceApply) {
		code, retryable = "workspace_service_apply_partial", false
	}
	// Stable fields make failures searchable without attaching user input or
	// remote messages that could contain credentials.
	span.SetAttributes(attribute.String("error.code", code), attribute.Bool("error.retryable", retryable))
	span.SetStatus(codes.Error, code)
}

func recordAppliedChange(ctx context.Context, action, resourceKind string) {
	if ctx == nil {
		return
	}
	// Why: recording one event per completed mutation preserves evidence of
	// partial applies while avoiding resource names, arguments, and secrets.
	trace.SpanFromContext(ctx).AddEvent("cli_change_applied", trace.WithAttributes(
		attribute.String("user_action", action),
		attribute.String("resource_kind", resourceKind),
	))
}

func recordAppliedChangeIf(ctx context.Context, action, resourceKind string, changed bool) {
	// A successful idempotent request is useful operationally, but it is not
	// evidence that state changed and must not be represented as such in audit data.
	if !changed {
		return
	}
	recordAppliedChange(ctx, action, resourceKind)
}

// recordGeneratedBindingCount keeps smart-init decisions diagnosable while
// excluding service names, variable names, references, and provider values.
func recordGeneratedBindingCount(ctx context.Context, count int) {
	// A missing context cannot own a command span, so there is nowhere safe to attach metadata.
	if ctx == nil {
		return
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("scaffold.generated_binding_count", count))
}

func withApplyAudit(cmd *cobra.Command, opts applyOptions) applyOptions {
	opts.auditCtx = cmd.Context()
	opts.auditAction = cmd.CommandPath()
	return opts
}

// ExecuteWithTelemetry replaces the standard cmd.Execute() to ensure OTEL shuts down cleanly.
func ExecuteWithTelemetry(cmd *cobra.Command) {
	shutdown := InitTelemetry()
	defer func() {
		_ = shutdown(context.Background())
	}()

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
