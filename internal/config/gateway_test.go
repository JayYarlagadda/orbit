package config

import (
	"testing"
	"time"
)

func TestLoadGateway(t *testing.T) {
	values := map[string]string{
		"ORBIT_GATEWAY_ID":               "gateway-1",
		"ORBIT_CONTROL_ADDRESS":          "control:6000",
		"ORBIT_GATEWAY_LISTEN_ADDRESS":   ":6001",
		"ORBIT_GATEWAY_SHUTDOWN_TIMEOUT": "20s",
		"ORBIT_GATEWAY_CONTROL_BUFFER":   "64",
		"ORBIT_DEVICE_CONNECTION_BUFFER": "8",
	}
	got, err := LoadGateway(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("LoadGateway() error = %v", err)
	}
	if got.GatewayID != "gateway-1" || got.ControlAddress != "control:6000" || got.ListenAddress != ":6001" ||
		got.ShutdownTimeout != 20*time.Second || got.ControlBuffer != 64 || got.ConnectionBuffer != 8 {
		t.Fatalf("LoadGateway() = %+v", got)
	}
}

func TestLoadGatewayRejectsInvalidConfiguration(t *testing.T) {
	for _, values := range []map[string]string{
		{},
		{"ORBIT_GATEWAY_ID": "bad gateway"},
		{"ORBIT_GATEWAY_ID": "gateway", "ORBIT_GATEWAY_CONTROL_BUFFER": "0"},
		{"ORBIT_GATEWAY_ID": "gateway", "ORBIT_GATEWAY_SHUTDOWN_TIMEOUT": "0s"},
	} {
		if _, err := LoadGateway(func(key string) (string, bool) {
			value, ok := values[key]
			return value, ok
		}); err == nil {
			t.Fatalf("LoadGateway(%v) unexpectedly succeeded", values)
		}
	}
}
