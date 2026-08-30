package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayYarlagadda/orbit/internal/command"
	"github.com/JayYarlagadda/orbit/internal/heartbeat"
)

const (
	defaultListenAddress  = "127.0.0.1:50051"
	defaultShutdown       = 10 * time.Second
	defaultMaxConnections = int32(10)
	defaultGatewayBuffer  = 128
	defaultLeaseBatch     = 32
	defaultSweepBatch     = 64
	defaultLeaseDuration  = 15 * time.Second
	defaultPollInterval   = 250 * time.Millisecond
)

type LookupEnv func(string) (string, bool)

type Orbitd struct {
	ListenAddress           string
	DatabaseURL             string
	ShutdownTimeout         time.Duration
	DBMaxConnections        int32
	GatewayOutboundBuffer   int
	SchedulerLeaseBatch     int
	SchedulerSweepBatch     int
	SchedulerLeaseDuration  time.Duration
	SchedulerPollInterval   time.Duration
	HeartbeatInterval       time.Duration
	HeartbeatTimeout        time.Duration
	MaxDeliveryAttempts     int32
	RetryBaseDelay          time.Duration
	RetryMaxDelay           time.Duration
	GlobalAdmissionLimit    int
	PerDeviceAdmissionLimit int
}

func LoadOrbitd(lookup LookupEnv) (Orbitd, error) {
	config := Orbitd{
		ListenAddress:           defaultListenAddress,
		ShutdownTimeout:         defaultShutdown,
		DBMaxConnections:        defaultMaxConnections,
		GatewayOutboundBuffer:   defaultGatewayBuffer,
		SchedulerLeaseBatch:     defaultLeaseBatch,
		SchedulerSweepBatch:     defaultSweepBatch,
		SchedulerLeaseDuration:  defaultLeaseDuration,
		SchedulerPollInterval:   defaultPollInterval,
		HeartbeatInterval:       heartbeat.DefaultInterval,
		HeartbeatTimeout:        heartbeat.DefaultTimeout,
		MaxDeliveryAttempts:     command.DefaultMaxDeliveryAttempts,
		RetryBaseDelay:          command.DefaultRetryBaseDelay,
		RetryMaxDelay:           command.DefaultRetryMaxDelay,
		GlobalAdmissionLimit:    command.DefaultGlobalAdmissionLimit,
		PerDeviceAdmissionLimit: command.DefaultPerDeviceAdmissionLimit,
	}

	if value, ok := lookup("ORBIT_LISTEN_ADDRESS"); ok {
		config.ListenAddress = strings.TrimSpace(value)
	}
	if config.ListenAddress == "" {
		return Orbitd{}, fmt.Errorf("ORBIT_LISTEN_ADDRESS must not be empty")
	}

	value, ok := lookup("ORBIT_DATABASE_URL")
	if !ok || strings.TrimSpace(value) == "" {
		return Orbitd{}, fmt.Errorf("ORBIT_DATABASE_URL is required")
	}
	config.DatabaseURL = strings.TrimSpace(value)

	if value, ok := lookup("ORBIT_SHUTDOWN_TIMEOUT"); ok {
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil || parsed < time.Second || parsed > 2*time.Minute {
			return Orbitd{}, fmt.Errorf("ORBIT_SHUTDOWN_TIMEOUT must be between 1s and 2m")
		}
		config.ShutdownTimeout = parsed
	}
	if value, ok := lookup("ORBIT_DB_MAX_CONNECTIONS"); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
		if err != nil || parsed < 1 || parsed > 100 {
			return Orbitd{}, fmt.Errorf("ORBIT_DB_MAX_CONNECTIONS must be between 1 and 100")
		}
		config.DBMaxConnections = int32(parsed)
	}
	integerSettings := []struct {
		key     string
		target  *int
		minimum int64
		maximum int64
	}{
		{key: "ORBIT_GATEWAY_OUTBOUND_BUFFER", target: &config.GatewayOutboundBuffer, minimum: 1, maximum: 4096},
		{key: "ORBIT_SCHEDULER_LEASE_BATCH", target: &config.SchedulerLeaseBatch, minimum: 1, maximum: 256},
		{key: "ORBIT_SCHEDULER_SWEEP_BATCH", target: &config.SchedulerSweepBatch, minimum: 1, maximum: 256},
	}
	for _, setting := range integerSettings {
		if value, ok := lookup(setting.key); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return Orbitd{}, fmt.Errorf("%s must be between %d and %d", setting.key, setting.minimum, setting.maximum)
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
		{key: "ORBIT_SCHEDULER_LEASE_DURATION", target: &config.SchedulerLeaseDuration, minimum: time.Second, maximum: 5 * time.Minute},
		{key: "ORBIT_SCHEDULER_POLL_INTERVAL", target: &config.SchedulerPollInterval, minimum: 10 * time.Millisecond, maximum: time.Minute},
	}
	for _, setting := range durationSettings {
		if value, ok := lookup(setting.key); ok {
			parsed, err := time.ParseDuration(strings.TrimSpace(value))
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return Orbitd{}, fmt.Errorf("%s must be between %s and %s", setting.key, setting.minimum, setting.maximum)
			}
			*setting.target = parsed
		}
	}
	if err := applyHeartbeatSettings(
		lookup,
		"ORBIT_CONTROL_HEARTBEAT_INTERVAL",
		"ORBIT_CONTROL_HEARTBEAT_TIMEOUT",
		&config.HeartbeatInterval,
		&config.HeartbeatTimeout,
	); err != nil {
		return Orbitd{}, err
	}
	if value, ok := lookup("ORBIT_MAX_DELIVERY_ATTEMPTS"); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
		if err != nil || parsed < 1 || parsed > 100 {
			return Orbitd{}, fmt.Errorf("ORBIT_MAX_DELIVERY_ATTEMPTS must be between 1 and 100")
		}
		config.MaxDeliveryAttempts = int32(parsed)
	}
	retryDurations := []struct {
		key     string
		target  *time.Duration
		minimum time.Duration
		maximum time.Duration
	}{
		{key: "ORBIT_RETRY_BASE_DELAY", target: &config.RetryBaseDelay, minimum: 10 * time.Millisecond, maximum: time.Minute},
		{key: "ORBIT_RETRY_MAX_DELAY", target: &config.RetryMaxDelay, minimum: time.Second, maximum: 30 * time.Minute},
	}
	for _, setting := range retryDurations {
		if value, ok := lookup(setting.key); ok {
			parsed, err := time.ParseDuration(strings.TrimSpace(value))
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return Orbitd{}, fmt.Errorf("%s must be between %s and %s", setting.key, setting.minimum, setting.maximum)
			}
			*setting.target = parsed
		}
	}
	admissionSettings := []struct {
		key     string
		target  *int
		minimum int64
		maximum int64
	}{
		{key: "ORBIT_GLOBAL_ADMISSION_LIMIT", target: &config.GlobalAdmissionLimit, minimum: 1, maximum: 1_000_000},
		{key: "ORBIT_PER_DEVICE_ADMISSION_LIMIT", target: &config.PerDeviceAdmissionLimit, minimum: 1, maximum: 100_000},
	}
	for _, setting := range admissionSettings {
		if value, ok := lookup(setting.key); ok {
			parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return Orbitd{}, fmt.Errorf("%s must be between %d and %d", setting.key, setting.minimum, setting.maximum)
			}
			*setting.target = int(parsed)
		}
	}
	retryPolicy := command.RetryPolicy{
		MaxAttempts: config.MaxDeliveryAttempts,
		BaseDelay:   config.RetryBaseDelay,
		MaxDelay:    config.RetryMaxDelay,
	}
	if err := retryPolicy.Validate(); err != nil {
		return Orbitd{}, fmt.Errorf("retry policy: %w", err)
	}
	admissionLimits := command.AdmissionLimits{
		GlobalMax:    config.GlobalAdmissionLimit,
		PerDeviceMax: config.PerDeviceAdmissionLimit,
	}
	if err := admissionLimits.Validate(); err != nil {
		return Orbitd{}, fmt.Errorf("admission limits: %w", err)
	}
	return config, nil
}
