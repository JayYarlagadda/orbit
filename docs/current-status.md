# Current Status

Last reconciled: 2026-08-31.

This document is the implementation handoff ledger. The project brief describes
why Orbit is worth building, the system design describes intended behavior, and
this file records what actually exists and what has been verified.

## Milestone state

| Milestone | State | Evidence |
|---|---|---|
| M0 build | Complete locally | Go/C++ build, scenario fixtures, generated-code check, reversible PostgreSQL migration |
| M1 durable API | Complete locally | Real gRPC submit/get/cancel against PostgreSQL 18.6; race-enabled repository suite |
| M2 first delivery | Complete locally | Producer to device to durable `ACKNOWLEDGED` across separate `orbitd`, gateway, and client processes |
| M3 recovery | Complete locally | Retry/backoff, dead letter, admission limits, gateway-control reconnect, lease/TTL recovery, `orbitd` graceful shutdown test, and online smoke with mid-run `orbitd` restart (CI + `scripts/smoke-online.ps1`) |
| M4 replay | Complete locally | SplitMix64 schedule compiler, gateway logical fault injection, scenario runner, history collector/checker, golden schedule fixture, and online-smoke closed-loop run |
| M5 failover | Complete locally | Dual-gateway deployment, client gateway selection/failover, gateway-crash scenario playbooks, INV-05/INV-08 history checks, and failure artifacts |
| M6 observability | Complete locally | Prometheus metrics on orbitd/gateway/client, OTLP traces, Compose Prometheus/Grafana/Jaeger, bounded-label tests, and `docs/operations.md` triage guide |
| M7 release | Complete locally | B0 benchmark harness, committed `summary.json`, `docs/release.md`, `verify-release` |

### Embedded expansion (future — frozen core at `v1.0-distributed-runtime`)

| Milestone | State | Evidence |
|---|---|---|
| E0 core freeze | Complete | Git tag `v1.0-distributed-runtime`; `embedded/` + plan docs; M0–M7 unchanged |
| E1 bring-up & acquisition | In progress | Zephyr west workspace, LSM6DSO SPI driver, E1 app scaffold on `feature/embedded-endpoint` |
| E2 DMA & RTOS structure | Not started | DMA SPI; bounded static queues; jitter/CPU vs polling |
| E3 persistent offline buffer | Not started | Flash circular log; reboot recovery |
| E4 transport & edge gateway | Not started | Embedded protocol; Linux gateway → `orbitd` |
| E5 reconnect & replay | Not started | Ordered replay; idempotency; backpressure on MCU |
| E6 watchdog & health | Not started | Supervisor; persisted reboot reason |
| E7 HIL automation | Not started | Python harness; scripted faults; reports |
| E8 stress & metrics | Not started | Long-run benchmarks; `docs/results/embedded/` |
| E9 stretch (optional) | Not started | CAN FD; MCUboot OTA rollback |

Plan: [embedded-expansion-plan.md](embedded-expansion-plan.md).

## Implemented

- Versioned scenario JSON contract with strict Go validation and valid/invalid
  fixtures.
- C++20 deterministic event queue ordered by timestamp then insertion ordinal.
- SplitMix64 PRNG (`splitmix64-v1`) and a canonical schedule compiler shared
  between Go (`internal/scenario/schedule.go`) and the C++ fault engine.
- Gateway logical transport fault injection driven by compiled schedules.
- Go scenario runner (`internal/scenariorunner`) and `scenario-run` CLI that
  deploy multiple gateways, apply lifecycle events, collect PostgreSQL audit
  history, run the invariant checker, and write failure artifacts.
- Client gateway failover via ordered `ORBIT_CLIENT_GATEWAY_ADDRESSES` with
  round-robin reconnect and scenario-driven `device_gateway_switch` events.
- Pinned Windows bootstrap for Go, CMake, Protobuf, generators, GCC, and Ninja.
- Protobuf contracts for command, device, and gateway-control services with
  committed generated Go bindings and drift verification.
- Explicit command state machine, bounded validation, canonical idempotency
  request hashing, and typed domain errors.
- PostgreSQL schema for device cursors, commands, delivery attempts, audit
  events, session ownership, fencing tokens, and acknowledgement evidence.
- Advisory-locked transactional migration runner with one-step rollback.
- Transactional submit, get, cancel, ordered lease, in-flight, lease-expiry,
  terminal command-expiry, and acknowledgement repository operations.
- Capped exponential retry scheduling with full jitter, retry-budget dead
  lettering, and durable global/per-device admission limits on submit.
