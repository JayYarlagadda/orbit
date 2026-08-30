package config

import (
	"testing"
	"time"
)

func TestApplyHeartbeatSettingsRejectsTimeoutBelowInterval(t *testing.T) {
	var interval, timeout time.Duration
	err := applyHeartbeatSettings(func(key string) (string, bool) {
		switch key {
		case "ORBIT_TEST_HEARTBEAT_INTERVAL":
			return "5s", true
		case "ORBIT_TEST_HEARTBEAT_TIMEOUT":
			return "1s", true
		default:
			return "", false
		}
	}, "ORBIT_TEST_HEARTBEAT_INTERVAL", "ORBIT_TEST_HEARTBEAT_TIMEOUT", &interval, &timeout)
	if err == nil {
		t.Fatal("applyHeartbeatSettings() accepted a timeout below the interval")
	}
}
