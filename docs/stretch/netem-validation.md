# Linux `tc netem` validation (stretch)

Orbit's C++ simulator and Go scenario runner inject **logical** transport faults
(delay, loss, duplication) at the gateway device stream. This guide describes how
to compare those results with **kernel-level** `tc netem` on Linux loopback.

## Goal

Build confidence that logical fault schedules are directionally consistent with
real network impairment — not to claim bit-identical timing.

## Prerequisites

- Linux host with `ip`, `tc`, and root or `CAP_NET_ADMIN`
- Orbit built from a clean commit (`scripts/verify-release.ps1`)
- PostgreSQL reachable (Compose or native)

## Baseline without netem

1. Start `orbitd`, gateway, and client against PostgreSQL.
2. Run `scenario-run` with `scenarios/examples/online-smoke.v1.json`.
3. Record `history.json` latency and `checker-report.json`.

## Apply netem on loopback

```bash
# 500 ms one-way delay + 5% loss (approximate 1 s RTT with jitter omitted)
sudo tc qdisc add dev lo root netem delay 500ms loss 5%

# Run the same scenario (or a ping/iperf sanity check first)
go run ./cmd/scenario-run \
  -database-url "$ORBIT_DATABASE_URL" \
  -scenario scenarios/examples/online-smoke.v1.json \
  -work-dir /tmp/orbit-netem-smoke

sudo tc qdisc del dev lo root
```

Compare tail latency and retry counts against the logical-fault scenario
(`offline-reconnect.v1.json` uses 25 ms latency and 0% loss in its profile).

## Automated helper

On Linux/WSL with `tc` available:

```bash
./scripts/validate-netem.sh scenarios/examples/online-smoke.v1.json
```

The script applies a documented netem profile, runs `scenario-check` on the
scenario fixture, and prints reminders for manual `scenario-run` comparison. It
does not modify Orbit binaries.

## Interpretation

| Observation | Meaning |
|-------------|---------|
| Logical schedule reproduces ordering | Simulator + runner contract holds |
| Netem increases p99 more than logical-only | Expected; kernel queues add jitter |
| Large divergence in duplicate/ACK counts | Investigate loss injection boundary |

Report findings under `docs/results/netem/` when publishing comparisons.
