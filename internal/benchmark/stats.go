package benchmark

import "sort"

type LatencySummary struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

type StatsSummary struct {
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

type LatencyAggregate struct {
	P50 StatsSummary `json:"p50"`
	P95 StatsSummary `json:"p95"`
	P99 StatsSummary `json:"p99"`
}

type TrialResult struct {
	Trial                  int            `json:"trial"`
	CommandsSubmitted      int            `json:"commands_submitted"`
	CommandsAcknowledged   int            `json:"commands_acknowledged"`
	AdmissionRejected      int            `json:"admission_rejected,omitempty"`
	DurationSeconds        float64        `json:"duration_seconds"`
	ThroughputAckPerSecond float64        `json:"throughput_ack_per_second"`
	LatencySeconds         LatencySummary `json:"latency_seconds"`
}

type AggregateResult struct {
	ThroughputAckPerSecond StatsSummary     `json:"throughput_ack_per_second"`
	LatencySeconds         LatencyAggregate `json:"latency_seconds"`
}

type Summary struct {
	SchemaVersion   string          `json:"schema_version"`
	BenchmarkName   string          `json:"benchmark_name"`
	MatrixID        string          `json:"matrix_id"`
	GitCommit       string          `json:"git_commit"`
	GitDirty        bool            `json:"git_dirty"`
	GoVersion       string          `json:"go_version"`
	Host            HostInfo        `json:"host"`
	StartedAt       string          `json:"started_at"`
	FinishedAt      string          `json:"finished_at"`
	Configuration   Config          `json:"configuration"`
	Trials          []TrialResult   `json:"trials"`
	Aggregate       AggregateResult `json:"aggregate"`
	BottleneckNotes string          `json:"bottleneck_notes,omitempty"`
}

func SummarizeLatencies(samples []float64) LatencySummary {
	if len(samples) == 0 {
		return LatencySummary{}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	return LatencySummary{
		P50: percentile(sorted, 0.50),
		P95: percentile(sorted, 0.95),
		P99: percentile(sorted, 0.99),
		Max: sorted[len(sorted)-1],
	}
}

func AggregateTrials(trials []TrialResult) AggregateResult {
	throughputs := make([]float64, 0, len(trials))
	p50 := make([]float64, 0, len(trials))
	p95 := make([]float64, 0, len(trials))
	p99 := make([]float64, 0, len(trials))
	for _, trial := range trials {
		throughputs = append(throughputs, trial.ThroughputAckPerSecond)
		p50 = append(p50, trial.LatencySeconds.P50)
		p95 = append(p95, trial.LatencySeconds.P95)
		p99 = append(p99, trial.LatencySeconds.P99)
	}
	return AggregateResult{
		ThroughputAckPerSecond: summarizeValues(throughputs),
		LatencySeconds: LatencyAggregate{
			P50: summarizeValues(p50),
			P95: summarizeValues(p95),
			P99: summarizeValues(p99),
		},
	}
}

func percentile(sorted []float64, quantile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if quantile <= 0 {
		return sorted[0]
	}
	if quantile >= 1 {
		return sorted[len(sorted)-1]
	}
	position := quantile * float64(len(sorted)-1)
	lower := int(position)
	upper := lower + 1
	if upper >= len(sorted) {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func summarizeValues(values []float64) StatsSummary {
	if len(values) == 0 {
		return StatsSummary{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return StatsSummary{
		Median: percentile(sorted, 0.50),
		Min:    sorted[0],
		Max:    sorted[len(sorted)-1],
	}
}
