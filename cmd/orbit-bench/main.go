package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/JayYarlagadda/orbit/internal/benchmark"
)

func main() {
	configPath := flag.String("config", "", "path to benchmark configuration JSON")
	outputPath := flag.String("output", "", "path to write benchmark summary JSON")
	stateRoot := flag.String("state-root", "", "directory for per-device client state files")
	flag.Parse()

	if *configPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "usage: orbit-bench -config <path> -output <path> [-state-root <dir>]")
		os.Exit(2)
	}

	config, err := benchmark.LoadConfig(*configPath)
	if err != nil {
		exitError(err)
	}
	if err := config.Validate(); err != nil {
		exitError(err)
	}

	root := *stateRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "orbit-bench-state")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	summary, err := (&benchmark.Runner{Config: config, StateRoot: root}).Run(ctx)
	if err != nil {
		exitError(err)
	}
	if err := benchmark.WriteSummary(*outputPath, summary); err != nil {
		exitError(err)
	}
	fmt.Printf(
		"benchmark %q complete: %d trials, median throughput %.2f ack/s, median p99 %.3fs\n",
		summary.BenchmarkName,
		len(summary.Trials),
		summary.Aggregate.ThroughputAckPerSecond.Median,
		summary.Aggregate.LatencySeconds.P99.Median,
	)
}

func exitError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
