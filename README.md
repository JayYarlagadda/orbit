# Orbit

Orbit is a durable command-delivery system for intermittently connected
edge devices. It is designed to demonstrate distributed-systems correctness,
failure recovery, deterministic fault replay, and measured performance rather
than simply assembling a large infrastructure stack.

Status: M0–M7 are complete locally. The durable command API, delivery path,
failover scenarios, observability stack, B0 benchmark harness, and release
verification are implemented and verified. See [current status](docs/current-status.md)
and [release evidence](docs/release.md) for exact claims and artifacts.

## Project thesis

An operator submits commands for a device that may be offline, slow, or moving
between gateways. Orbit durably stores those commands and eventually delivers
them with explicit semantics:

- at-least-once transport;
- idempotent acknowledgement and client-side deduplication;
- strict ordering per device, with no global ordering claim;
- lease-based worker ownership protected by fencing tokens;
- bounded resource use and explicit overload behavior;
- deterministic reproduction of failures and automated invariant checks.

The differentiator is the verification loop: a deterministic C++20 fault
engine creates reproducible scenarios, the same scenarios exercise the real Go
services, and a history checker verifies the documented guarantees.

## Planning documents

- [Project brief](docs/project-brief.md): resume fit, scope, users, goals,
  non-goals, and portfolio success criteria.
- [System design](docs/system-design.md): components, data model, protocol,
  state machines, failure behavior, and key decisions.
- [Implementation plan](docs/implementation-plan.md): ordered work packages,
  phase gates, deliverables, and definition of done.
- [Verification and benchmarks](docs/verification-and-benchmarks.md): test
  strategy, invariant catalog, fault scenarios, benchmark method, and reporting
  rules.
- [Toolchain](docs/toolchain.md): pinned versions, local bootstrap, and
  verification commands.
- [API and configuration](docs/api-and-configuration.md): gRPC operations,
  validation limits, environment variables, and local command workflow.
- [Current status](docs/current-status.md): implemented surfaces, verification
  evidence, known gaps, local environment state, and the next safe work item.
- [Release evidence](docs/release.md): claim-to-evidence table and benchmark
  references.
- [Operations guide](docs/operations.md): triage failures from metrics, traces,
  and scenario history.

## Stack

- Go for the control plane, gateways, scheduler, and reference client
- gRPC and Protobuf for service and device protocols
- PostgreSQL for authoritative command, acknowledgement, and lease state
- C++20 for deterministic event simulation and scenario generation
- OpenTelemetry, Prometheus, and Grafana for traces and metrics
- Docker Compose for the primary reproducible environment
- Kubernetes and Linux `tc netem` only after the core system is proven

## Scope rule

Orbit is not a Kafka clone, a custom database, or a multi-region consensus
system. A feature belongs in the first release only if it proves one of the
documented delivery guarantees or measures the system under failure.

## Development

On Windows, install the checksum-pinned local toolchain and run the foundation
verification suite:

```powershell
./scripts/bootstrap-tools.ps1
./scripts/verify.ps1
```

The bootstrap installs tools outside the repository and does not modify the
machine-wide `PATH`. PostgreSQL requires Docker separately; see the toolchain
document for the Compose command.

Once PostgreSQL is running, apply migrations and start the control plane:

```powershell
$env:ORBIT_DATABASE_URL = 'postgres://orbit:orbit-local-only@127.0.0.1:5432/orbit?sslmode=disable'
go run ./cmd/orbit-migrate -direction up
go run ./cmd/orbitd
```

From another terminal, submit and inspect a command:

```powershell
go run ./cmd/orbitctl submit -producer demo -idempotency-key request-1 `
  -device edge-1 -priority 4 -payload collect-diagnostics -expires-after 1h
go run ./cmd/orbitctl get -command-id <command UUID from the submit response>
go run ./cmd/orbitctl cancel -command-id <command UUID from the submit response>
```

To run the whole delivery path as separate processes and assert the durable
`ACKNOWLEDGED` result, use the online smoke script:

```powershell
./scripts/smoke-online.ps1
```

Release verification and portfolio demo:

```powershell
./scripts/verify-release.ps1   # foundation + benchmark artifact checks
./scripts/demo-release.ps1       # smoke + online-smoke scenario
./scripts/benchmark-b0.ps1       # regenerate B0 results (long-running)
```
