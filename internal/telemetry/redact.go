package telemetry

import "regexp"

var (
	deviceIDPattern  = regexp.MustCompile(`(?i)\bdevice[_-]?id[=:\s]+[A-Za-z0-9._-]+`)
	commandIDPattern = regexp.MustCompile(`(?i)\bcommand[_-]?id[=:\s]+[0-9a-f-]{36}`)
)

// RedactLogValue removes device and command identifiers from free-form log text.
func RedactLogValue(value string) string {
	value = deviceIDPattern.ReplaceAllString(value, "device_id=<redacted>")
	value = commandIDPattern.ReplaceAllString(value, "command_id=<redacted>")
	return value
}
