package observability

import (
	"context"
	"fmt"
	"strings"

	"kubesage/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "kubesage"

// InitTracing configures OpenTelemetry tracing and returns a shutdown function.
func InitTracing(ctx context.Context, cfg config.OTelConfig) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	serviceName := strings.TrimSpace(cfg.ServiceName)
	if serviceName == "" {
		serviceName = "kubesage"
	}
	exporter, err := newTraceExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			"",
			attribute.String("service.name", serviceName),
		)),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider.Shutdown, nil
}

// Tracer returns the project tracer used by service and HTTP instrumentation.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// newTraceExporter creates either an OTLP/HTTP or stdout trace exporter.
func newTraceExporter(ctx context.Context, cfg config.OTelConfig) (sdktrace.SpanExporter, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Exporter)) {
	case "", "stdout":
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	case "otlp", "otlphttp", "otlp_http":
		opts := []otlptracehttp.Option{}
		if strings.TrimSpace(cfg.Endpoint) != "" {
			opts = append(opts, otlptracehttp.WithEndpoint(strings.TrimSpace(cfg.Endpoint)))
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unsupported otel exporter %q", cfg.Exporter)
	}
}
