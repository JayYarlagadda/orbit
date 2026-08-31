# Orbit benchmark matrix

Pinned configurations for the verification matrix in
[`docs/verification-and-benchmarks.md`](../docs/verification-and-benchmarks.md) §8.

## Harness benchmarks (`orbit-bench`)

In-process device sessions against a live `orbitd` + gateway. Use
`scripts/benchmark-b0.ps1` as a template (pass `-ConfigPath`).

| Matrix | Config | Smoke config | Purpose |
|--------|--------|--------------|---------|
| B0 | `b0-harness-calibration.v1.json` | `b0-harness-calibration-smoke.v1.json` | Harness calibration |
| B1 | `b1-healthy-baseline.v1.json` | `b1-healthy-baseline-smoke.v1.json` | Healthy saturation curve |

Published B0 results: [`docs/results/b0-harness-calibration/`](../docs/results/b0-harness-calibration/).

Published B1 smoke results: [`docs/results/b1-healthy-baseline/`](../docs/results/b1-healthy-baseline/) (23 ack/s median on reference host).

## Scenario benchmarks (`scenario-run`)

Fault and recovery matrices exercise real processes through the scenario runner.
Entries are listed in [`matrix.json`](matrix.json).

| Matrix | Scenario | Notes |
|--------|----------|-------|
| B2 | `offline-reconnect.v1.json` | Logical latency/loss via schedule |
| B3 | `dual-gateway-session.v1.json` | Gateway crash and recovery |
| B4 | `offline-reconnect.v1.json` | Mass disconnect playbook (extend schedule) |
| B5 | — | Admission limits; use integration tests + future harness |
| B6 | `dual-gateway-session.v1.json` | Reconnect churn across gateways |

## Validation

```powershell
./scripts/benchmark-check-all.ps1
```

## Linux `tc netem`

See [`docs/stretch/netem-validation.md`](../docs/stretch/netem-validation.md) for
comparing logical simulator faults with kernel-level delay and loss.
