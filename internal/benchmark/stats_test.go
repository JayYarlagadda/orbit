package benchmark

import "testing"

func TestPercentile(t *testing.T) {
	samples := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(samples, 0.50); got != 5.5 {
		t.Fatalf("p50 = %v, want 5.5", got)
	}
	if got := percentile(samples, 0.95); got < 9.5 || got > 9.6 {
		t.Fatalf("p95 = %v, want ~9.55", got)
	}
}

func TestAggregateTrials(t *testing.T) {
	aggregate := AggregateTrials([]TrialResult{
		{ThroughputAckPerSecond: 100, LatencySeconds: LatencySummary{P50: 0.01, P95: 0.02, P99: 0.03}},
		{ThroughputAckPerSecond: 120, LatencySeconds: LatencySummary{P50: 0.015, P95: 0.025, P99: 0.035}},
	})
	if aggregate.ThroughputAckPerSecond.Median != 110 {
		t.Fatalf("throughput median = %v", aggregate.ThroughputAckPerSecond.Median)
	}
	if aggregate.LatencySeconds.P95.Median != 0.0225 {
		t.Fatalf("latency p95 median = %v", aggregate.LatencySeconds.P95.Median)
	}
}

func TestConfigValidate(t *testing.T) {
	config := Config{
		SchemaVersion:     "1",
		Name:              "test",
		Clients:           1,
		Commands:          1,
		Trials:            1,
		SubmitConcurrency: 1,
		ControlAddress:    "127.0.0.1:50051",
		GatewayAddress:    "127.0.0.1:50052",
		ProducerID:        "bench",
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
