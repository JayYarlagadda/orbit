# ADR 0004: Replay Logical Transport Faults Before Packet Faults

- Status: accepted
- Date: 2026-08-27

## Context

Packet-level emulation is platform-specific and does not make wall-clock
execution fully deterministic. Orbit needs stable scenarios that can reproduce
ambiguous delivery and recovery histories in CI.

## Decision

Release-one scenarios inject logical command-frame and acknowledgement faults
through controlled endpoints. The C++20 engine orders seeded events, and the Go
runner applies them to real Orbit processes. Equal timestamps use document
order as a stable tie-breaker.

Linux `tc netem` is a later validation layer. Until that work exists, Orbit will
not describe logical frame loss as packet-level network emulation.

## Consequences

- Correctness scenarios are portable and deterministic enough for CI.
- Simulator output must drive the real services; standalone synthetic output
  is not accepted as delivery-system evidence.
- Published material must state which faults are logical and which were
  validated against a real network stack.
