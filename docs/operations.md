# Orbit operations guide

This guide explains how to triage command-delivery failures using Prometheus
metrics, OpenTelemetry traces, and scenario `history.json` artifacts — without
reading raw process logs.

## Local observability stack

Start the backing services from the repository root:

```powershell
cd deployments/compose
docker compose up -d
```

| Service | URL | Purpose |
|---------|-----|---------|
| Prometheus | http://localhost:9091 | Scrapes orbitd (`:9090`), gateway (`:9092`), client (`:9093`) on the host |
| Grafana | http://localhost:3000 | Dashboard **Orbit / Orbit overview** (admin / `orbit-local-only`) |
| Jaeger | http://localhost:16686 | Trace UI for OTLP export on `:4318` |

Run orbit binaries on the host with `.env` (see `.env.example`). Defaults:

- `ORBIT_METRICS_ADDRESS` — per process (`9090` orbitd, `9092` gateway, `9093` client)
- `ORBIT_OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318`
- `ORBIT_OTEL_ENABLED=false` disables trace export

On graceful shutdown, orbitd drains gRPC, flushes the metrics HTTP server, and
shuts down the OTLP trace exporter within five seconds.

## Metric contract

All metrics use the `orbit_` prefix and **bounded labels** only. Device IDs,
command IDs, and raw error strings never appear as label values. See
`internal/metrics` and `docs/system-design.md` §11.

| Metric | Labels | Meaning |
|--------|--------|---------|
| `orbit_commands_submitted_total` | `result` | Submit outcomes (`created`, `idempotent`) |
| `orbit_commands_admission_rejected_total` | `reason` | `global` or `per_device` admission cap |
| `orbit_commands_leased_total` | — | Commands leased to a gateway |
| `orbit_commands_acknowledged_total` | — | Terminal successful deliveries |
| `orbit_queue_depth` | `state` | Outstanding commands (`QUEUED`, `RETRY_WAIT`, `LEASED`, `IN_FLIGHT`) |
| `orbit_command_delivery_duration_seconds` | — | Create-to-ack latency histogram |
| `orbit_lease_expirations_total` | `outcome` | `retry_wait` or `dead_letter` after lease sweep |
| `orbit_stale_lease_rejections_total` | `operation` | Stale `in_flight` or `acknowledge` |
| `orbit_gateway_control_reconnects_total` | — | Gateway control-plane reconnects |
| `orbit_client_reconnects_total` | `reason` | `session_end` or `failover` |
| `orbit_gateway_control_streams_active` | — | Open control streams on orbitd |
| `orbit_gateway_device_sessions_active` | — | Device streams on a gateway process |
| `orbit_client_sessions_active` | — | `0` or `1` on the reference client |

## Trace spans

Spans are named `orbit.<area>.<action>` and carry command or correlation IDs as
**attributes** (not metric labels):

| Span | Component | Typical parent |
|------|-----------|----------------|
| `orbit.command.submit` | orbitd API | — |
| `orbit.scheduler.cycle` | orbitd scheduler | — |
| `orbit.command.lease` | orbitd scheduler | scheduler cycle |
| `orbit.command.deliver` | orbitd gateway control | — |
| `orbit.command.ack` | orbitd gateway control | deliver |
| `orbit.gateway.control.stream` | gateway | — |
| `orbit.gateway.control.reconnect` | gateway | stream |
| `orbit.client.session` | client | — |

Search Jaeger by `orbit.command.id` or `orbit.correlation.id` to follow a single
command from submit through lease, delivery, and acknowledgement across retries.

## Failure triage workflow

1. **Confirm the scenario failed** — `go run ./cmd/scenario-run -scenario <path>` writes `history.json` and `checker-report.json` under the work directory.
2. **Open Grafana** — check the scenario time window for anomalies (queue buildup, lease expirations, reconnect spikes, admission rejects).
3. **Open Jaeger** — filter by service (`orbitd`, `orbit-gateway`, `orbit-client`) and look for error spans or long `orbit.command.lease` → `orbit.command.ack` gaps.
4. **Read `history.json`** — audit events carry `correlation_id`, state transitions, and `lease_token` for authoritative ordering evidence.
5. **Cross-check invariants** — `checker-report.json` lists which INV-* rules passed or failed.

## Worked example: `gateway-crash-after-send`

Scenario: `scenarios/examples/gateway-crash-after-send.v1.json` — gateway-a
crashes 500ms after start while a command is in flight; gateway-b recovers
delivery.

### What you should see in Grafana

- **Command throughput** — a brief gap in `leased`/`acknowledged` rates when
  gateway-a dies, then recovery on gateway-b.
- **Queue depth** — `LEASED` or `IN_FLIGHT` may spike briefly, then return to
  zero after ack.
- **Lease expiry** — usually zero for this scenario (recovery happens before
  lease sweep); non-zero `retry_wait` would indicate slower recovery.
- **Reconnect** — `orbit_gateway_control_reconnects_total` increases on the
  surviving gateway; `orbit_client_reconnects_total{reason="failover"}` may tick
  if the client rotates gateways.
- **Stale-token rejects** — should stay flat unless a zombie worker races the
  new lease token.

### What you should see in Jaeger

1. `orbit.command.submit` on **orbitd** with `orbit.correlation.id` from
   orbitctl metadata.
2. `orbit.command.lease` then `orbit.command.deliver` on **orbitd** for the
   command ID.
3. A gap while **orbit-gateway** shows `orbit.gateway.control.stream` ending and
   `orbit.gateway.control.reconnect` on the replacement stream.
4. A second `orbit.command.lease` / `orbit.command.deliver` pair if the first
   attempt did not ack before crash.
5. `orbit.command.ack` closing the trace.

### What you should see in `history.json`

- Audit trail: `QUEUED` → `LEASED` → `IN_FLIGHT` → `ACKNOWLEDGED` (possibly
  with an intermediate `LEASED`/`IN_FLIGHT` if the first attempt was abandoned).
- `delivery_attempts` — two rows with the same `command_id` but different
  `lease_token` values if duplicate delivery occurred; only one should end with
  `outcome: "ACKNOWLEDGED"`.
- `client_applications` — one application record (dedup preserved one apply).
- Checker report — INV-01/02/03/05/08/09 pass for the committed scenario.

### Decision table

| Signal | Likely cause |
|--------|----------------|
| `stale_lease_rejections` ↑ after crash | Old gateway instance still reporting in-flight or ack |
| `lease_expirations{outcome="retry_wait"}` ↑, no ack | Recovery slower than lease duration |
| `admission_rejected` ↑ | Producer overload, not gateway crash |
| Queue depth stuck in `RETRY_WAIT` | Scheduler not running (no gateway connected) |
| History shows two ACKs | Invariant violation — investigate checker report |

## Running the observability gate locally

```powershell
cd d:\creqate\orbit\deployments\compose
docker compose up -d

# separate terminals: orbitd, gateway(s), client — or use scenario-run
go run ./cmd/scenario-run -scenario scenarios/examples/gateway-crash-after-send.v1.json

# metrics smoke
curl http://127.0.0.1:9090/metrics | findstr orbit_commands

# traces: open http://localhost:16686 and search orbitd / orbit-gateway
# dashboards: http://localhost:3000 → Orbit overview
```

The scenario work directory contains `history.json` for the audit-side story;
metrics and traces explain timing and recovery without log archaeology.
