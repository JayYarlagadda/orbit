# Current Status

Last reconciled: 2026-08-29.

This document is the implementation handoff ledger. The project brief describes
why Orbit is worth building, the system design describes intended behavior, and
this file records what actually exists and what has been verified.

## Milestone state

| Milestone | State | Evidence |
|---|---|---|
| M0 build | Complete locally | Go/C++ build, scenario fixtures, generated-code check, reversible PostgreSQL migration |
| M1 durable API | Complete locally | Real gRPC submit/get/cancel against PostgreSQL 18.6; race-enabled repository suite |
| M2 first delivery | Complete locally | Producer to device to durable `ACKNOWLEDGED` across separate `orbitd`, gateway, and client processes |
| M3 recovery | Complete locally | Capped exponential retry with full jitter, retry-budget dead letter, global/per-device admission limits, gateway-control reconnect, lease-expiry recovery, and terminal TTL expiration |
| M4 replay | Foundation only | Stable C++ event queue and scenario contract exist; fault engine does not |
| M5-M7 | Not started | No failover runner, telemetry stack, benchmark results, or release evidence |

## Implemented

- Versioned scenario JSON contract with strict Go validation and valid/invalid
  fixtures.
- C++20 deterministic event queue ordered by timestamp then insertion ordinal.
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
  state), then exited 0 within the drain deadline. The tests start the
  binaries in their own process group so Windows Ctrl+Break does not hit the
  `go test` parent. `orbitd` shutdown still needs PostgreSQL.
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

The online happy path is proven end to end across separate processes. Gateway
control reconnect is implemented: the gateway no longer exits when its control
stream fails, and it drops device sessions so they re-register after `orbitd`
restarts. `DeviceService.Connect` is covered in-process: assignment and ACK
forwarding, hello rejection, rebind teardown without leaking `DeviceOffline`
onto the next control stream, and a bounded connect/disconnect goroutine check.
A reconnect soak covers control-stream churn with concurrent device connects.
Built `gateway` and `client` executables now drain and exit after an interrupt
(Ctrl+Break on Windows, SIGTERM elsewhere) against holding stubs, so shutdown
is not tested through `go run`. Duplicate delivery is proven across separate
`gateway` and `client` processes: two assignments yield two ACKs with the same
result hash and one handler invocation. A lost ACK followed by reconnect
replays the command without a second apply. Heartbeat frames keep the control,
gateway device, and client streams alive with a bounded silence timeout. M3
recovery policies are in place: lease expiry schedules capped exponential retry
with full jitter, exhausted retry budgets dead-letter commands, and submit
admission limits return `ResourceExhausted` when durable queues are full. The
next implementation unit is confirming the mid-run `orbitd` restart in
`scripts/smoke-online.ps1` against real PostgreSQL.

Only in-process reconnect, device-stream, and soak checks have been added on
top of the online path. No performance, scale, failover, or complete
at-least-once claim is justified yet, and the README contains no benchmark
numbers.

## Open Phase 2 work

- Confirm `scripts/smoke-online.ps1` still reaches `ACKNOWLEDGED` after the
  mid-run `orbitd` restart against real PostgreSQL.
- Extend Compose beyond PostgreSQL after the process path is proven. The
  PostgreSQL service itself now starts and reports healthy.

## Known limitations

- Local transport is plaintext and there is no producer/device authentication.
- Command TTL is enforced at submission, at lease selection, at dispatch, and by
  the terminal expiration sweep, but expiry is only observed when a scheduler
  cycle runs, so a device with no connected gateway keeps its expired commands
  until one does.
- No history checker, closed-loop fault replay, telemetry pipeline, dashboards,
  release benchmark, or Kubernetes deployment exists.
- Remote CI has not run against the GitHub remote.

## Safe resume sequence

1. Run `scripts/verify.ps1`.
2. Start or verify PostgreSQL 18.6 and run the race-enabled storage suite with
   `ORBIT_TEST_DATABASE_URL`.
3. Start the Compose PostgreSQL service and run `scripts/smoke-online.ps1` to
   confirm the online path still reaches durable `ACKNOWLEDGED` after an
   `orbitd` restart.
4. Run `scripts/smoke-online.ps1` against PostgreSQL to confirm `ACKNOWLEDGED`
   after an `orbitd` restart.
5. Update this ledger and the invariant evidence table with the result.
