package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Config struct {
	SchemaVersion       string `json:"schema_version"`
	Name                string `json:"name"`
	MatrixID            string `json:"matrix_id"`
	Clients             int    `json:"clients"`
	Commands            int    `json:"commands"`
	PayloadBytes        int    `json:"payload_bytes"`
	WarmupCommands      int    `json:"warmup_commands"`
	Trials              int    `json:"trials"`
	SubmitConcurrency   int    `json:"submit_concurrency"`
	Priority            int    `json:"priority"`
	ExpiresAfterSeconds int    `json:"expires_after_seconds"`
	ControlAddress      string `json:"control_address"`
	GatewayAddress      string `json:"gateway_address"`
	ProducerID          string `json:"producer_id"`
	GatewayID           string `json:"gateway_id"`
	PollIntervalMS      int    `json:"poll_interval_ms"`
	TrialTimeoutSeconds int    `json:"trial_timeout_seconds"`
}

func LoadConfig(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read benchmark config: %w", err)
	}
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return Config{}, fmt.Errorf("parse benchmark config: %w", err)
	}
	return config.withDefaults(), nil
}

func (c Config) withDefaults() Config {
	if c.Priority == 0 {
		c.Priority = 4
	}
	if c.ExpiresAfterSeconds == 0 {
		c.ExpiresAfterSeconds = 3600
	}
	if c.GatewayID == "" {
		c.GatewayID = "gateway-bench"
	}
	if c.PollIntervalMS == 0 {
		c.PollIntervalMS = 25
	}
	if c.TrialTimeoutSeconds == 0 {
		c.TrialTimeoutSeconds = 600
	}
	return c
}

func (c Config) Validate() error {
	switch {
	case c.SchemaVersion != "1":
		return fmt.Errorf("schema_version must be 1")
	case c.Name == "":
		return fmt.Errorf("name is required")
	case c.Clients < 1 || c.Commands < 1:
		return fmt.Errorf("clients and commands must be positive")
	case c.Trials < 1:
		return fmt.Errorf("trials must be at least 1")
	case c.SubmitConcurrency < 1:
		return fmt.Errorf("submit_concurrency must be at least 1")
	case c.ControlAddress == "" || c.GatewayAddress == "":
		return fmt.Errorf("control_address and gateway_address are required")
	case c.ProducerID == "":
		return fmt.Errorf("producer_id is required")
	}
	return nil
}

func (c Config) PollInterval() time.Duration {
	return time.Duration(c.PollIntervalMS) * time.Millisecond
}

func (c Config) TrialTimeout() time.Duration {
	return time.Duration(c.TrialTimeoutSeconds) * time.Second
}

func (c Config) ExpiresAfter() time.Duration {
	return time.Duration(c.ExpiresAfterSeconds) * time.Second
}

func (c Config) DeviceID(index int) string {
	return fmt.Sprintf("bench-device-%04d", index)
}
