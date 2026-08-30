package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"github.com/JayYarlagadda/orbit/internal/session"
)

const (
	defaultClientGatewayAddress = "127.0.0.1:50052"
	defaultClientStatePath      = "data/orbit-client-state.json"
	defaultClientRetention      = 1024
	defaultReconnectInitial     = 250 * time.Millisecond
	defaultReconnectMax         = 10 * time.Second
)

type Client struct {
	DeviceID              string
	ClientInstanceID      string
	GatewayAddress        string
	GatewayAddresses      []string
	GatewayIndex          int
	StatePath             string
	DedupRetention        int
	ReconnectInitialDelay time.Duration
	ReconnectMaxDelay     time.Duration
	MaxReconnectAttempts  int
	HeartbeatInterval     time.Duration
	HeartbeatTimeout      time.Duration
}

// LoadClient reads the reference-client settings. MaxReconnectAttempts counts
// consecutive failures and zero means the client retries for as long as it
// runs; the retry rate stays bounded by ReconnectMaxDelay either way.
func LoadClient(lookup LookupEnv) (Client, error) {
	result := Client{
		GatewayAddress:        defaultClientGatewayAddress,
		StatePath:             defaultClientStatePath,
		DedupRetention:        defaultClientRetention,
		ReconnectInitialDelay: defaultReconnectInitial,
		ReconnectMaxDelay:     defaultReconnectMax,
		HeartbeatInterval:     heartbeat.DefaultInterval,
		HeartbeatTimeout:      heartbeat.DefaultTimeout,
	}

	value, ok := lookup("ORBIT_DEVICE_ID")
	if !ok {
		return Client{}, fmt.Errorf("ORBIT_DEVICE_ID is required")
	}
	deviceID, err := session.NormalizeIdentifier("ORBIT_DEVICE_ID", value)
	if err != nil {
		return Client{}, err
	}
	result.DeviceID = deviceID

	if value, ok := lookup("ORBIT_CLIENT_INSTANCE_ID"); ok {
		instanceID, err := session.NormalizeIdentifier("ORBIT_CLIENT_INSTANCE_ID", value)
		if err != nil {
			return Client{}, err
		}
		result.ClientInstanceID = instanceID
	}

	for key, target := range map[string]*string{
		"ORBIT_CLIENT_GATEWAY_ADDRESS": &result.GatewayAddress,
		"ORBIT_CLIENT_STATE_PATH":      &result.StatePath,
	} {
		if value, ok := lookup(key); ok {
			value = strings.TrimSpace(value)
			if value == "" {
				return Client{}, fmt.Errorf("%s must not be empty", key)
			}
			*target = value
		}
	}

	if value, ok := lookup("ORBIT_CLIENT_GATEWAY_ADDRESSES"); ok {
		addresses, err := parseGatewayAddresses(value)
		if err != nil {
			return Client{}, err
		}
		result.GatewayAddresses = addresses
	}
	if len(result.GatewayAddresses) == 0 && result.GatewayAddress != "" {
		result.GatewayAddresses = []string{result.GatewayAddress}
	}
	if len(result.GatewayAddresses) > 0 && result.GatewayAddress == defaultClientGatewayAddress {
		result.GatewayAddress = result.GatewayAddresses[0]
	}

	integerSettings := []struct {
		key     string
		target  *int
		minimum int64
		maximum int64
	}{
		{key: "ORBIT_CLIENT_DEDUP_RETENTION", target: &result.DedupRetention, minimum: 1, maximum: 100_000},
		{key: "ORBIT_CLIENT_MAX_RECONNECT_ATTEMPTS", target: &result.MaxReconnectAttempts, minimum: 0, maximum: 1000},
		{key: "ORBIT_CLIENT_GATEWAY_INDEX", target: &result.GatewayIndex, minimum: 0, maximum: 16},
	}
	for _, setting := range integerSettings {
		if value, ok := lookup(setting.key); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return Client{}, fmt.Errorf("%s must be between %d and %d", setting.key, setting.minimum, setting.maximum)
			}
			*setting.target = int(parsed)
		}
	}

	durationSettings := []struct {
		key     string
		target  *time.Duration
		minimum time.Duration
		maximum time.Duration
	}{
		{key: "ORBIT_CLIENT_RECONNECT_INITIAL_DELAY", target: &result.ReconnectInitialDelay, minimum: 10 * time.Millisecond, maximum: 10 * time.Second},
		{key: "ORBIT_CLIENT_RECONNECT_MAX_DELAY", target: &result.ReconnectMaxDelay, minimum: 100 * time.Millisecond, maximum: 2 * time.Minute},
	}
	for _, setting := range durationSettings {
		if value, ok := lookup(setting.key); ok {
			parsed, err := time.ParseDuration(strings.TrimSpace(value))
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return Client{}, fmt.Errorf("%s must be between %s and %s", setting.key, setting.minimum, setting.maximum)
			}
			*setting.target = parsed
		}
	}
	if result.ReconnectMaxDelay < result.ReconnectInitialDelay {
		return Client{}, fmt.Errorf("ORBIT_CLIENT_RECONNECT_MAX_DELAY must not be below ORBIT_CLIENT_RECONNECT_INITIAL_DELAY")
	}
	if len(result.GatewayAddresses) > 0 && result.GatewayIndex >= len(result.GatewayAddresses) {
		return Client{}, fmt.Errorf("ORBIT_CLIENT_GATEWAY_INDEX must be below the number of gateway addresses (%d)", len(result.GatewayAddresses))
	}
	if err := applyHeartbeatSettings(
		lookup,
		"ORBIT_CLIENT_HEARTBEAT_INTERVAL",
		"ORBIT_CLIENT_HEARTBEAT_TIMEOUT",
		&result.HeartbeatInterval,
		&result.HeartbeatTimeout,
	); err != nil {
		return Client{}, err
	}
	return result, nil
}

func parseGatewayAddresses(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	addresses := make([]string, 0, len(parts))
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("ORBIT_CLIENT_GATEWAY_ADDRESSES[%d] must not be empty", index)
		}
		addresses = append(addresses, part)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("ORBIT_CLIENT_GATEWAY_ADDRESSES must contain at least one address")
	}
	return addresses, nil
}
