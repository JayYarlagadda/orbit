package history

import (
	"testing"
	"time"
)

func TestCheckerDetectsCorruptedHistories(t *testing.T) {
	passing := Record{
		AuditEvents: []AuditEvent{
			{CommandID: "cmd-1", NewState: "QUEUED"},
			{CommandID: "cmd-1", OldState: "QUEUED", NewState: "ACKNOWLEDGED"},
		},
		Commands: []CommandSnapshot{
			{ID: "cmd-1", DeviceID: "device-a", SequenceNumber: 1, State: "ACKNOWLEDGED"},
		},
		Applications: []ClientApplication{
			{CommandID: "cmd-1", DeviceID: "device-a", SequenceNumber: 1},
		},
	}
	if report := Check(passing); !report.Passed {
		t.Fatalf("expected passing history, got %+v", report.Violations)
	}

	leavesAck := passing
	leavesAck.AuditEvents = append([]AuditEvent(nil), passing.AuditEvents...)
	leavesAck.AuditEvents = append(leavesAck.AuditEvents, AuditEvent{
		CommandID: "cmd-1",
		OldState:  "ACKNOWLEDGED",
		NewState:  "LEASED",
	})
	if report := Check(leavesAck); report.Passed {
		t.Fatal("expected INV-01 violation")
	}

	doubleApply := passing
	doubleApply.Applications = append(doubleApply.Applications, ClientApplication{
		CommandID: "cmd-1", DeviceID: "device-a", SequenceNumber: 1,
	})
	if report := Check(doubleApply); report.Passed {
		t.Fatal("expected INV-02 violation")
	}

	leaseRegression := passing
	leaseRegression.AuditEvents = []AuditEvent{
		{CommandID: "cmd-1", NewState: "LEASED", LeaseToken: 2},
		{CommandID: "cmd-1", OldState: "LEASED", NewState: "IN_FLIGHT", LeaseToken: 1},
	}
	if report := Check(leaseRegression); report.Passed || report.Violations[0].Invariant != InvFencingMonotonicity {
		t.Fatalf("expected INV-05 violation, got %+v", report)
	}

	staleSession := passing
	now := time.Now()
	staleSession.Attempts = []DeliveryAttempt{
		{CommandID: "cmd-1", DeviceID: "device-a", SessionEpoch: 2, Outcome: "ACKNOWLEDGED", StartedAt: now},
		{CommandID: "cmd-1", DeviceID: "device-a", SessionEpoch: 1, Outcome: "ACKNOWLEDGED", StartedAt: now.Add(time.Second)},
	}
	report := Check(staleSession)
	if report.Passed {
		t.Fatalf("expected INV-08 violation, got %+v", report)
	}
	foundINV08 := false
	for _, violation := range report.Violations {
		if violation.Invariant == InvSingleActiveSession {
			foundINV08 = true
			break
		}
	}
	if !foundINV08 {
		t.Fatalf("expected INV-08 violation, got %+v", report.Violations)
	}

	expired := passing
	expired.Commands[0].ExpiresAt = now
	expired.Attempts = []DeliveryAttempt{
		{
			CommandID: "cmd-1",
			DeviceID:  "device-a",
			StartedAt: now.Add(time.Second),
			Outcome:   "LEASED",
		},
	}
	if report := Check(expired); report.Passed || report.Violations[0].Invariant != InvNoDeliveryAfterExpiry {
		t.Fatalf("expected INV-04 violation, got %+v", report)
	}

	duplicateKey := passing
	duplicateKey.Commands = append(duplicateKey.Commands, CommandSnapshot{
		ID:             "cmd-2",
		DeviceID:       "device-a",
		ProducerID:     "producer-a",
		IdempotencyKey: "request-1",
		RequestHash:    "deadbeef",
		SequenceNumber: 2,
		State:          "QUEUED",
	})
	duplicateKey.Commands[0].ProducerID = "producer-a"
	duplicateKey.Commands[0].IdempotencyKey = "request-1"
	duplicateKey.Commands[0].RequestHash = "cafebabe"
	if report := Check(duplicateKey); report.Passed || report.Violations[0].Invariant != InvIdempotencyConsistency {
		t.Fatalf("expected INV-07 violation, got %+v", report)
	}

	brokenAudit := passing
	brokenAudit.AuditEvents = []AuditEvent{
		{CommandID: "cmd-1", NewState: "QUEUED"},
		{CommandID: "cmd-1", OldState: "LEASED", NewState: "ACKNOWLEDGED"},
	}
	if report := Check(brokenAudit); report.Passed || report.Violations[0].Invariant != InvAuditableTransitions {
		t.Fatalf("expected INV-11 violation, got %+v", report)
	}
}
