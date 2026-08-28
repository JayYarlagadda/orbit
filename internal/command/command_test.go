package command

import (
	"errors"
	"testing"
	"time"
)

func TestNewSubmissionNormalizesAndHashes(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.FixedZone("test", -7*60*60))
	expiresAt := now.Add(time.Hour)
	payload := []byte("diagnostic")

	got, err := NewSubmission(" producer-1 ", " request-42 ", " device-7 ", 4, payload, expiresAt, now)
	if err != nil {
		t.Fatalf("NewSubmission() error = %v", err)
	}
	payload[0] = 'X'

	if got.ProducerID != "producer-1" || got.IdempotencyKey != "request-42" || got.DeviceID != "device-7" {
		t.Fatalf("identifiers were not normalized: %+v", got)
	}
	if string(got.Payload) != "diagnostic" {
		t.Fatalf("payload aliases caller memory: %q", got.Payload)
	}
	if got.ExpiresAt.Location() != time.UTC {
		t.Fatalf("ExpiresAt location = %v, want UTC", got.ExpiresAt.Location())
	}
	if got.PayloadHash == ([32]byte{}) || got.RequestHash == ([32]byte{}) {
		t.Fatal("expected non-zero hashes")
	}

	same, err := NewSubmission("producer-1", "request-42", "device-7", 4, []byte("diagnostic"), expiresAt, now)
	if err != nil {
		t.Fatalf("NewSubmission(same) error = %v", err)
	}
	if same.RequestHash != got.RequestHash {
		t.Fatal("equivalent normalized requests produced different hashes")
	}

	changed, err := NewSubmission("producer-1", "request-42", "device-7", 5, []byte("diagnostic"), expiresAt, now)
	if err != nil {
		t.Fatalf("NewSubmission(changed) error = %v", err)
	}
	if changed.RequestHash == got.RequestHash {
		t.Fatal("different requests produced the same request hash")
	}
}

func TestNewSubmissionRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, time.August, 27, 19, 0, 0, 0, time.UTC)
	validExpiry := now.Add(time.Minute)

	tests := []struct {
		name           string
		producerID     string
		idempotencyKey string
		deviceID       string
		priority       int16
		payload        []byte
		expiresAt      time.Time
		field          string
	}{
		{name: "producer", producerID: "bad producer", idempotencyKey: "key", deviceID: "device", payload: []byte("x"), expiresAt: validExpiry, field: "producer_id"},
		{name: "idempotency key", producerID: "producer", idempotencyKey: " ", deviceID: "device", payload: []byte("x"), expiresAt: validExpiry, field: "idempotency_key"},
		{name: "device", producerID: "producer", idempotencyKey: "key", deviceID: "device/1", payload: []byte("x"), expiresAt: validExpiry, field: "device_id"},
		{name: "priority", producerID: "producer", idempotencyKey: "key", deviceID: "device", priority: MaxPriority + 1, payload: []byte("x"), expiresAt: validExpiry, field: "priority"},
		{name: "empty payload", producerID: "producer", idempotencyKey: "key", deviceID: "device", payload: nil, expiresAt: validExpiry, field: "payload"},
		{name: "large payload", producerID: "producer", idempotencyKey: "key", deviceID: "device", payload: make([]byte, MaxPayloadBytes+1), expiresAt: validExpiry, field: "payload"},
		{name: "expiry", producerID: "producer", idempotencyKey: "key", deviceID: "device", payload: []byte("x"), expiresAt: now, field: "expires_at"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSubmission(test.producerID, test.idempotencyKey, test.deviceID, test.priority, test.payload, test.expiresAt, now)
			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("NewSubmission() error = %v, want ValidationError", err)
			}
			if validationError.Field != test.field {
				t.Fatalf("ValidationError.Field = %q, want %q", validationError.Field, test.field)
			}
		})
	}
}

