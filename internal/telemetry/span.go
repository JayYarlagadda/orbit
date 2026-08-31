package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	AttrCommandID     = "orbit.command.id"
	AttrCorrelationID = "orbit.correlation.id"
	AttrGatewayID     = "orbit.gateway.id"
	AttrDeviceID      = "orbit.device.id"
	AttrReconnect     = "orbit.reconnect.attempt"
)

func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

func End(span trace.Span, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func CommandID(id string) attribute.KeyValue {
	return attribute.String(AttrCommandID, id)
}

func CorrelationID(id string) attribute.KeyValue {
	return attribute.String(AttrCorrelationID, id)
}

func GatewayID(id string) attribute.KeyValue {
	return attribute.String(AttrGatewayID, id)
}

func DeviceID(id string) attribute.KeyValue {
	return attribute.String(AttrDeviceID, id)
}

func ReconnectAttempt(attempt int) attribute.KeyValue {
	return attribute.Int(AttrReconnect, attempt)
}
