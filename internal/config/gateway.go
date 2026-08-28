package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayYarlagadda/orbit/internal/session"
)

type Gateway struct {
	GatewayID        string
	ControlAddress   string
	ListenAddress    string
	ShutdownTimeout  time.Duration
	ControlBuffer    int
	ConnectionBuffer int
}

func LoadGateway(lookup LookupEnv) (Gateway, error) {
	result := Gateway{
		ControlAddress:   "127.0.0.1:50051",
		ListenAddress:    "127.0.0.1:50052",
		ShutdownTimeout:  10 * time.Second,
		ControlBuffer:    256,
		ConnectionBuffer: 16,
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
		maximum int64
	}{
		{key: "ORBIT_GATEWAY_CONTROL_BUFFER", target: &result.ControlBuffer, maximum: 4096},
		{key: "ORBIT_DEVICE_CONNECTION_BUFFER", target: &result.ConnectionBuffer, maximum: 256},
	} {
		if value, ok := lookup(setting.key); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
			if err != nil || parsed < 1 || parsed > setting.maximum {
				return Gateway{}, fmt.Errorf("%s must be between 1 and %d", setting.key, setting.maximum)
			}
			*setting.target = int(parsed)
		}
	}
	return result, nil
}
