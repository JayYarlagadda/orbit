package metrics

import (
	"fmt"
	"regexp"
	"slices"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	SubmittedResultCreated    = "created"
	SubmittedResultIdempotent = "idempotent"

	AdmissionReasonGlobal    = "global"
	AdmissionReasonPerDevice = "per_device"

	LeaseExpiryOutcomeRetryWait  = "retry_wait"
	LeaseExpiryOutcomeDeadLetter = "dead_letter"

	StaleLeaseOperationInFlight    = "in_flight"
	StaleLeaseOperationAcknowledge = "acknowledge"

	GatewayReconnectReasonStreamEnd = "stream_end"
	ClientReconnectReasonSessionEnd = "session_end"
	ClientReconnectReasonFailover   = "failover"
)

var forbiddenLabelPattern = regexp.MustCompile(`(?i)(device|command)[-_]?id`)

var (
	commandsSubmitted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "commands_submitted_total",
			Help:      "Commands accepted through Submit, by outcome.",
		},
		[]string{"result"},
	)
	commandsAdmissionRejected = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "commands_admission_rejected_total",
			Help:      "Commands rejected by admission limits.",
		},
		[]string{"reason"},
	)
	commandsLeased = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "commands_leased_total",
			Help:      "Commands leased to a gateway for delivery.",
		},
	)
	commandsAcknowledged = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "commands_acknowledged_total",
			Help:      "Commands acknowledged by a device.",
		},
	)
	commandsExpired = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "commands_expired_total",
			Help:      "Commands moved to EXPIRED by the TTL sweeper.",
		},
	)
	leaseExpirations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "lease_expirations_total",
			Help:      "Expired leases swept by the scheduler.",
		},
		[]string{"outcome"},
	)
	staleLeaseRejections = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "stale_lease_rejections_total",
			Help:      "Rejected updates because the lease token or session was stale.",
		},
		[]string{"operation"},
	)
	commandDeliveryDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "orbit",
			Name:      "command_delivery_duration_seconds",
			Help:      "Time from command creation to acknowledgement.",
			Buckets:   prometheus.ExponentialBuckets(0.01, 2, 14),
		},
	)
	queueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "orbit",
			Name:      "queue_depth",
			Help:      "Outstanding commands by durable state.",
		},
		[]string{"state"},
	)
	gatewayControlStreams = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "orbit",
			Name:      "gateway_control_streams_active",
			Help:      "Active gateway control-plane streams.",
		},
	)
	gatewayControlReconnects = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "gateway_control_reconnects_total",
			Help:      "Gateway control-plane reconnect attempts.",
		},
	)
	gatewayDeviceSessions = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "orbit",
			Name:      "gateway_device_sessions_active",
			Help:      "Device sessions currently attached to this gateway process.",
		},
	)
	clientReconnects = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "orbit",
			Name:      "client_reconnects_total",
			Help:      "Device client reconnect attempts.",
		},
		[]string{"reason"},
	)
	clientSessions = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "orbit",
			Name:      "client_sessions_active",
			Help:      "Whether the device client currently has an open session (0 or 1).",
		},
	)
)

var queueDepthMu sync.Mutex

func init() {
	prometheus.MustRegister(
		commandsSubmitted,
		commandsAdmissionRejected,
		commandsLeased,
		commandsAcknowledged,
		commandsExpired,
		leaseExpirations,
		staleLeaseRejections,
		commandDeliveryDuration,
		queueDepth,
		gatewayControlStreams,
		gatewayControlReconnects,
		gatewayDeviceSessions,
		clientReconnects,
		clientSessions,
	)
}

func RecordCommandSubmitted(result string) error {
	return recordLabel(commandsSubmitted, "result", result, SubmittedResultCreated, SubmittedResultIdempotent)
}

func RecordAdmissionRejected(reason string) error {
	return recordLabel(commandsAdmissionRejected, "reason", reason, AdmissionReasonGlobal, AdmissionReasonPerDevice)
}

func RecordCommandLeased() {
	commandsLeased.Inc()
}

func RecordCommandAcknowledged() {
	commandsAcknowledged.Inc()
}

func RecordCommandExpired(count int) {
	if count > 0 {
		commandsExpired.Add(float64(count))
	}
}

func RecordLeaseExpiration(outcome string) error {
	return recordLabel(leaseExpirations, "outcome", outcome, LeaseExpiryOutcomeRetryWait, LeaseExpiryOutcomeDeadLetter)
}

func RecordStaleLeaseRejection(operation string) error {
	return recordLabel(staleLeaseRejections, "operation", operation, StaleLeaseOperationInFlight, StaleLeaseOperationAcknowledge)
}

func ObserveCommandDeliveryDuration(seconds float64) {
	if seconds < 0 {
		return
	}
	commandDeliveryDuration.Observe(seconds)
}

func SetQueueDepth(state string, depth int) error {
	if err := validateBoundedLabel("state", state, []string{
		"QUEUED", "RETRY_WAIT", "LEASED", "IN_FLIGHT",
	}); err != nil {
		return err
	}
	queueDepthMu.Lock()
	queueDepth.WithLabelValues(state).Set(float64(depth))
	queueDepthMu.Unlock()
	return nil
}

func ResetQueueDepth(states ...string) {
	queueDepthMu.Lock()
	for _, state := range states {
		queueDepth.WithLabelValues(state).Set(0)
	}
	queueDepthMu.Unlock()
}

func IncGatewayControlStreams() {
	gatewayControlStreams.Inc()
}

func DecGatewayControlStreams() {
	gatewayControlStreams.Dec()
}

func RecordGatewayControlReconnect() {
	gatewayControlReconnects.Inc()
}

func SetGatewayDeviceSessions(count int) {
	if count < 0 {
		count = 0
	}
	gatewayDeviceSessions.Set(float64(count))
}

func RecordClientReconnect(reason string) error {
	return recordLabel(clientReconnects, "reason", reason,
		ClientReconnectReasonSessionEnd,
		ClientReconnectReasonFailover,
	)
}

func SetClientSessionActive(active bool) {
	if active {
		clientSessions.Set(1)
		return
	}
	clientSessions.Set(0)
}

func recordLabel(counter *prometheus.CounterVec, labelName, value string, allowed ...string) error {
	if err := validateBoundedLabel(labelName, value, allowed); err != nil {
		return err
	}
	counter.WithLabelValues(value).Inc()
	return nil
}

func validateBoundedLabel(labelName, value string, allowed []string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("metrics: %s label %q is not in bounded set %v", labelName, value, allowed)
}

func Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		commandsSubmitted,
		commandsAdmissionRejected,
		commandsLeased,
		commandsAcknowledged,
		commandsExpired,
		leaseExpirations,
		staleLeaseRejections,
		commandDeliveryDuration,
		queueDepth,
		gatewayControlStreams,
		gatewayControlReconnects,
		gatewayDeviceSessions,
		clientReconnects,
		clientSessions,
	}
}

func LabelNames() [][]string {
	return [][]string{
		{"result"},
		{"reason"},
		{"outcome"},
		{"operation"},
		{"state"},
	}
}

func LabelValueLooksUnbounded(value string) bool {
	return forbiddenLabelPattern.MatchString(value)
}
