# Orbit Verification and Benchmark Specification

## 1. Purpose

Testing is the product differentiator for Orbit. This document defines what
must be proven, which artifacts count as evidence, and how performance results
may be reported.

## 2. Test layers

### Unit tests

Use table-driven Go tests for state transitions, retry calculations, admission
rules, sequence eligibility, acknowledgement validation, and configuration.
Use C++ tests for event ordering, PRNG stability, parsing, and fault expansion.

### Database integration tests

Run against real PostgreSQL. Cover transactional sequence allocation, producer
idempotency, concurrent lease acquisition, fencing-token updates, audit-event
atomicity, expiration, and query plans for scheduler paths.

### Protocol integration tests

Run real gRPC servers and clients with injectable clocks and transport fault
hooks. Cover reconnect, duplicate frames, delayed ACKs, cancellation races,
stream closure, and graceful shutdown.

### Model and property tests

Generate command/state sequences and verify that illegal transitions never
commit. Check monotonic tokens and sequences, terminal-state immutability,
deterministic scenario output, and bounded event growth.

### End-to-end fault tests

Run real processes and PostgreSQL through the scenario runner. Store the event
history and invariant report for any failure. A fixed seed must reproduce the
ordering of injected events even if wall-clock timing varies slightly.

### Soak and resource tests

Repeatedly connect/disconnect clients, exceed delivery capacity, and restart
workers. Track goroutines, file descriptors, heap, queue depth, and database
connections. Define an explicit duration and acceptable drift before running.

## 3. Invariant catalog

Identifiers are stable so tests, reports, and documentation can refer to them.

### Current automated evidence

As of 2026-08-28, the following implementation tests exist. This table records
coverage, not final release proof; process-level scenarios and history-checker
evidence remain required.

| Invariant | Current evidence | Remaining evidence |
|---|---|---|
| INV-01 | Closed command transition table; ACK terminal-state database update | Corrupted-history checker test |
| INV-02 | Durable client state applies a retained command ID once across reopen | Lost-ACK process scenario |
| INV-03 | PostgreSQL leases only the earliest non-terminal device sequence | Multi-command device process test |
| INV-05 | Stale lease token and stale/mismatched session epoch integration tests | Paused-worker process scenario |
| INV-06 | Submit plus forced audit-failure rollback integration test | API failure injection scenario |
| INV-07 | Matching and conflicting idempotency integration tests | Concurrent duplicate producer test |
| INV-08 | Session acquisition increments epochs; stale release/report rejected | Dual-gateway process scenario |
| INV-09 | Cancellation and ACK advance contiguous terminal cursor | Expiry/dead-letter successor tests |
| INV-10 | Scheduler, control stream, gateway, and per-device queues have validated bounds | Memory/connection soak test |
| INV-11 | Submit, cancel, lease, in-flight, retry-wait, and ACK audit writes share transactions | Audit history checker |

PostgreSQL integration tests run against the real `postgres:18.6-bookworm`
image. The local run on 2026-08-28 passed under the Go race detector. Remote CI
has not executed because this repository currently has no configured remote.

### INV-01: acknowledged state is terminal

Once a command is durably `ACKNOWLEDGED`, no later audit event may move it to a
non-terminal state or create a new accepted delivery attempt.

### INV-02: one client-side application per command

For the reference client and retained deduplication window, a command ID may
produce at most one successful handler-application event. Duplicate deliveries
may produce multiple ACK frames.

### INV-03: per-device application order

Successful handler-application events for a device have strictly increasing
sequence numbers, except that documented terminal skips for expired or
cancelled commands may advance the cursor without handler execution.

### INV-04: no new delivery after expiry

No delivery attempt may begin after the authoritative server determines that
`expires_at` has passed. A delivery begun before expiry may have an ambiguous
outcome; this boundary must be visible in the history.

### INV-05: fencing-token monotonicity

