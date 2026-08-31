# B0 harness calibration results

## Purpose

Matrix **B0** validates the `orbit-bench` measurement harness and establishes a
baseline before larger B1–B6 runs. See
[`docs/verification-and-benchmarks.md`](../../verification-and-benchmarks.md) §8.

## Configuration

Pinned in [`benchmarks/b0-harness-calibration.v1.json`](../../benchmarks/b0-harness-calibration.v1.json)
on the reference host (8-core Windows, PostgreSQL 18.6 in Docker via WSL):

- 50 in-process device sessions (`bench-device-0000` … `0049`)
- 5,000 commands per measured trial (100-command warmup)
- 64-byte payload, priority 4, single gateway
- 5 measured trials, 32-way submit concurrency
- Release binaries (`-trimpath -ldflags '-s -w'`)

## Environment

Record the host used when regenerating `summary.json`:

- OS / CPU / RAM (from `summary.json` `host` and manual notes)
- PostgreSQL 18.6 via Docker Compose (`deployments/compose`)
- `ORBIT_OTEL_ENABLED=false` during benchmark to reduce exporter overhead
- Prometheus metrics remain enabled on orbitd and gateway

## Regenerate

```powershell
cd deployments/compose
docker compose up -d postgres

cd ../..
./scripts/benchmark-b0.ps1
```

Commit the updated `summary.json` only when configuration is unchanged and the
machine context is noted in the commit message.

## Interpretation

The first bottleneck under B0 is typically PostgreSQL fsync latency on submit
and acknowledge transactions. Compare trials using the aggregate median/p99
fields in `summary.json`, not a single best run.

The verification specification targets 100×10,000 for large-host studies; this
pinned B0 configuration is scaled for reproducibility on the reference host.
