package config

import (
	"testing"
	"time"
)

func TestLoadOrbitd(t *testing.T) {
	values := map[string]string{
		"ORBIT_DATABASE_URL":             "postgres://orbit:secret@localhost/orbit",
		"ORBIT_LISTEN_ADDRESS":           ":6000",
		"ORBIT_SHUTDOWN_TIMEOUT":         "15s",
		"ORBIT_DB_MAX_CONNECTIONS":       "24",
		"ORBIT_GATEWAY_OUTBOUND_BUFFER":  "64",
		"ORBIT_SCHEDULER_LEASE_BATCH":    "12",
		"ORBIT_SCHEDULER_SWEEP_BATCH":    "20",
		"ORBIT_SCHEDULER_LEASE_DURATION": "20s",
		"ORBIT_SCHEDULER_POLL_INTERVAL":  "100ms",
	}
	got, err := LoadOrbitd(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("LoadOrbitd() error = %v", err)
	}
	if got.ListenAddress != ":6000" || got.ShutdownTimeout != 15*time.Second || got.DBMaxConnections != 24 ||
		got.GatewayOutboundBuffer != 64 || got.SchedulerLeaseBatch != 12 || got.SchedulerSweepBatch != 20 ||
		got.SchedulerLeaseDuration != 20*time.Second || got.SchedulerPollInterval != 100*time.Millisecond {
		t.Fatalf("LoadOrbitd() = %+v", got)
	}
}

func TestLoadOrbitdDefaults(t *testing.T) {
	got, err := LoadOrbitd(func(key string) (string, bool) {
		if key == "ORBIT_DATABASE_URL" {
			return "postgres://localhost/orbit", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("LoadOrbitd() error = %v", err)
	}
	if got.ListenAddress != defaultListenAddress || got.ShutdownTimeout != defaultShutdown || got.DBMaxConnections != defaultMaxConnections {
		t.Fatalf("LoadOrbitd() = %+v", got)
	}
}

func TestLoadOrbitdRejectsInvalidConfiguration(t *testing.T) {
	tests := []map[string]string{
		{},
		{"ORBIT_DATABASE_URL": "postgres://localhost/orbit", "ORBIT_LISTEN_ADDRESS": " "},
		{"ORBIT_DATABASE_URL": "postgres://localhost/orbit", "ORBIT_SHUTDOWN_TIMEOUT": "0s"},
		{"ORBIT_DATABASE_URL": "postgres://localhost/orbit", "ORBIT_DB_MAX_CONNECTIONS": "101"},
		{"ORBIT_DATABASE_URL": "postgres://localhost/orbit", "ORBIT_GATEWAY_OUTBOUND_BUFFER": "0"},
		{"ORBIT_DATABASE_URL": "postgres://localhost/orbit", "ORBIT_SCHEDULER_LEASE_DURATION": "500ms"},
	}
	for _, values := range tests {
		_, err := LoadOrbitd(func(key string) (string, bool) {
			value, ok := values[key]
			return value, ok
		})
		if err == nil {
			t.Fatalf("LoadOrbitd(%v) unexpectedly succeeded", values)
		}
	}
}
