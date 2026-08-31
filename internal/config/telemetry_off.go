package config

// TelemetryDisabledEnv returns environment entries that disable metrics and
// tracing exporters. Tests that spawn multiple processes should append these
// values to avoid fixed-port listener conflicts.
func TelemetryDisabledEnv() []string {
	return []string{
		"ORBIT_METRICS_ADDRESS=",
		"ORBIT_OTEL_ENABLED=false",
	}
}
