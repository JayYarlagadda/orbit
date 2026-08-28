# ADR 0001: PostgreSQL Is the Authoritative Store

- Status: accepted
- Date: 2026-08-27

## Context

Orbit must recover command state after gateway, scheduler, and worker failure.
Gateways also need to hand device sessions between processes without treating
connection-local memory as durable truth.

## Decision

PostgreSQL is the sole authority for command state, device cursors, session
epochs, lease fencing tokens, delivery attempts, and audit events in release
one. State transitions and their audit events commit in the same transaction.

Gateways may cache connection routing and bounded outbound work. Cache loss may
reduce performance or cause a duplicate delivery, but it cannot lose accepted
state or advance an acknowledgement.

## Consequences

- Correctness depends on reviewed transactions, constraints, and conditional
  updates rather than an in-memory queue.
- PostgreSQL unavailability stops durable acceptance and new lease acquisition.
- The first scalability ceiling will likely involve database contention or I/O;
  benchmarks must measure both.
- Adding Kafka, Redis, or RocksDB requires a later ADR and evidence that the
  existing authority model is insufficient.
