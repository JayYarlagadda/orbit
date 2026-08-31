package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"github.com/JayYarlagadda/orbit/internal/session"
)

const defaultGatewayMetricsAddress = "127.0.0.1:9092"

type Gateway struct {
	GatewayID             string
	ControlAddress        string
	ListenAddress         string
	MetricsAddress        string
	ShutdownTimeout       time.Duration
	ControlBuffer         int
	ConnectionBuffer      int
	ReconnectInitialDelay time.Duration
	ReconnectMaxDelay     time.Duration
	MaxReconnectAttempts  int
	HeartbeatInterval     time.Duration
	HeartbeatTimeout      time.Duration
}

// LoadGateway reads the gateway settings. MaxReconnectAttempts counts
// consecutive control-stream failures and zero means the gateway retries for as
// long as it runs; the retry rate stays bounded by ReconnectMaxDelay either way.
func LoadGateway(lookup LookupEnv) (Gateway, error) {
	result := Gateway{
		ControlAddress:        "127.0.0.1:50051",
		ListenAddress:         "127.0.0.1:50052",
		MetricsAddress:        defaultGatewayMetricsAddress,
		ShutdownTimeout:       10 * time.Second,
		ControlBuffer:         256,
		ConnectionBuffer:      16,
		ReconnectInitialDelay: defaultReconnectInitial,
		ReconnectMaxDelay:     defaultReconnectMax,
		HeartbeatInterval:     heartbeat.DefaultInterval,
		HeartbeatTimeout:      heartbeat.DefaultTimeout,
	}
	value, ok := lookup("ORBIT_GATEWAY_ID")
	if !ok {
		return Gateway{}, fmt.Errorf("ORBIT_GATEWAY_ID is required")
	}
	gatewayID, err := session.NormalizeIdentifier("ORBIT_GATEWAY_ID", value)
	if err != nil {
		return Gateway{}, err
	}
	result.GatewayID = gatewayID

	for key, target := range map[string]*string{
		"ORBIT_CONTROL_ADDRESS":        &result.ControlAddress,
		"ORBIT_GATEWAY_LISTEN_ADDRESS": &result.ListenAddress,
	} {
		if value, ok := lookup(key); ok {
			value = strings.TrimSpace(value)
			if value == "" {
				return Gateway{}, fmt.Errorf("%s must not be empty", key)
			}
			*target = value
		}
	}
	if value, ok := lookup("ORBIT_METRICS_ADDRESS"); ok {
		result.MetricsAddress = strings.TrimSpace(value)
	}
	if value, ok := lookup("ORBIT_GATEWAY_SHUTDOWN_TIMEOUT"); ok {
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil || parsed < time.Second || parsed > 2*time.Minute {
			return Gateway{}, fmt.Errorf("ORBIT_GATEWAY_SHUTDOWN_TIMEOUT must be between 1s and 2m")
		}
		result.ShutdownTimeout = parsed
	}
	for _, setting := range []struct {
		key     string
		target  *int
		minimum int64
		maximum int64
	}{
		{key: "ORBIT_GATEWAY_CONTROL_BUFFER", target: &result.ControlBuffer, minimum: 1, maximum: 4096},
		{key: "ORBIT_DEVICE_CONNECTION_BUFFER", target: &result.ConnectionBuffer, minimum: 1, maximum: 256},
		{key: "ORBIT_GATEWAY_MAX_RECONNECT_ATTEMPTS", target: &result.MaxReconnectAttempts, minimum: 0, maximum: 1000},
	} {
		if value, ok := lookup(setting.key); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return Gateway{}, fmt.Errorf("%s must be between %d and %d", setting.key, setting.minimum, setting.maximum)
			}
			*setting.target = int(parsed)
		}
	}

	for _, setting := range []struct {
		key     string
		target  *time.Duration
		minimum time.Duration
		maximum time.Duration
	}{
		{key: "ORBIT_GATEWAY_RECONNECT_INITIAL_DELAY", target: &result.ReconnectInitialDelay, minimum: 10 * time.Millisecond, maximum: 10 * time.Second},
		{key: "ORBIT_GATEWAY_RECONNECT_MAX_DELAY", target: &result.ReconnectMaxDelay, minimum: 100 * time.Millisecond, maximum: 2 * time.Minute},
	} {
		if value, ok := lookup(setting.key); ok {
			parsed, err := time.ParseDuration(strings.TrimSpace(value))
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return Gateway{}, fmt.Errorf("%s must be between %s and %s", setting.key, setting.minimum, setting.maximum)
			}
			*setting.target = parsed
		}
	}
	// A cap below the initial delay would make the backoff shrink as failures
	// accumulate instead of growing.
	if result.ReconnectMaxDelay < result.ReconnectInitialDelay {
		return Gateway{}, fmt.Errorf("ORBIT_GATEWAY_RECONNECT_MAX_DELAY must not be below ORBIT_GATEWAY_RECONNECT_INITIAL_DELAY")
	}
	if err := applyHeartbeatSettings(
		lookup,
		"ORBIT_GATEWAY_HEARTBEAT_INTERVAL",
		"ORBIT_GATEWAY_HEARTBEAT_TIMEOUT",
		&result.HeartbeatInterval,
		&result.HeartbeatTimeout,
	); err != nil {
		return Gateway{}, err
	}
	return result, nil
}