func TestStateTransitions(t *testing.T) {
	states := []State{
		StateQueued, StateLeased, StateInFlight, StateRetryWait,
		StateAcknowledged, StateExpired, StateDeadLetter, StateCancelled,
	}
	allowed := map[[2]State]bool{
		{StateQueued, StateLeased}:         true,
		{StateQueued, StateExpired}:        true,
		{StateQueued, StateCancelled}:      true,
		{StateLeased, StateInFlight}:       true,
		{StateLeased, StateRetryWait}:      true,
		{StateLeased, StateExpired}:        true,
		{StateLeased, StateDeadLetter}:     true,
		{StateInFlight, StateAcknowledged}: true,
		{StateInFlight, StateRetryWait}:    true,
		{StateInFlight, StateExpired}:      true,
		{StateInFlight, StateDeadLetter}:   true,
		{StateRetryWait, StateLeased}:      true,
		{StateRetryWait, StateExpired}:     true,
		{StateRetryWait, StateDeadLetter}:  true,
		{StateRetryWait, StateCancelled}:   true,
	}

	for _, from := range states {
		for _, to := range states {
			err := ValidateTransition(from, to)
			if allowed[[2]State{from, to}] && err != nil {
				t.Errorf("ValidateTransition(%s, %s) error = %v", from, to, err)
			}
			if !allowed[[2]State{from, to}] && err == nil {
				t.Errorf("ValidateTransition(%s, %s) unexpectedly succeeded", from, to)
			}
		}
	}
}

func TestStateClassification(t *testing.T) {
	for _, state := range []State{StateAcknowledged, StateExpired, StateDeadLetter, StateCancelled} {
		if !state.IsKnown() || !state.IsTerminal() {
			t.Errorf("state %s should be known and terminal", state)
		}
	}
	for _, state := range []State{StateQueued, StateLeased, StateInFlight, StateRetryWait} {
		if !state.IsKnown() || state.IsTerminal() {
			t.Errorf("state %s should be known and non-terminal", state)
		}
	}
	if State("BROKEN").IsKnown() || State("BROKEN").IsTerminal() {
		t.Fatal("unknown state was classified as valid")
	}
}

func TestNewLeaseRequest(t *testing.T) {
	got, err := NewLeaseRequest(" worker-1 ", 32, 15*time.Second)
	if err != nil {
		t.Fatalf("NewLeaseRequest() error = %v", err)
	}
	if got.Owner != "worker-1" || got.Limit != 32 || got.Duration != 15*time.Second {
		t.Fatalf("NewLeaseRequest() = %+v", got)
	}

	for _, test := range []struct {
		owner    string
		limit    int
		duration time.Duration
	}{
		{owner: "bad owner", limit: 1, duration: time.Second},
		{owner: "worker", limit: 0, duration: time.Second},
		{owner: "worker", limit: MaxLeaseBatchSize + 1, duration: time.Second},
		{owner: "worker", limit: 1, duration: MinLeaseDuration - time.Nanosecond},
		{owner: "worker", limit: 1, duration: MaxLeaseDuration + time.Nanosecond},
	} {
		if _, err := NewLeaseRequest(test.owner, test.limit, test.duration); err == nil {
			t.Fatalf("NewLeaseRequest(%q, %d, %s) unexpectedly succeeded", test.owner, test.limit, test.duration)
		}
	}
}

func TestNewAcknowledgement(t *testing.T) {
	appliedAt := time.Date(2026, time.August, 28, 7, 0, 0, 0, time.FixedZone("test", 2*60*60))
	hash := make([]byte, 32)
	hash[0] = 42
	got, err := NewAcknowledgement(
		"76386381-325e-49c6-82d1-afd7f140fcaf",
		"device-1",
		3,
		"gateway-1",
		7,
		2,
		hash,
		&appliedAt,
	)
	if err != nil {
		t.Fatalf("NewAcknowledgement() error = %v", err)
	}
	hash[0] = 0
	if got.ResultHash[0] != 42 || got.ClientAppliedAt.Location() != time.UTC {
		t.Fatalf("NewAcknowledgement() = %+v", got)
	}

	for _, mutate := range []func(*Acknowledgement){
		func(value *Acknowledgement) { value.CommandID = "bad" },
		func(value *Acknowledgement) { value.DeviceID = "bad device" },
		func(value *Acknowledgement) { value.SequenceNumber = 0 },
		func(value *Acknowledgement) { value.GatewayID = "" },
		func(value *Acknowledgement) { value.LeaseToken = 0 },
		func(value *Acknowledgement) { value.SessionEpoch = 0 },
	} {
		invalid := got
		mutate(&invalid)
		if _, err := NewAcknowledgement(
			invalid.CommandID,
			invalid.DeviceID,
			invalid.SequenceNumber,
			invalid.GatewayID,
			invalid.LeaseToken,
			invalid.SessionEpoch,
			invalid.ResultHash[:],
			invalid.ClientAppliedAt,
		); err == nil {
			t.Fatalf("NewAcknowledgement(%+v) unexpectedly succeeded", invalid)
		}
	}
}
