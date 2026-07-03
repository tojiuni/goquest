// Package observability wires OpenTelemetry tracing for goquest.
package observability

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitTracer configures a global OTLP/gRPC tracer provider from the standard
// OTEL_* environment variables (OTEL_EXPORTER_OTLP_ENDPOINT, _PROTOCOL,
// OTEL_SERVICE_NAME, …). When OTEL_EXPORTER_OTLP_ENDPOINT is unset it is a
// no-op — local/dev/test runs emit nothing and pay no cost. The returned
// function flushes and shuts the provider down; call it on graceful shutdown.
func InitTracer(ctx context.Context) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return noop, nil
	}

	// otlptracegrpc reads endpoint/insecure/headers from the OTEL_EXPORTER_OTLP_*
	// env vars. An "http://host:port" endpoint selects an insecure connection.
	exp, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	svcName := os.Getenv("OTEL_SERVICE_NAME")
	if svcName == "" {
		svcName = "goquest-service"
	}
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName(svcName)),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp.Shutdown, nil
}
