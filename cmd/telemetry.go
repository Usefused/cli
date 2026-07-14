package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
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

		// Overwrite the context so child calls inherit the span
		cmd.SetContext(ctx)

		err := runE(cmd, args)
		if err != nil {
			span.RecordError(err)
		}
		return err
	}
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
