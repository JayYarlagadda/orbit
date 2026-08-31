package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestInitDisabledUsesNoopTracer(t *testing.T) {
	provider, err := Init(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ctx, span := Start(context.Background(), "orbit.test.noop", attribute.String("orbit.test", "1"))
	span.End()
	if err := provider.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
