# B1 healthy baseline (smoke) results

## Purpose

Matrix **B1** establishes a healthy-network saturation curve without injected
faults. This artifact is the **smoke** configuration for fast CI/dev validation;
the pinned reference config is `benchmarks/b1-healthy-baseline.v1.json`.

## Smoke configuration

- 20 in-process device sessions
- 500 commands per measured trial (50-command warmup)
- 256-byte payload, priority 4, single gateway
- 3 measured trials, 16-way submit concurrency

## Published smoke run (2026-08-31)

| Metric | Value |
|--------|-------|
| Median throughput | **23.0 ack/s** |
| Median p99 latency | **~21.7 s** |
| Host | 8-core Windows, PostgreSQL 18.6 via WSL Docker |

## Regenerate

```powershell
./scripts/benchmark-b0.ps1 `
  -ConfigPath benchmarks/b1-healthy-baseline-smoke.v1.json `
  -OutputPath docs/results/b1-healthy-baseline/summary.json
```

For the full B1 pin (100 clients × 2,000 commands), swap in
`benchmarks/b1-healthy-baseline.v1.json`.
