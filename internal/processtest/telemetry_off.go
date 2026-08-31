package processtest

import "github.com/JayYarlagadda/orbit/internal/config"

func withTelemetryOff(env []string) []string {
	return append(env, config.TelemetryDisabledEnv()...)
}
