# Orbit Engineering Instructions

Orbit is a correctness-focused distributed system. Do not add placeholder
features, invented performance claims, or infrastructure that is not required
by a documented guarantee.

## System invariants

- PostgreSQL is authoritative for durable command, session, lease, and audit
  state.
- Delivery is at least once. Never claim exactly once.
- Ordering is strict per device only; no global ordering is promised.
- Lease tokens and session epochs are monotonic fencing values.
- Every queue, worker pool, buffer, retry policy, and payload has an explicit
  bound.
- Logical fault injection must not be described as packet-level emulation.

## Implementation standards

- Prefer narrow packages and caller-owned interfaces over generic frameworks.
- Keep state transitions explicit and test illegal transitions.
- Inject clocks and randomness into time-dependent behavior.
- Wrap errors with operation context; do not log and return the same error.
- Do not start unbounded goroutines or use unbounded channels.
- Use parameterized SQL and schema-qualified object names.
- Keep metric labels bounded; command IDs and device IDs belong in traces and
  structured logs, not metric labels.
- Use C++20 RAII and value semantics. Avoid owning raw pointers and global
  mutable state.
- Treat compiler warnings as errors and keep sanitizer tests passing.

## Change requirements

- Add tests for behavior changes and failure paths.
- Update the relevant design document when a guarantee, state transition,
  configuration contract, or fault model changes.
- Add an ADR for consequential architectural changes.
- Never put benchmark numbers in documentation until the configuration and raw
  result artifact are committed.
- Never commit secrets, local toolchains, generated build output, or local
  benchmark results.
