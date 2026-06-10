package trace

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestStartSpan_RecordsSpanWithTraceID(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx := WithTraceID(context.Background(), "abc123")
	ctx, end := StartSpan(ctx, "chat.run", map[string]string{"model": "deepseek"})
	// a child span should nest under the parent
	_, endChild := StartSpan(ctx, "tool.invoke", map[string]string{"tool": "file_write"})
	endChild()
	end()

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("recorded %d spans, want 2", len(spans))
	}
	// child ended first
	if spans[0].Name() != "tool.invoke" || spans[1].Name() != "chat.run" {
		t.Fatalf("span names: %s, %s", spans[0].Name(), spans[1].Name())
	}
	// child parents to root (same trace)
	if spans[0].SpanContext().TraceID() != spans[1].SpanContext().TraceID() {
		t.Fatal("child span not in same trace as parent")
	}
	// orka trace id stamped
	var found bool
	for _, a := range spans[1].Attributes() {
		if a.Key == "orka.trace_id" && a.Value.AsString() == "abc123" {
			found = true
		}
	}
	if !found {
		t.Fatalf("orka.trace_id attribute missing: %v", spans[1].Attributes())
	}
}

func TestInit_NoopWhenUnconfigured(t *testing.T) {
	shutdown, err := Init(context.Background(), "orka-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("noop shutdown: %v", err)
	}
}
