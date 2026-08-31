package config

import "strings"

const (
	defaultOTLPEndpoint = "http://127.0.0.1:4318"
)

type Telemetry struct {
	ServiceName  string
	OTLPEndpoint string
	Enabled      bool
}

func LoadTelemetry(lookup LookupEnv, serviceName string) Telemetry {
	result := Telemetry{
		ServiceName:  serviceName,
		OTLPEndpoint: defaultOTLPEndpoint,
		Enabled:      true,
	}
	if value, ok := lookup("ORBIT_OTEL_ENABLED"); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "0", "false", "off", "no":
			result.Enabled = false
		}
	}
	if value, ok := lookup("ORBIT_OTEL_EXPORTER_OTLP_ENDPOINT"); ok {
		result.OTLPEndpoint = strings.TrimSpace(value)
		if result.OTLPEndpoint == "" {
			result.Enabled = false
		}
	}
	return result
}
