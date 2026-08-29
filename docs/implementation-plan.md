# Orbit Implementation Plan

## Current status

Phase 1 is complete. Its migration, repository, gRPC, idempotency, concurrency,
rollback, cancellation, and external CLI gates pass against PostgreSQL 18.6.
Phase 2 is in progress: session epochs, ordered leasing, fencing, attempt
records, lease-expiry recovery, acknowledgement persistence, gateway-control
and device protocols, bounded scheduler and gateway queues, the gateway
executable, durable client state, and control-stream reconnect are implemented.
Duplicate-delivery and disconnect-during-send process tests are the next work
items. Detailed evidence and limitations are maintained in `current-status.md`.

## 1. Execution rules

- Complete phases in order. Later infrastructure must not hide an incomplete
  correctness model.
- Every phase ends with a runnable demonstration and automated acceptance gate.
- Update the relevant design document in the same change as a behavior change.
- Keep the control plane modular, but do not split a process without a measured
  or reliability-driven reason.
- Record decisions that change guarantees, dependencies, or deployment
  assumptions in `docs/decisions/` as short ADRs.
- Do not publish benchmark numbers from debug builds, uncommitted configs, or
  single unexplained runs.

## 2. Planned repository layout

```text
orbit/
|-- cmd/
|   |-- orbitd/                 # API and scheduler process
|   |-- gateway/                # device connection process
|   |-- client/                 # reference device client
|   `-- scenario-runner/        # real-system fault runner and checker
|-- internal/
|   |-- command/                # domain model and state transitions
|   |-- storage/                # PostgreSQL repositories and migrations
|   |-- scheduler/              # eligibility, ordering, leases, retries
|   |-- gateway/                # sessions, streams, bounded buffers
|   |-- client/                 # durable deduplication and apply logic
|   |-- history/                # audit events and invariant checks
|   |-- telemetry/              # metrics, tracing, structured logging
|   `-- testclock/              # deterministic clock and PRNG adapters
|-- proto/
|-- migrations/
|-- simulator/
|   |-- include/
|   |-- src/
|   |-- tests/
|   `-- CMakeLists.txt
|-- scenarios/
|   |-- smoke/
|   |-- correctness/
|   `-- benchmarks/
|-- deployments/
|   |-- compose/
|   `-- kubernetes/             # deferred
|-- dashboards/
|-- scripts/
|-- docs/
|   |-- decisions/
|   |-- results/
|   |-- project-brief.md
|   |-- system-design.md
|   |-- implementation-plan.md
|   `-- verification-and-benchmarks.md
|-- Makefile
|-- go.mod
`-- README.md
```

The exact layout may adapt to Go conventions during scaffolding. Ownership
boundaries and responsibilities should remain stable.

## 3. Phase 0: foundation and executable contracts

Status: complete locally. Remote CI has not run because no remote is configured.

### Work

- choose supported Go, C++, PostgreSQL, Protobuf, and container versions;
- create Go module, CMake project, lint/format configs, and local build targets;
- define configuration loading and validation conventions;
- define a versioned scenario JSON schema;
- write ADRs for PostgreSQL authority, delivery semantics, and monolith-first
  deployment;
- create CI for Go build/test/race, C++ build/test, Protobuf compatibility, and
  documentation link checks;
- create Docker Compose with PostgreSQL only;
- decide whether to delete the placeholder HTML or reserve it for a later
  diagnostic UI. It is not part of the core release.

### Gate

- clean checkout builds Go and C++ targets;
- CI executes at least one test in each language;
- PostgreSQL starts and migrations run in a disposable database;
- scenario schema accepts a valid fixture and rejects an invalid fixture;
- README contains exact build and test commands.

## 4. Phase 1: durable command core

Status: complete and verified against PostgreSQL 18.6 on 2026-08-28.

### Work

- define Protobuf types and `CommandService` API;
- implement command, device cursor, attempt, and audit migrations;
- implement transactional sequence allocation per device;
- implement producer idempotency and conflicting-request detection;
- implement submit, get, and pre-delivery cancel operations;
- implement explicit command state transition functions;
- introduce injected clock and deterministic randomness interfaces;
- add structured errors mapped to gRPC status codes.

### Tests

- duplicate submission returns the original command;
- same idempotency key with different content returns conflict;
- concurrent submissions allocate unique increasing device sequences;
- invalid TTL, priority, device ID, and payload size are rejected;
- state transition table rejects illegal transitions;
- transaction rollback leaves no partial command or cursor update.

### Gate

- a command can be submitted, persisted, queried, and cancelled through gRPC;
- all state transitions produce audit events in the same transaction;
- unit, integration, and race tests pass.

## 5. Phase 2: one gateway and reliable client

Status: in progress. Storage fencing, scheduling, protocols, gateway process,
and durable client library are implemented; process-level delivery is proven
and control-stream reconnect is implemented. Duplicate-delivery, disconnect
during send, and soak gates remain open.

### Work

- define bidirectional device stream frames;
- implement gateway registration, device hello, heartbeat, and disconnect;
- implement authoritative session epoch acquisition;
- build reference client with reconnect and persistent local state;
- implement basic scheduler eligibility and bounded worker pool;
- implement lease acquisition with `SKIP LOCKED` or an equivalent reviewed
  PostgreSQL pattern;
- increment and propagate lease fencing tokens;
- implement delivery, ACK persistence, attempt records, and duplicate ACK logic;
- enforce earliest non-terminal sequence per device.

### Tests

- online device receives and applies a command once;
- duplicate transport invokes the client handler once and produces a valid ACK;
- out-of-order frame cannot advance the device cursor;
- stale lease token cannot acknowledge or complete a command;
- stale session epoch cannot advance state;
- disconnect during send leaves recoverable durable state;
- all channels and goroutines terminate in the shutdown test.

### Gate

- a scripted demo submits commands, disconnects a client, reconnects it, and
  ends with ordered acknowledgements;
- `go test -race` is clean for gateway, scheduler, and client packages;
- process memory does not grow with a fixed queue and disconnected client during
  a bounded soak test.

## 6. Phase 3: retry, expiration, and overload behavior

### Work

- implement timeout classification and capped exponential backoff with jitter;
- implement `RETRY_WAIT`, retry budget, dead-letter, and expiration sweeps;
- recheck TTL immediately before transport;
- add global and per-device durable admission limits;
- add bounded scheduler batches, assignment channels, and connection buffers;
- return explicit resource-exhausted responses with retry metadata;
- implement graceful SIGTERM sequence and telemetry flush hooks;
- define configuration defaults with validation and safe upper bounds.

### Tests

- fixed seed produces an exact retry schedule;
- expired command is never newly delivered;
- terminal failure reaches dead letter with a reason;
- a full per-device queue rejects new work without affecting other devices;
- producer rate above service rate keeps memory bounded;
- SIGTERM stops leasing, drains within deadline, and leaves work recoverable;
- hard kill followed by restart recovers through lease expiry.

### Gate

- overload demo shows explicit rejection and stable memory;
- retry, TTL, dead-letter, and shutdown behaviors have deterministic tests;
- profiling identifies no goroutine leak after reconnect soak.

## 7. Phase 4: deterministic C++20 fault engine

### Work

- implement scenario parser and schema-version validation;
- implement stable seeded PRNG rules and document reproducibility boundaries;
- implement discrete-event queue with stable tie breaking;
- implement latency, jitter, drop, duplicate, delayed ACK, disconnect, reconnect,
  gateway crash, and recovery events;
- emit canonical JSON event schedules and reference histories;
- add golden fixtures for seeds and configs;
- add property tests for event monotonicity, deterministic output, and bounded
  event expansion;
- benchmark simulator throughput separately from Orbit service throughput.

### Gate

- same build, scenario version, and seed produce byte-identical canonical output;
- golden scenarios run in CI;
- sanitizer-enabled C++ tests pass;
- the engine output is consumed by the scenario runner, not demonstrated only
  as a standalone binary.

## 8. Phase 5: two-gateway failover and closed-loop replay

### Work

- deploy two gateways against the same authoritative store;
- allow client reconnect to choose another healthy gateway;
- finalize session epoch fencing across gateways;
- implement the Go scenario runner and process-control adapter;
- capture API, delivery, ACK, audit, and lifecycle events into one history;
- implement invariant checker rules from the verification document;
- store failure artifacts with seed, versions, config, history, and checker result;
- add event-prefix minimization for failed scenarios if time permits.

### Tests

- gateway dies before send;
- gateway dies after send but before ACK persistence;
- old gateway resumes after a newer session exists;
- delayed stale worker attempts completion after lease reassignment;
- repeated reconnects do not violate ordering or leak sessions;
- each injected bad implementation used by checker tests is detected.

### Gate

- a fixed-seed gateway crash is replayable against real services;
- all release invariants pass;
- intentionally corrupted histories fail with a precise counterexample;
- recovery time and duplicate count are derived from captured events.

## 9. Phase 6: observability and operating evidence

### Work

- instrument API, database, scheduler, gateway, and client paths;
- export bounded-cardinality Prometheus metrics;
- add OpenTelemetry traces with correlation across retry and reconnect;
- add structured logs with redaction tests;
- create Grafana panels for queue depth, delivery latency, attempts, duplicates,
  lease expiry, stale-token rejection, sessions, and admission rejection;
- write a local operations guide and failure-triage flow;
- test telemetry flush on graceful shutdown.

### Gate

- a failure scenario is explainable from a trace, metrics, and audit history;
- metric-label tests reject device/command IDs and unbounded error strings;
- dashboard provisioning is automatic in Docker Compose;
- screenshots are captured from a committed scenario, with configuration noted.

## 10. Phase 7: benchmark and release

### Work

- implement the benchmark matrix in the verification document;
- pin release builds and all environment settings;
- run warmups and repeated trials on a documented machine;
- collect CPU, memory, database, throughput, percentile latency, retry,
  duplicate, queue, and recovery metrics;
- analyze the first bottleneck rather than only reporting the best number;
- publish raw summaries and plotting inputs under `docs/results/`;
- write known limitations and a claim-evidence table;
- record a short terminal/demo script for reviewers;
- replace resume placeholders only after results are committed.

### Gate

- all portfolio success criteria in the project brief pass;
- another person can reproduce the smoke scenario from a clean checkout;
- benchmark results include methodology and variability;
- every README claim links to a test, result, or design section;
- release tag is built by CI and has no uncommitted benchmark changes.

## 11. Optional Phase 8: earned stretch goals

Select at most one initially, based on evidence:

- validate simulator assumptions with Linux `tc netem`;
- compare FIFO with weighted fair priority scheduling under a bandwidth cap;
- deploy the proven topology to Kubernetes and measure failover differences;
- implement result-preserving scenario minimization;
- test a second persistence approach behind the established storage contract.

Do not start multi-region replication unless Orbit already has a complete,
measured single-region release.

## 12. Milestone sequence

Milestones are outcome-based rather than date-based:

1. `M0-build`: reproducible Go/C++ build and database migrations.
2. `M1-durable-api`: accepted commands are durable and idempotent.
3. `M2-first-delivery`: one device receives ordered commands through one gateway.
4. `M3-recovery`: retry, TTL, backpressure, shutdown, and lease recovery work.
5. `M4-replay`: deterministic C++ schedules drive real services.
6. `M5-failover`: two gateways recover with session and lease fencing.
7. `M6-observable`: failures are visible and diagnosable.
8. `M7-release`: invariants and benchmark claims are reproducible.

## 13. Documentation checklist

Keep these current throughout implementation:

- README quick start and verified claims;
- architecture and data-flow diagram;
- delivery semantics and caveats;
- command and session state machines;
- failure model and recovery table;
- Protobuf/API compatibility policy;
- database schema and migration policy;
- configuration reference with defaults and limits;
- scenario format and reproducibility contract;
- invariant catalog and test mapping;
- benchmark methodology and raw result references;
- local operations and troubleshooting guide;
- security assumptions and threat boundary;
- ADRs for consequential decisions;
- known limitations and future work;
- interview-oriented claim/evidence summary.

## 14. Definition of done

Orbit is not done when services merely communicate. It is done when documented
behavior survives the named faults, invariant checks catch invalid histories,
resource limits remain bounded, benchmark claims are reproducible, the repo can
be run without author guidance, and every resume claim has code, tests, traces,
or results behind it.
