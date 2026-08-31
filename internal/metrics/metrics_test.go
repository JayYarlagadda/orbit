package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCommandMetricLabelsStayBounded(t *testing.T) {
	for _, labels := range LabelNames() {
		for _, label := range labels {
			if LabelValueLooksUnbounded(label) {
				t.Fatalf("metric label %q looks unbounded", label)
			}
		}
	}
}

func TestRecordCommandSubmittedRejectsUnboundedLabels(t *testing.T) {
	if err := RecordCommandSubmitted(SubmittedResultCreated); err != nil {
		t.Fatalf("RecordCommandSubmitted(created) error = %v", err)
	}
	if err := RecordCommandSubmitted("device_id=edge-1"); err == nil {
		t.Fatal("RecordCommandSubmitted() unexpectedly accepted unbounded label")
	}
}

func TestRecordAdmissionRejectedRejectsUnboundedLabels(t *testing.T) {
	if err := RecordAdmissionRejected(AdmissionReasonGlobal); err != nil {
		t.Fatalf("RecordAdmissionRejected(global) error = %v", err)
	}
	if err := RecordAdmissionRejected("command_id"); err == nil {
		t.Fatal("RecordAdmissionRejected() unexpectedly accepted unbounded label")
	}
}

func TestLifecycleCountersIncrement(t *testing.T) {
	if err := RecordCommandSubmitted(SubmittedResultCreated); err != nil {
		t.Fatalf("RecordCommandSubmitted() error = %v", err)
	}
	if err := RecordAdmissionRejected(AdmissionReasonPerDevice); err != nil {
		t.Fatalf("RecordAdmissionRejected() error = %v", err)
	}
	if err := RecordLeaseExpiration(LeaseExpiryOutcomeRetryWait); err != nil {
		t.Fatalf("RecordLeaseExpiration() error = %v", err)
	}
	if err := RecordStaleLeaseRejection(StaleLeaseOperationAcknowledge); err != nil {
		t.Fatalf("RecordStaleLeaseRejection() error = %v", err)
	}
	RecordCommandLeased()
	RecordCommandAcknowledged()
	ObserveCommandDeliveryDuration(0.5)
	if err := SetQueueDepth("QUEUED", 3); err != nil {
		t.Fatalf("SetQueueDepth() error = %v", err)
	}
	RecordGatewayControlReconnect()
	SetGatewayDeviceSessions(1)
	if err := RecordClientReconnect(ClientReconnectReasonFailover); err != nil {
		t.Fatalf("RecordClientReconnect() error = %v", err)
	}
	SetClientSessionActive(true)

	count, err := testutil.GatherAndCount(prometheus.DefaultGatherer)
	if err != nil {
		t.Fatalf("GatherAndCount() error = %v", err)
	}
	if count == 0 {
		t.Fatal("expected registered command metrics")
	}
}

func TestForbiddenLabelPattern(t *testing.T) {
	cases := []struct {
		value     string
		forbidden bool
	}{
		{value: "created", forbidden: false},
		{value: "device_id", forbidden: true},
		{value: "command-id", forbidden: true},
		{value: "global", forbidden: false},
	}
	for _, testCase := range cases {
		if got := LabelValueLooksUnbounded(testCase.value); got != testCase.forbidden {
			t.Fatalf("LabelValueLooksUnbounded(%q) = %v, want %v", testCase.value, got, testCase.forbidden)
		}
	}
}

func TestSetQueueDepthRejectsUnknownState(t *testing.T) {
	if err := SetQueueDepth("ACKNOWLEDGED", 1); err == nil {
		t.Fatal("SetQueueDepth() unexpectedly accepted terminal state")
	}
}
