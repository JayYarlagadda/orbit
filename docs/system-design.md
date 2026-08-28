# Orbit System Design

## 1. Design principles

1. PostgreSQL is the authority for command and delivery state.
2. Gateways own connections, not durable truth.
3. Every ambiguous outcome is expected to produce a possible duplicate.
4. Per-device ordering is enforced at assignment and client application time.
5. All queues and concurrency are bounded.
6. Time-dependent logic is injectable so tests can use a deterministic clock.
7. State transitions are explicit, conditional, and auditable.
8. A documented guarantee is incomplete until a test can falsify it.

## 2. Logical architecture

```text
Operator/Producer
       |
       | gRPC: SubmitCommand, GetCommand
       v
+----------------------+          +----------------------+
| Orbit control plane  |<-------->| PostgreSQL           |
| API + scheduler      |          | commands, attempts,  |
| Go                   |          | sessions, audit log  |
+----------+-----------+          +----------------------+
           |
           | assignment stream
           v
+----------+-----------+          +----------------------+
| Gateway A / B        |<-------->| Device clients       |
| Go, connection state |  gRPC    | reference Go client  |
+----------+-----------+          +----------------------+
           ^
           | scenario events and process controls
+----------+-----------+          +----------------------+
| Integration runner   |<---------| C++20 fault engine   |
| Go                   | schedule | deterministic events |
+----------+-----------+          +----------------------+
           |
           v
+----------------------+
| History checker      |
| invariant report     |
+----------------------+
```

The initial code may deploy the API and scheduler in one Go process while
keeping package boundaries explicit. Splitting them into services is deferred
until measurements or failure isolation justify it.

## 3. Components

### Control plane

- validates producer requests and idempotency keys;
- performs transactional command enqueue;
- exposes status and cancellation APIs;
- schedules eligible commands;
- owns retry, TTL, dead-letter, and admission policies;
- writes append-only audit events for state transitions.

### Gateway

- authenticates a logical device identity in the first release;
- establishes one active session epoch per device;
- streams commands and acknowledgements;
- applies bounded per-connection buffers;
- reports connection and delivery outcomes;
- contains no authoritative acknowledgement state.

### Reference client

- reconnects with exponential backoff and jitter;
- persists the last applied sequence and a bounded deduplication record;
- applies a command through an idempotent handler;
- acknowledges duplicates without applying them twice;
- exposes controllable delays and failures for integration tests.

### C++20 fault engine

- uses a discrete-event priority queue and a seeded PRNG;
- reads a versioned scenario format;
- emits a canonical event schedule and reference history;
- supports latency, jitter, drop, duplicate, disconnect, reconnect, delayed ACK,
  gateway crash, and gateway recovery events;
- produces the same event sequence for the same version, seed, and config.

The engine does not implement an alternative messaging system. Its output is
consumed by the real-system integration runner. Release-one loss and delay are
logical command-frame and ACK faults applied by controlled endpoints. Orbit
does not call them packet-level network emulation; `tc netem` validation is a
separate stretch goal.

### Integration runner and history checker

- starts or connects to an Orbit test deployment;
- creates clients and producers from the scenario manifest;
- applies fault events at deterministic logical offsets;
- records API responses, database audit events, deliveries, acknowledgements,
  process lifecycle events, and telemetry summaries;
- checks the invariant catalog in `verification-and-benchmarks.md`;
- writes a self-contained result artifact.

## 4. External API

The versioned contract lives in `proto/orbit/v1/command.proto`. The implemented
Phase 1 surface is:

```text
CommandService
  SubmitCommand(request) -> command
  GetCommand(command_id) -> command
  CancelCommand(command_id) -> command

DeviceService
  Connect(stream DeviceFrame) <-> (stream ServerFrame)
```

`DeviceService` and the internal `GatewayControlService` are now defined in
`device.proto` and `gateway.proto`. The standalone gateway owns device streams;
its one control stream reports device lifecycle, delivery start, and ACKs to
`orbitd`. Every state-changing report includes a lease token and session epoch.
Command validation limits and runtime configuration are documented in
`api-and-configuration.md`.

Important frames:

```text
DeviceHello {
  device_id
  client_instance_id
  last_observed_session_epoch
  last_seen_sequence
}

CommandDelivery {
  command_id
  device_id
  sequence_number
  lease_token
  priority
  expires_at
  payload
  payload_hash
}

CommandAck {
  command_id
  sequence_number
  lease_token
  result_hash
  client_applied_at
}
```

The server never trusts client timestamps for ordering or lease validity. They
are diagnostic fields only.

## 5. Authoritative data model

### commands

```text
id                    UUID primary key
producer_id           text
idempotency_key       text
device_id             text
sequence_number       bigint
priority              smallint
payload               bytea
payload_hash          bytea
state                 enum
created_at            timestamptz
expires_at            timestamptz
next_attempt_at       timestamptz
attempt_count         integer
lease_owner           text nullable
lease_token           bigint
lease_expires_at      timestamptz nullable
acknowledged_at       timestamptz nullable
failure_reason        text nullable
```

Required constraints and indexes:

- unique `(producer_id, idempotency_key)`;
- unique `(device_id, sequence_number)`;
- index for eligible work by state, `next_attempt_at`, priority, and creation;
- index for expired leases;
- check that `expires_at > created_at`;
- conditional transitions that include the expected state and lease token.

### device_cursors

```text
device_id                    text primary key
next_sequence_number         bigint
last_terminal_sequence       bigint
active_session_epoch         bigint
active_gateway_id            text nullable
active_client_instance_id    text nullable
updated_at                   timestamptz
```

`last_terminal_sequence` is the largest contiguous sequence for which every
earlier command is acknowledged, expired, cancelled, or dead-lettered. It
advances transactionally with terminal state changes and prevents a skipped
sequence from blocking the device forever.

### delivery_attempts

Append-only attempt records include command ID, attempt number, gateway,
session epoch, lease token, start/end time, outcome, reason, result hash, and
the diagnostic client-applied timestamp. These records support debugging and
benchmarking, but the `commands` row remains the current state authority.

### audit_events

Append-only state transition records include a monotonic event ID, command ID,
old state, new state, actor, lease token, server timestamp, and correlation ID.
The history checker consumes these events.

## 6. Command state machine

```text
QUEUED -> LEASED -> IN_FLIGHT -> ACKNOWLEDGED
                ^        |           |
                |        |           | ambiguous outcome / disconnect
                |        +-----------+
                |          lease expiry + retry policy
                |
                +---- RETRY_WAIT

QUEUED / RETRY_WAIT / LEASED -> EXPIRED
retry budget exhausted       -> DEAD_LETTER
pre-delivery cancellation    -> CANCELLED
```

Rules:

- terminal states are `ACKNOWLEDGED`, `EXPIRED`, `DEAD_LETTER`, and
  `CANCELLED`;
- an acknowledgement is accepted only for the matching command, device,
  session policy, and current lease token;
- a stale worker cannot complete a command after a newer lease token exists;
- expiration is checked before assignment and again before device delivery;
- cancellation is accepted only from `QUEUED` or `RETRY_WAIT`; once leased, the
  API returns failed precondition because the device may already apply it.

## 7. Delivery guarantees

### At-least-once transport

Once Orbit durably accepts a non-expired command, it keeps attempting delivery
until acknowledgement, expiration, cancellation, or retry-budget exhaustion.
Crashes and lost acknowledgements can cause duplicate delivery.

This is not an unconditional liveness guarantee. Progress requires eventual
device connectivity, available storage, and sufficient retry/TTL budget.

### Idempotent producer submission

Repeating `SubmitCommand` with the same producer ID and idempotency key returns
the original command if the normalized request matches. A conflicting payload
returns a conflict error.

### Duplicate-safe client application

The reference client persists applied command IDs and sequence progress before
acknowledging. Receiving a known command causes another ACK but does not invoke
the handler again. Applications using Orbit must provide equivalent durable,
idempotent behavior.

### Strict per-device ordering