Lease tokens for a command and session epochs for a device only increase. An
event carrying a stale value cannot advance authoritative state.

### INV-06: durable acceptance is truthful

A successful submit response has exactly one durable command with the returned
ID and matching normalized content. A rejected request creates no command.

### INV-07: idempotency-key consistency

A producer ID and idempotency key map to at most one normalized command body.
Retries with matching content return that command; conflicting content fails.

### INV-08: single active authoritative session

At any authoritative event position, at most one session epoch can advance a
device's durable delivery state. Physical network connections may overlap.

### INV-09: terminal sequence does not block successors

Acknowledged, expired, cancelled, or dead-letter commands cannot permanently
block later eligible commands for the same device.

### INV-10: bounded in-memory work

For a fixed configuration, in-memory queues and worker counts never exceed
their declared limits, even when producers and reconnecting clients exceed
service capacity.

### INV-11: every durable transition is auditable

Each change to authoritative command state has one matching audit event in the
same transaction, with old state, new state, actor, and relevant token.

### INV-12: payload confidentiality in telemetry

Payload contents, credentials, and configured secret canaries never appear in
logs, traces, metrics, or scenario result summaries.

## 4. Required correctness scenarios

Each scenario has a committed config, stable name, seed, expected invariants,
and maximum runtime.

| Scenario | Fault sequence | Primary evidence |
|---|---|---|
| `online-smoke` | No injected fault | Submit, deliver, apply, ACK history |
| `offline-reconnect` | Device offline, queued commands, reconnect | Ordered recovery, queue depth |
| `lost-ack` | Deliver, apply, drop logical ACK frame, retry | Duplicate transport, one application |
| `gateway-crash-before-send` | Lease then crash | Lease recovery, eventual delivery |
| `gateway-crash-after-send` | Apply then crash before ACK commit | Possible duplicate, no double apply |
| `stale-worker` | Pause owner, expire lease, resume owner | Stale-token rejection |
| `dual-gateway-session` | Reconnect to B while A remains alive | Epoch fencing |
| `expiry-offline` | TTL passes before reconnect | No delivery after expiry |
| `retry-exhaustion` | Repeated transport failure | Dead-letter transition |
| `overload-one-device` | Per-device limit exceeded | Explicit rejection, isolation |
| `overload-global` | Producer rate exceeds capacity | Bounded memory, truthful admission |
| `shutdown-drain` | SIGTERM with in-flight work | Bounded drain, recoverable remainder |
| `restart-recovery` | Hard kill control plane/gateway | Durable recovery |
| `reconnect-soak` | Repeated drop/reconnect cycle | No leak, preserved order |

## 5. Checker validation

The history checker itself must be tested. For every invariant, include at
least one minimal passing history and one deliberately corrupted history. The
failing result must name the invariant ID, counterexample position, involved
identifiers, expected and observed state, scenario version, and seed.

A checker that only passes real runs is not sufficient evidence.

## 6. Reproducibility artifact

Every end-to-end scenario result stores:

```text
scenario name and schema version
seed and PRNG algorithm version
Git commit and dirty-worktree flag
Go/C++ compiler and dependency versions
container image digests
database migration version
full non-secret configuration
host OS and hardware summary
event schedule
normalized observed history
invariant report
metric summary
start/end timestamps
exit status and failure reason
```

Secrets and payloads are replaced with stable hashes where correlation is
needed.

## 7. Benchmark metrics

Primary metrics:

- accepted and acknowledged commands per second;
- end-to-end latency P50, P95, P99, and maximum;
- reconnect-to-first-delivery recovery time;
- gateway-failure recovery time;
- retry and duplicate-delivery rates;
- admission rejection rate;
- durable and in-memory queue depth over time;
- CPU and resident memory by process;
- PostgreSQL CPU, connections, lock waits, transaction rate, and storage I/O;
- stale-token rejection and lease-expiration counts.

