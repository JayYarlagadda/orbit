package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/JayYarlagadda/orbit/internal/heartbeat"
)

func applyHeartbeatSettings(lookup LookupEnv, intervalKey, timeoutKey string, interval, timeout *time.Duration) error {
	*interval = heartbeat.DefaultInterval
	*timeout = heartbeat.DefaultTimeout
	for _, setting := range []struct {
		key     string
		target  *time.Duration
		minimum time.Duration
		maximum time.Duration
	}{
		{key: intervalKey, target: interval, minimum: 10 * time.Millisecond, maximum: time.Minute},
		{key: timeoutKey, target: timeout, minimum: 100 * time.Millisecond, maximum: 5 * time.Minute},
	} {
		if value, ok := lookup(setting.key); ok {
			parsed, err := time.ParseDuration(strings.TrimSpace(value))
			if err != nil || parsed < setting.minimum || parsed > setting.maximum {
				return fmt.Errorf("%s must be between %s and %s", setting.key, setting.minimum, setting.maximum)
			}
			*setting.target = parsed
		}
	}
	if err := (heartbeat.Settings{Interval: *interval, Timeout: *timeout}).Validate(); err != nil {
		return fmt.Errorf("%s/%s: %w", intervalKey, timeoutKey, err)
	}
	return nil
}
