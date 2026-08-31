# Orbit Edge Gateway (planned)

**Status:** Not started (milestone **E4**).

Linux process that bridges **embedded binary protocol** (MCU) to the **existing
gRPC backend** (`orbitd`). This is separate from `cmd/gateway`, which terminates
device streams for the reference Go client today.

## Planned layout

```text
gateway/
├── transport/        # Ethernet/CAN listeners, frame parse
├── device_sessions/  # Per-node sequence and ACK state
└── grpc_bridge/      # Orbit gRPC client to orbitd
```

## Architecture rule

Do not embed gRPC or Protobuf on the MCU. Decode and validate on Linux, then
submit commands using the same idempotency and ordering semantics as the
distributed runtime.

See [docs/embedded-architecture.md](../docs/embedded-architecture.md).