- Terminal TTL expiration sweep that moves `QUEUED` and `RETRY_WAIT` commands
  past `expires_at` to `EXPIRED`, advances the terminal cursor, and runs in the
  scheduler cycle between the lease sweep and leasing.
- Producer idempotency, concurrent per-device sequence allocation, contiguous
  terminal cursor advancement, monotonic lease tokens, and session epochs.
- `orbitd` with command and gateway-control gRPC services, health service,
  bounded message sizes, structured logs, and deadline-based graceful stop.
- `orbitctl` submit/get/cancel client with deadlines, correlation metadata,
  binary payload-file support, and protobuf JSON output.
- Bounded scheduler cycle with injectable ticker/correlation factory.
- Standalone gateway process, bounded control/per-device queues, serialized
  stream writers, device session routing, delivery/ACK forwarding, and a
  capped jittered control-stream reconnect loop. Device sessions are dropped
  on rebind so they re-register for fresh epochs after an `orbitd` restart.
- Reference-client executable with a capped, jittered reconnect loop, bounded
  consecutive-failure limit, durable dedup state, and signal-based shutdown.
- Reference-client state library with payload-hash validation, atomic Windows
  file replacement, bounded retained command IDs, persist-before-ACK behavior,
  duplicate suppression, and device stream session logic.
- Bounded Prometheus metrics (`internal/metrics`) on orbitd, gateway, and client
  with queue depth, delivery latency, lease expiry, stale-token rejects,
  admission limits, session gauges, and reconnect counters.
- OpenTelemetry traces (`internal/telemetry`) with correlation across submit,
  lease, deliver, ack, gateway control reconnect, and client session spans.
- Docker Compose stack with PostgreSQL, Prometheus, Grafana (provisioned
  **Orbit overview** dashboard), and Jaeger OTLP collector.
- Operations guide (`docs/operations.md`) for triaging failures from metrics,
  traces, and `history.json`.
- History checker invariants INV-01–INV-05, INV-07–INV-09, and INV-11 on
  collected scenario histories (INV-04 post-expiry delivery, INV-07 idempotency
  keys, INV-11 audit-chain continuity).

## Verified locally

The following passed on this workstation:

- `scripts/verify.ps1`: Go vet/tests/build, generated Protobuf drift, scenario
  accept/reject contract, C++ configure/build/test, and repository text checks.
- Go race tests for command, API, configuration, scheduler, session, migration,
  and PostgreSQL packages.
- PostgreSQL 18.6 migration up, migration down, and migration up again.
- Matching idempotent submit and conflicting idempotency rejection.
- Sixteen concurrent submissions producing unique contiguous device sequences.
- Audit-insert failure rolling back both command and cursor creation.
- Earliest-sequence-only leasing across devices.
- Stale lease token, stale session epoch, and mismatched newer epoch rejection.
- Expired lease recovery to `RETRY_WAIT` with scheduled `next_attempt_at`,
  closed attempt outcome, and re-lease with token 2.
- Fenced acknowledgement, duplicate acknowledgement, terminal cursor advance,
  and successor eligibility.
- Retry-budget exhaustion moving a command to `DEAD_LETTER` after repeated
  lease expiry.
- Per-device and global admission rejection without affecting unrelated devices.
- Generated gRPC client submit/get/cancel workflow backed by the real store.
- External `orbitd` plus `orbitctl` submit/get/cancel workflow.
- An expired command no longer blocking its device queue: leasing returns
  nothing while the expired predecessor is present, the expiry sweep moves it to
  `EXPIRED` and advances the cursor, and the successor then leases.
- Migration `000002` applying, rolling back, and reapplying its indexes.
- Gateway hub registration, pre-session frame replay, backlog rejection,
  session fencing, unregister routing, control-stream reconnect, hub rebind,
  and `DeviceService.Connect` forwarding, rejection, rebind teardown, and
  goroutine-count checks under the race detector. A bounded reconnect soak
  (25 control drop/reconnect cycles plus 30 device connect attempts) stayed
  within 24 goroutines and 16 MiB heap of the post-warmup snapshot.
- Built `gateway` and `client` executables interrupted while a control or
  device stream was held open: both became healthy (or persisted session
  state), then exited 0 within the drain deadline.
- Built `orbitd` executable interrupted against real PostgreSQL: health check
  passed, then the process exited 0 within the drain deadline.
- Built `orbitd`, gateway, and client against PostgreSQL: two commands reached
  durable `ACKNOWLEDGED`, including one submitted after a forced `orbitd`
  restart while the gateway stayed up.
