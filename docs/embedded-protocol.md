# Orbit embedded protocol (sketch)

Draft framing for MCU → edge gateway messages. Final layout is implemented in
`embedded/protocol/` during **E4**; this document is the design reference.

## Design goals

- Fixed header for parsing on constrained firmware
- CRC over header + payload
- Device ID + monotonic sequence for ordering and idempotency at the gateway
- No protobuf or gRPC on the MCU

## Frame layout (v0 sketch)

| Field | Size | Description |
|-------|------|-------------|
| Magic | 2 B | `0x4F52` (`"OR"`) |
| Version | 1 B | Protocol version |
| Message type | 1 B | Sample, ACK hint, health, … |
| Device ID | 8 B | Fixed-width node identifier |
| Sequence | 4 B | UInt32, per-device monotonic |
| Timestamp | 8 B | Microseconds since boot or UTC offset |
| Payload length | 2 B | Bytes following header |
| Payload | N B | Sensor blob or control message |
| CRC32 | 4 B | IEEE polynomial over bytes after magic |

Endianness: **little-endian** unless board constraints dictate otherwise.

## Message types (initial)

| Type | Direction | Purpose |
|------|-----------|---------|
| `SAMPLE` | MCU → gateway | Sensor record for Orbit ingestion |
| `ACK_RANGE` | gateway → MCU | Highest contiguous acknowledged sequence |
| `HEALTH` | MCU → gateway | Subsystem status, error counts |
| `NACK` | gateway → MCU | Reject corrupt or out-of-policy frame |

## Gateway validation

- Reject unknown version or bad magic
- Verify CRC before touching payload
- Detect duplicate sequence (idempotent accept)
- Detect gap (trigger replay request or NACK policy)
- Rate-limit and apply backpressure when `orbitd` admission rejects

## CAN FD variant (E9)

Same logical frame; CAN FD splits into multi-frame transfer with a small
reassembly buffer on the gateway. Corrupted frames must increment error counters
visible in HIL reports.
