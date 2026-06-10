package trace

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const tracerName = "orka"

// ShutdownFunc flushes and stops the tracer provider.
type ShutdownFunc func(context.Context) error

// Init wires an OpenTelemetry tracer provider:
//   - OTEL_EXPORTER_OTLP_ENDPOINT set -> OTLP/HTTP exporter
//   - OTEL_TRACES_STDOUT=1           -> pretty stdout exporter (local debugging)
//   - otherwise                      -> no-op (zero overhead)
//
// Spans are created via StartSpan regardless; without a provider they are no-ops.
func Init(ctx context.Context, service string) (ShutdownFunc, error) {
	var exp sdktrace.SpanExporter
	var err error
	switch {
	case os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "":
		exp, err = otlptracehttp.New(ctx)
	case os.Getenv("OTEL_TRACES_STDOUT") == "1":
		exp, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	default:
		return func(context.Context) error { return nil }, nil
	}
	if err != nil {
		return nil, err
	}
	res, _ := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", service),
	))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// StartSpan starts a span as a child of ctx and returns the new context plus an
// end function. It stamps the orka TraceID (if present on ctx) as an attribute
// so OTel traces correlate with the SSE event stream's trace_id. Callers do not
// need to import OpenTelemetry.
func StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	tr := otel.GetTracerProvider().Tracer(tracerName)
	ctx, span := tr.Start(ctx, name)
	if tid := TraceIDFrom(ctx); tid != "" {
		span.SetAttributes(attribute.String("orka.trace_id", tid))
	}
	for k, v := range attrs {
		span.SetAttributes(attribute.String(k, v))
	}
	return ctx, func() { span.End() }
}
