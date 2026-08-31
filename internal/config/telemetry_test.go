package config

import "testing"

func TestLoadTelemetryDefaults(t *testing.T) {
	got := LoadTelemetry(func(string) (string, bool) { return "", false }, "orbitd")
	if got.ServiceName != "orbitd" || !got.Enabled || got.OTLPEndpoint != defaultOTLPEndpoint {
		t.Fatalf("LoadTelemetry() = %+v", got)
	}
}

func TestLoadTelemetryCanDisable(t *testing.T) {
	got := LoadTelemetry(func(key string) (string, bool) {
		switch key {
		case "ORBIT_OTEL_ENABLED":
			return "false", true
		default:
			return "", false
		}
	}, "orbit-gateway")
	if got.Enabled {
		t.Fatalf("LoadTelemetry() = %+v, want disabled", got)
	}
}
