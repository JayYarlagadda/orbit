package history

import "testing"

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
}