- Built gateway plus client receiving the same command twice: two durable ACKs,
  one `applying command` log line, `last_seen_sequence` 1. Built client after
  a dropped ACK reconnecting and acknowledging without a second apply.
- Duplicate device-stream delivery invoking the client handler once and
  producing matching ACKs, plus persist-before-ACK surviving a failed ACK send.
- `scripts/smoke-online.ps1`: built `orbitd`, gateway, and client running as
  three separate processes, one `orbitctl` submission, and the command reaching
  durable `ACKNOWLEDGED`. The recorded evidence was `attempt_count` 1,
  `lease_token` 1, delivery-attempt outcome `ACKNOWLEDGED`, a persisted
  `result_hash` equal to the device handler's output, `last_terminal_sequence`
  advanced to 1, and an audit chain of
  `QUEUED -> LEASED -> IN_FLIGHT -> ACKNOWLEDGED`. All three processes wrote
  empty standard-error logs.
- Go schedule compiler golden test for `offline-reconnect.v1.json`.

The PostgreSQL image digest used locally was
`sha256:1c59e2c3c818eaa0f0628f695b36e7c9e362d6b219b36a54a32df645cbd7e1af`.
It ran in the existing Ubuntu WSL Docker Engine because Docker is not exposed in
the Windows shell. A WSL keepalive process is required on this host or the
distro cleanly stops its containers when idle.

The Compose stack was started with `docker compose up -d` from
`deployments/compose` and reached a healthy state. Earlier revisions mounted the
data volume at `/var/lib/postgresql/data`, which PostgreSQL 18 images reject,
leaving the container in a restart loop; the mount is now `/var/lib/postgresql`.

## Current boundary

M5 failover is closed in code and CI: the scenario runner deploys every gateway
in a scenario topology, the client rotates through gateway addresses on reconnect,
and dual-gateway plus gateway-crash playbooks pass the expanded history checker.

M6 observability is closed locally: all three processes export `/metrics`,
OTLP traces land in Jaeger, Grafana panels cover the M6 contract, and
`docs/operations.md` documents the gateway-crash triage gate.

M7 release evidence is closed locally: the B0 harness is pinned, results are
committed under `docs/results/`, and `scripts/verify-release.ps1` validates the
artifact contract.

Post-release polish extended the history checker with INV-04, INV-07, and
INV-11. INV-06 (submit truthfulness) and INV-10 (bounded memory) remain
integration/process-test evidence; INV-12 is covered by telemetry redaction tests.

Portfolio reviewers can clone the repository and follow `scripts/demo-release.ps1`.

## Open work

- **Embedded E1:** West workspace + LSM6DSO driver + E1 app on
  `feature/embedded-endpoint` — flash on Nucleo-H743ZI and record
  `benchmarks/realtime/` metrics. See [embedded/README.md](../embedded/README.md).
- **Embedded (E2+):** DMA path, flash buffer, transport per
  [embedded-expansion-plan.md](embedded-expansion-plan.md). Distributed core frozen
  at tag `v1.0-distributed-runtime`.
- Extend harness benchmarks B1 full pin and B2–B6 results (`benchmarks/matrix.json`).
- Optional stretch: Kubernetes (`deployments/k8s/`), `tc netem`
  (`docs/stretch/netem-validation.md`).

## Known limitations

- Local transport is plaintext and there is no producer/device authentication.
- Command TTL is enforced at submission, at lease selection, at dispatch, and by
  the terminal expiration sweep, but expiry is only observed when a scheduler
  cycle runs, so a device with no connected gateway keeps its expired commands
  until one does.
- No Kubernetes deployment exists.
- Remote CI should be confirmed green on GitHub after the latest push.

## Safe resume sequence

1. Run `scripts/verify.ps1`.
2. Start or verify PostgreSQL 18.6 and run the race-enabled storage and
   processtest suites with `ORBIT_TEST_DATABASE_URL`.
3. Run `scripts/smoke-online.ps1` when validating the full Windows process path.
4. Build the C++ simulator (`cmake --preset default && ctest --preset default`)
   and confirm the golden schedule test passes.
5. Run `go run ./cmd/scenario-run -scenario scenarios/examples/online-smoke.v1.json`
   with `ORBIT_DATABASE_URL` set to execute a closed-loop scenario.
6. Start `deployments/compose` (Prometheus, Grafana, Jaeger) and follow
   `docs/operations.md` to validate metrics and traces during a scenario run.
7. For embedded E1: `./scripts/bootstrap-embedded.ps1` then
   `./scripts/build-embedded-e1.ps1 -Flash` (requires Zephyr SDK + west).
