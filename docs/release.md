# Orbit release evidence

This document links portfolio claims to automated tests and committed benchmark
artifacts. Performance numbers come only from pinned configurations under
`benchmarks/` and summarized results under `docs/results/`.

## Published benchmark: B0 harness calibration

| Field | Value |
|-------|-------|
| Matrix | B0 — harness calibration |
| Configuration | [`benchmarks/b0-harness-calibration.v1.json`](../benchmarks/b0-harness-calibration.v1.json) |
| Result artifact | [`docs/results/b0-harness-calibration/summary.json`](../results/b0-harness-calibration/summary.json) |
| Published throughput (median of 5 trials) | **10.9 ack/s** on 8-core Windows host (`orbit-bench`) |
| Published end-to-end p99 latency (median of trials) | **~457 s** at 50 clients × 5k commands (pinned config; spec target is 100×10k) |
| Methodology | [`docs/results/b0-harness-calibration/README.md`](../results/b0-harness-calibration/README.md) |
| Reproduce | `./scripts/benchmark-b0.ps1` (requires PostgreSQL and ~10–20 minutes) |

Latency is measured from successful durable submit to observed `ACKNOWLEDGED`
state via `GetCommand`. Throughput is acknowledged commands divided by the
measured trial window after warmup.

## Claim-to-evidence table

| Claim | Design basis | Automated evidence | Result artifact |
|-------|--------------|--------------------|-----------------|
| At-least-once transport | [Delivery semantics](decisions/0002-at-least-once-delivery.md) | `gateway-crash-after-send`, processtest duplicate ACK | scenario `history.json` |
| Duplicate-safe reference client | Client dedup in [system design](system-design.md) | INV-02 checker tests, processtest | `internal/history/checker_test.go` |
| Per-device ordering | Sequence eligibility rules | INV-03 integration tests, scheduler tests | PostgreSQL integration suite |
| Lease fencing | Token-conditional transitions | Stale lease integration tests (INV-05) | `commands_integration_test.go` |
| Gateway recovery | Session epoch design | `dual-gateway-session`, gateway-crash scenarios | scenario runner + history checker |
| Bounded overload | Admission catalog | Admission integration tests | `commands_integration_test.go` |
| Failures are diagnosable | Observability contract §11 | Metrics label tests, M6 dashboards | [`operations.md`](operations.md) |
| Measured throughput | [Benchmark method](verification-and-benchmarks.md) §7–9 | `orbit-bench` harness, `benchmark-check` | B0 `summary.json` |

## Known limitations

- Single-region, single PostgreSQL instance; no multi-region replication.
- Plaintext gRPC locally; no production authentication in release one.
- Benchmarks run on the documented host with Docker PostgreSQL; results vary with
  hardware, power mode, and background load.
- B0 uses in-process device sessions in `orbit-bench`, not separate client
  processes — comparable for harness calibration but not identical to N process
  deployment.
- Exact wall-clock scheduling is not guaranteed across machines; scenario seeds
  fix logical event order, not timing.

## Verification targets

```powershell
./scripts/verify.ps1          # foundation: unit, integration, build, scenarios
./scripts/verify-release.ps1  # adds benchmark config + committed result checks
./scripts/demo-release.ps1    # reviewer smoke + online-smoke scenario
./scripts/benchmark-b0.ps1    # regenerate B0 summary (long-running)
```

On Linux/macOS, `make verify-release` runs the same checks where Make is available.
