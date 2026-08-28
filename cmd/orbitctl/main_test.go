package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseSubmitOptions(t *testing.T) {
	got, err := parseOptions([]string{
		"submit",
		"-producer", "producer-1",
		"-idempotency-key", "request-1",
		"-device", "device-1",
		"-priority", "5",
		"-payload", "diagnostic",
		"-expires-after", "30m",
	})
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	if got.command != "submit" || got.priority != 5 || got.payload != "diagnostic" || got.expiresAfter != 30*time.Minute {
		t.Fatalf("parseOptions() = %+v", got)
	}
}

func TestParseOptionsRejectsAmbiguousPayload(t *testing.T) {
	_, err := parseOptions([]string{
		"submit",
		"-producer", "producer-1",
		"-idempotency-key", "request-1",
		"-device", "device-1",
		"-payload", "diagnostic",
		"-payload-file", "payload.bin",
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("parseOptions() error = %v", err)
	}
}

func TestParseGetRequiresCommandID(t *testing.T) {
	if _, err := parseOptions([]string{"get"}); err == nil {
		t.Fatal("parseOptions() unexpectedly succeeded")
	}
}
