package cmd

import (
	"context"
	"testing"

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
