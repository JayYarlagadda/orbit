# ADR 0002: At-Least-Once Delivery with Idempotent Application

- Status: accepted
- Date: 2026-08-27

## Context

A device can apply a command and lose its acknowledgement before Orbit commits
it. No protocol can distinguish that outcome from a command the device never
received without additional application state.

## Decision

Orbit provides at-least-once transport within TTL and retry-budget constraints.
It may redeliver after ambiguous outcomes. Producer submission is idempotent,
acknowledgements are idempotent, and the reference client persists command IDs
so it can acknowledge a duplicate without applying it again.

Orbit does not claim exactly-once delivery. Application handlers must be
idempotent or use equivalent durable deduplication.

## Consequences

- Duplicate delivery is expected and measured, not treated as an exceptional
  implementation failure.
- Command IDs and payload hashes are stable across attempts.
- Correctness tests must inject lost acknowledgements and gateway crashes after
  application but before acknowledgement commit.
