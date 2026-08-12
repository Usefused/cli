package cmd

import (
	"context"
	"errors"
	"testing"

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
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	_, span := provider.Tracer("test").Start(context.Background(), "read")
	recordTelemetryError(span, &cliapi.APIError{
		Code: "registry_request_failed", Message: "do not attach this remote message", Retryable: true,
	})
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
