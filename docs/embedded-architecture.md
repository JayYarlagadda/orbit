# Orbit embedded architecture

Companion to [embedded-expansion-plan.md](embedded-expansion-plan.md). Describes
how the STM32 endpoint, edge gateway, and existing distributed runtime connect.

## Layered responsibilities

| Layer | Runs on | Responsibility |
|-------|---------|----------------|
| **Firmware** | STM32H7 + Zephyr | Sample sensors, sequence, bounded RAM queue, flash log, embedded transport |
| **Edge gateway** | Linux (C++20 or Go) | Decode embedded protocol, device/session state, translate to Orbit gRPC |
| **Distributed runtime** | Linux (`orbitd`, gateway, PostgreSQL) | Durable commands, leasing, ordering, idempotency, observability (frozen at v1.0) |

## Data flow

```text
[IMU/SPI] → acquisition ISR/DMA → ring buffer → persistence thread
                                                    ↓
                                            transport thread
                                                    ↓
                              Ethernet (primary) or CAN FD (stretch)
                                                    ↓
                                         Orbit Edge Gateway
                                                    ↓
                              gRPC CommandService / GatewayControl (existing)
                                                    ↓
                                              orbitd → PostgreSQL
```

## RTOS thread map (Zephyr)

| Priority (high → low) | Thread | Primitives |
|----------------------|--------|------------|
| Highest | Watchdog / safety supervisor | Timer, event flags |
| High | Sensor acquisition | ISR, DMA completion, message queue |
| Medium-high | Local persistence (flash log) | Mutex, semaphore |
| Medium | Orbit transport | Message queue, blocking socket/CAN |
| Low | Telemetry / diagnostics | Work queue, logging |

Design constraints:

- Static allocation in the acquisition hot path
- Bounded queue depth with explicit drop policy and high-water metrics
- No cloud protocols on the MCU

## Edge gateway boundary

The edge gateway is **not** the existing `cmd/gateway` device-stream process
today; it is a new Linux component that:

1. Accepts framed records from embedded nodes
2. Validates CRC, sequence, and device identity
3. Maps records to Orbit command submissions (idempotency keys derived from
   device + sequence)
4. Tracks ACKs and informs the MCU what may be deleted from flash

Keep protocol versioning in `embedded/protocol/` and gateway decode in
`gateway/transport/` when implemented.

## What stays frozen

Everything under `cmd/orbitd`, `internal/storage`, `internal/scheduler`, the
C++ simulator contract, and M7 benchmark artifacts at tag
`v1.0-distributed-runtime` unless a backward-compatible extension is explicitly
approved in an ADR.
