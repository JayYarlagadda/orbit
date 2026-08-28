# Orbit Project Brief

## 1. Recommendation

Build Orbit, but build the focused version in this document rather than the
full initial proposal.

The project is worthy of a flagship resume slot because it can prove three
things that are difficult to fake in an interview:

1. precise reasoning about distributed delivery semantics;
2. implementation of recovery and concurrency under real failure;
3. quantitative validation with reproducible experiments.

The original proposal is too broad. PostgreSQL, RocksDB, multiple services,
Kubernetes, multi-region routing, `tc netem`, custom scheduling, chaos tooling,
and a C++ simulator could become several disconnected demos. The first public
release should instead tell one coherent story: commands are delivered safely
to devices whose connectivity cannot be trusted, and every stated guarantee is
backed by a repeatable test.

## 2. Fit with the current resume

The resume already provides strong evidence for:

- production observability and OpenTelemetry;
- Go, C++, Rust, C#, Kubernetes, and cloud infrastructure;
- graceful shutdown and retry-safe draining;
- GPU and algorithmic performance benchmarking;
- edge-computing simulation;
- contributions to large open-source systems.

Because of that background, a shallow Orbit implementation would add little.
Merely listing gRPC, Prometheus, Grafana, Docker, or Kubernetes would repeat
existing evidence. Orbit earns its place only if it adds depth in areas the
resume currently names but does not demonstrate end to end:

- delivery semantics under ambiguous outcomes;
- durable state transitions and recovery after process failure;
- session handoff between gateways;
- lease fencing and stale-worker rejection;
- backpressure with a documented overload contract;
- deterministic reproduction of distributed failures;
- automated checking of safety and liveness-oriented invariants.

## 3. Product framing

### One-sentence description

Orbit is a durable control plane that delivers expiring, prioritized commands
to intermittently connected edge devices and proves its behavior through
deterministic fault replay.

### Concrete use case

Treat devices as a remote edge fleet: industrial sensors, field equipment, or
mobile units that may disappear for minutes and reconnect through a different
gateway. Operators need to send commands such as configuration updates,
diagnostic requests, or safety actions.

This is more concrete than generic messaging and naturally requires device
identity and session epochs, command expiration, priority, bounded queues,
store-and-forward delivery, reconnect recovery, duplicate-safe execution, and
an auditable command history.

Orbit will not claim suitability for real safety-critical use. The domain is a
workload model that makes the systems requirements legible.

## 4. Primary user journeys

### Operator sends a command

1. An operator submits a command with a device ID, idempotency key, priority,
   payload, and expiration time.
2. Orbit validates admission limits and durably accepts or rejects it.
3. The API returns a stable command ID and current state.
4. If the device is connected, a worker attempts delivery. Otherwise the
   command remains queued without consuming unbounded memory.

### Device reconnects

1. The device connects to any healthy gateway and presents its identity,
   session epoch, and last durably acknowledged sequence.
2. Orbit fences an older session for that device.
3. The gateway resumes from authoritative server state.
4. Duplicate commands may be transported, but the client deduplicates by
   command ID and returns idempotent acknowledgements.

### Engineer reproduces a failure

1. The engineer runs a named scenario with a fixed random seed.
2. The C++20 engine produces a deterministic event schedule and expected fault
   history.
3. The integration runner applies the schedule to real Orbit processes and
   clients.
4. The history checker verifies invariants and stores a compact artifact with
   the seed, configuration, event history, metrics, and outcome.

## 5. Release goals

The first portfolio-quality release must demonstrate:

- durable enqueue and acknowledgement;
- at-least-once delivery with duplicate-safe client processing;
- strict per-device sequence ordering;
- reconnect through another gateway without lost acknowledged state;
- lease expiry plus fencing of stale workers;
- bounded queues, admission control, and retry limits;
- deterministic latency, logical frame loss, duplicate, disconnect, and
  gateway-failure scenarios;
- automated invariant verification;
- published benchmark methodology and reproducible results.

## 6. Explicit non-goals

The first release will not include:

- exactly-once delivery claims;
- global command ordering;
- a custom consensus protocol or Raft implementation;
- a custom durable database or production Kafka replacement;
- true multi-region replication;
- Kubernetes operators, service meshes, or eBPF;
- a browser dashboard beyond Grafana;
- production-grade device provisioning or a complete PKI;
- arbitrary payload streaming or large file transfer;
- more than one authoritative PostgreSQL deployment.

These can be discussed as limitations. They should not be implemented before
the release gates in the roadmap are satisfied.

## 7. What makes the revised project distinctive

### Closed-loop deterministic replay

The simulator must not be a separate toy that merely prints synthetic metrics.
It defines a scenario and reference event history that can drive the real Go
system. The resulting service history is checked against the same invariants.
This connects the C++ component directly to the distributed system.

### Lease fencing, not lease expiry alone

A lease timeout does not stop a paused worker from waking up and writing stale
state. Every assignment carries a monotonically increasing fencing token. The
database rejects completion or acknowledgement transitions from an older token.
This is a precise, interview-worthy correctness mechanism.

### Verifiable guarantees

Each guarantee is written as an exact scope and caveat, the state that makes it
enforceable, one or more failure cases, an automated test, and a metric that
makes violations or degradation visible.

### Honest performance claims

No throughput, latency, failover, or scale number enters the README or resume
until it is produced by a committed benchmark configuration. Results must
include hardware, software versions, dataset, command size, concurrency,
database settings, warmup, run count, and percentile method.

## 8. Portfolio success criteria

Orbit is ready for a resume when a reviewer can clone the repository and:

1. start the system locally with one documented command;
2. observe a command delivered to an online client;
3. run an offline/reconnect scenario and see recovery;
4. run a gateway-crash scenario with a fixed seed;
5. inspect a passing invariant report;
6. inspect traces and a Grafana dashboard;
7. reproduce at least one published benchmark within an explained tolerance;
8. understand the guarantees and limitations without reading source code.

## 9. Resume positioning after completion

The final resume entry should use two bullets and only measured facts. Draft
structure:

> Orbit | Go, C++20, gRPC, PostgreSQL, OpenTelemetry
>
> - Built a durable command-delivery system for intermittently connected edge
>   devices with at-least-once transport, per-device ordering, idempotent ACKs,
>   lease fencing, backpressure, and gateway session recovery.
> - Developed a deterministic C++20 fault-replay engine and history checker to
>   validate delivery invariants under logical frame loss, disconnects, delayed
>   ACKs, and gateway crashes; measured [verified throughput], [P99 latency], and
>   [recovery time] in reproducible experiments.

Numbers and stronger claims remain placeholders until the evidence exists.

## 10. Effort and stopping rule

Expect roughly 140 to 200 focused engineering hours for the complete release,
depending on familiarity with gRPC, PostgreSQL concurrency, and CMake. A strong
core through Phase 5 is more valuable than a rushed observability and deployment
stack. If time becomes constrained, stop after closed-loop replay and publish
the verified guarantees; do not replace missing correctness work with more
infrastructure logos.
