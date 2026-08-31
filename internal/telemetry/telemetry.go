package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	ServiceName  string
	OTLPEndpoint string
	Enabled      bool
}

type Provider struct {
	tracerProvider *sdktrace.TracerProvider
}

func Init(ctx context.Context, config Config) (*Provider, error) {
	if !config.Enabled || strings.TrimSpace(config.OTLPEndpoint) == "" {
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		return &Provider{}, nil
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(normalizeOTLPEndpoint(config.OTLPEndpoint)),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(config.ServiceName),
		)),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return &Provider{tracerProvider: provider}, nil
}

func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.tracerProvider == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return p.tracerProvider.Shutdown(shutdownCtx)
}

func Tracer() trace.Tracer {
	return otel.Tracer("github.com/JayYarlagadda/orbit")
}

func normalizeOTLPEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return "http://" + endpoint
}
