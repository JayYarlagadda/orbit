package config

import (
	"testing"
	"time"
)

func lookupFrom(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestLoadClient(t *testing.T) {
	values := map[string]string{
		"ORBIT_DEVICE_ID":                      "edge-1",
		"ORBIT_CLIENT_INSTANCE_ID":             "instance-1",
		"ORBIT_CLIENT_GATEWAY_ADDRESS":         "gateway:7001",
		"ORBIT_CLIENT_STATE_PATH":              "state/edge-1.json",
		"ORBIT_CLIENT_DEDUP_RETENTION":         "64",
		"ORBIT_CLIENT_MAX_RECONNECT_ATTEMPTS":  "5",
		"ORBIT_CLIENT_RECONNECT_INITIAL_DELAY": "50ms",
		"ORBIT_CLIENT_RECONNECT_MAX_DELAY":     "5s",
	}
	got, err := LoadClient(lookupFrom(values))
	if err != nil {
		t.Fatalf("LoadClient() error = %v", err)
	}
	if got.DeviceID != "edge-1" || got.ClientInstanceID != "instance-1" ||
		got.GatewayAddress != "gateway:7001" || got.StatePath != "state/edge-1.json" ||
		got.DedupRetention != 64 || got.MaxReconnectAttempts != 5 ||
		got.ReconnectInitialDelay != 50*time.Millisecond || got.ReconnectMaxDelay != 5*time.Second {
		t.Fatalf("LoadClient() = %+v", got)
	}
}

func TestLoadClientAppliesDefaults(t *testing.T) {
	got, err := LoadClient(lookupFrom(map[string]string{"ORBIT_DEVICE_ID": "edge-1"}))
	if err != nil {
		t.Fatalf("LoadClient() error = %v", err)
	}
	if got.GatewayAddress != defaultClientGatewayAddress || got.StatePath != defaultClientStatePath ||
		got.DedupRetention != defaultClientRetention || got.ReconnectInitialDelay != defaultReconnectInitial ||
		got.ReconnectMaxDelay != defaultReconnectMax {
		t.Fatalf("LoadClient() = %+v", got)
	}
	// Zero means the client keeps retrying for as long as the process runs.
	if got.MaxReconnectAttempts != 0 {
		t.Fatalf("MaxReconnectAttempts = %d, want 0", got.MaxReconnectAttempts)
	}
	if got.ClientInstanceID != "" {
		t.Fatalf("ClientInstanceID = %q, want an empty value so the process generates one", got.ClientInstanceID)
	}
}

func TestLoadClientRejectsInvalidConfiguration(t *testing.T) {
	for _, values := range []map[string]string{
		{},
		{"ORBIT_DEVICE_ID": "bad device"},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_INSTANCE_ID": "bad instance"},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_GATEWAY_ADDRESS": "  "},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_STATE_PATH": ""},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_DEDUP_RETENTION": "0"},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_DEDUP_RETENTION": "100001"},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_MAX_RECONNECT_ATTEMPTS": "-1"},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_MAX_RECONNECT_ATTEMPTS": "1001"},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_RECONNECT_INITIAL_DELAY": "1ms"},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_RECONNECT_MAX_DELAY": "5m"},
		{"ORBIT_DEVICE_ID": "edge-1", "ORBIT_CLIENT_RECONNECT_MAX_DELAY": "not-a-duration"},
		// A cap below the initial delay would make the backoff shrink.
		{
			"ORBIT_DEVICE_ID":                      "edge-1",
			"ORBIT_CLIENT_RECONNECT_INITIAL_DELAY": "5s",
			"ORBIT_CLIENT_RECONNECT_MAX_DELAY":     "500ms",
		},
	} {
		if _, err := LoadClient(lookupFrom(values)); err == nil {
			t.Fatalf("LoadClient(%v) unexpectedly succeeded", values)
		}
	}
}