Commands receive a monotonically increasing device sequence in the enqueue
transaction. The scheduler exposes only the earliest non-terminal sequence for
a device. The client accepts only increasing sequences and treats older
sequences as duplicates. A gap is valid because an earlier sequence may already
be terminal without device execution. No ordering exists across devices.

An expired or cancelled command becomes a terminal sequence entry so it cannot
block all later commands indefinitely.

### Session fencing

Every successful reconnect advances the authoritative session epoch. A gateway
with an older epoch may finish network I/O, but its state-changing reports are
rejected. This prevents two gateways from concurrently advancing one device.

### Worker lease fencing

Every reassignment increments `lease_token`. Updates use a conditional form
equivalent to:

```sql
UPDATE commands
SET state = ...
WHERE id = ...
  AND state IN (...)
  AND lease_token = ...;
```

Expiry enables reassignment; the token makes a stale owner harmless.

## 8. Retry policy

The default policy is capped exponential backoff with full jitter:

```text
cap = min(max_backoff, base_backoff * 2^attempt)
delay = random(0, cap)
```

The PRNG is injectable in tests. Retry decisions distinguish transient
transport failure, terminal validation failure, expiration, cancellation, and
overload. Configuration is versioned and included in every benchmark artifact.

## 9. Backpressure and overload contract

Every buffering layer has a named limit:

- maximum accepted outstanding commands globally and per device;
- maximum payload size;
- bounded scheduler fetch batch;
- bounded worker pool;
- bounded gateway assignment channel;
- bounded per-device outbound buffer;
- bounded client deduplication retention policy.

When a durable admission limit is reached, `SubmitCommand` returns an explicit
resource-exhausted response with retry guidance. Orbit does not accept a command
and silently discard it. Priority affects eligible scheduling, not whether a
success response is truthful.

The first release uses FIFO across devices and strict per-device ordering.
Weighted priority scheduling is added only if the constrained-delivery
experiment proves it is needed.

## 10. Failure behavior

| Failure | Expected behavior |
|---|---|
| ACK lost | Lease expires or attempt times out; duplicate may be sent; client re-ACKs without reapplying. |
| Gateway crashes before ACK persistence | Device reconnects elsewhere; authoritative state may cause a duplicate; deduplication preserves one application. |
| Worker pauses beyond lease | A new token is issued; stale worker updates are rejected. |
| Device reconnects to two gateways | Newer session epoch wins; older session cannot advance durable state. |
| PostgreSQL unavailable | New submissions fail explicitly; workers stop taking new leases and retry database operations within a bound. |
| Command expires while offline | It transitions to `EXPIRED` and is never newly delivered. |
| Queue limit reached | Submission is rejected with resource exhaustion; process memory remains bounded. |
| Process receives SIGTERM | Admission and leasing stop, in-flight work drains for a deadline, telemetry flushes, and remaining leases recover by expiry. |
| Hard kill | Connection state is lost; durable state recovers through reconnect and lease expiry. |

## 11. Observability contract

Metrics use bounded-cardinality labels. Device IDs, command IDs, and raw error
strings are prohibited as metric labels.

Core metrics include command outcomes, delivery attempts and duplicates,
durable queue depth, active sessions, lease expirations, stale-token rejections,
retry delay, end-to-end latency, admission rejection, buffer utilization, and
gateway failover recovery duration.

Trace spans cover submit, persist, lease, assign, transport, ACK processing, and
retry scheduling. Correlation uses command ID in traces and logs, not metrics.

## 12. Security boundary for release one

Release one uses TLS in deployment examples and a test identity provider or
preconfigured device credentials. It enforces authenticated identities,
producer authorization for target groups, input limits, telemetry redaction,
payload hashes for correlation, parameterized SQL, and least-privilege database
credentials.

Full device provisioning, certificate rotation, and hardware roots of trust are
non-goals. The README must state this clearly.

## 13. Deferred decisions

These decisions wait for measurements:

- splitting the control plane and scheduler into separate deployables;
- Redis, Kafka, RocksDB, or another queue/store;
- weighted fairness beyond FIFO per device;
- multi-region routing and replication;
- Kubernetes deployment;
- Linux namespaces and `tc netem` validation;
- client deduplication compaction beyond a simple bounded strategy.