Latency starts at successful durable acceptance and ends at durable ACK commit.
Transport-only latency may be reported separately but never substituted for
end-to-end latency.

## 8. Benchmark matrix

Start small enough to validate the harness, then increase load until a resource
or latency objective is reached. Client counts below are starting points, not
resume promises.

### B0: harness calibration

```text
100 connected clients
10,000 commands
small fixed payload
0 injected loss
single gateway
```

Purpose: validate measurement overhead and result stability.

### B1: healthy baseline

```text
1,000 then 10,000 clients as supported
fixed and mixed payload sizes
0 loss, local network
one and two gateways
```

Purpose: establish saturation curve, first bottleneck, and latency distribution.

### B2: lossy high-latency delivery

```text
500 ms logical RTT
5 percent logical delivery/ACK frame loss according to scenario definition
jitter enabled
```

Purpose: quantify retry amplification and tail-latency change.

### B3: gateway failure

```text
steady-state load
two gateways
kill one gateway at a fixed event count
```

Purpose: measure recovery time, duplicate count, and queue catch-up.

### B4: mass disconnect

```text
30 percent of devices disconnected for a fixed interval
producers continue at a fixed rate
```

Purpose: observe durable queue growth, admission behavior, memory, and recovery.

### B5: constrained delivery capacity

```text
delivery capacity below producer rate
multiple priorities
fixed global and per-device limits
```

Purpose: prove bounded resource use and evaluate whether priority scheduling is
needed. Compare algorithms only after the FIFO result is understood.

### B6: reconnect churn

```text
clients reconnect repeatedly across gateways
small continuous command stream
```

Purpose: find session leaks, lock contention, and epoch-management cost.

## 9. Experimental method

For every published result:

1. build release binaries from a named commit;
2. record hardware, OS, power mode, container runtime, and database settings;
3. isolate avoidable background load or disclose it;
4. define client count, command count/rate, payload distribution, TTL, retry
   policy, queue limits, worker count, connections, and fault model;
5. perform a documented warmup;
6. run at least five measured trials unless runtime makes that impractical;
7. report median and variability across trials, not only the best run;
8. derive percentiles from a documented histogram or raw-sample method;
9. retain configuration and summary outputs in `docs/results/`;
10. explain the bottleneck and any anomalous run.

## 10. Claim-to-evidence table

Before release, the README must include a completed form of this table:

| Claim | Design basis | Automated evidence | Result artifact |
|---|---|---|---|
| At-least-once transport | Delivery semantics section | lost-ACK and crash scenarios | scenario report |
| Duplicate-safe reference client | Client dedup design | INV-02 checker tests | lost-ACK report |
| Per-device ordering | sequence and eligibility rules | INV-03 properties and E2E tests | reconnect report |
| Lease fencing | token-conditional transitions | stale-worker integration test | stale-worker report |
| Gateway recovery | session epoch design | dual-gateway crash scenarios | failover report |
| Bounded overload | limit catalog | overload and soak tests | B4/B5 results |
| Performance number | benchmark method | harness validation | named benchmark result |

## 11. Performance anti-patterns to avoid

- choosing resume numbers before measurement;
- reporting simulator events per second as message-system throughput;
- comparing Orbit to Kafka, NATS, or another system without equivalent
  durability and workload settings;
- using average latency without tail percentiles;
- omitting failed or variable trials;
- changing queue, retry, or database settings between compared runs;
- running with telemetry disabled if the documented system includes telemetry;
- claiming exact reproducibility of wall-clock scheduling across machines;
- treating a successful load-generator response as a durable ACK.

## 12. Release verification target

The final repository should expose one top-level target, such as:

```text
make verify-release
```

It should run formatting checks, unit tests, race tests, database integration
tests, C++ tests with sanitizers where supported, protocol compatibility checks,
correctness scenarios, checker self-tests, and documentation validation. Long
benchmarks remain separate, but committed artifacts are schema-validated.
