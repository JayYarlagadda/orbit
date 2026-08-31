# Orbit Embedded Expansion Plan

Future scope for extending Orbit from a distributed reference runtime to a
constrained embedded endpoint with hardware-in-the-loop validation. The
distributed core (M0–M7) is frozen at tag **`v1.0-distributed-runtime`**; all
embedded work proceeds without disturbing that baseline.

## 1. Freeze the existing core

Do **not** change behavior or layout of the parts that already work:

- Go backend (`orbitd`, scheduler, PostgreSQL store)
- gRPC command and gateway-control APIs
- Durable store-and-forward semantics
- Idempotent retries and per-device ordering
- Admission backpressure
- Gateway failover and session fencing
- Deterministic C++ network-fault simulator
- Scenario runner, history checker, and harness benchmarks (B0–B1+)

**Git markers**

| Marker | Purpose |
|--------|---------|
| Tag `v1.0-distributed-runtime` | Immutable reference for the distributed runtime release |
| Directory `embedded/` | Firmware, drivers, protocol, and board support (new code only) |
| Branch `feature/embedded-endpoint` | Optional long-lived integration branch for embedded + gateway bridge work |

Embedded milestones are **E0–E9** (below). They do not replace M0–M7.

## 2. Target architecture

```text
                ORBIT EMBEDDED NODE
        +-----------------------------+
        | STM32H7 + Zephyr RTOS       |
        |                             |
Sensor --> SPI/I2C --> DMA --> Queue  |
        |                  |          |
        |                  v          |
        |             Record Store    |
        |                  |          |
        |                  v          |
        |          Messaging Runtime  |
        +------------------+----------+
                           |
                     Ethernet / CAN FD
                           |
                           v
                  Orbit Edge Gateway (Linux)
                           |
                           v
                       gRPC (existing)
                           |
                           v
                    Backend Storage
```

**Preferred platform:** STM32H743 Nucleo, Zephyr RTOS.

**Why STM32H7:** Cortex-M7, sufficient RAM/flash, DMA, timers, SPI/I2C/UART,
CAN/FDCAN, Ethernet — strong embedded interview signal without Arduino-level
abstraction hiding the engineering.

**Edge gateway rule:** Do not run gRPC on the MCU. The Linux edge gateway
decodes the embedded binary protocol, maintains device/session state, and
translates to the existing Orbit gRPC backend.

## 3. Milestone state (embedded)

| Milestone | State | Gate / evidence |
|---|---|---|
| E0 core freeze | Complete at `v1.0-distributed-runtime` | Tag + `verify-release`; `embedded/` scaffold; this plan committed |
| E1 bring-up & acquisition | Not started | Zephyr boots on Nucleo; custom SPI IMU driver; interrupt-driven samples at 500 Hz |
| E2 DMA & RTOS structure | Not started | DMA SPI path; bounded queues; priority map; jitter/CPU benchmarks vs polling |
| E3 persistent offline buffer | Not started | Flash circular log; sequence numbers; clean recovery after power loss |
| E4 transport & edge gateway | Not started | Embedded framing protocol; Linux gateway → existing `orbitd`; end-to-end ACK |
| E5 reconnect & replay | Not started | Network partition; ordered replay; idempotency; backpressure under load |
| E6 watchdog & health | Not started | Supervisor; subsystem heartbeats; persisted reboot reason; fault telemetry |
| E7 HIL automation | Not started | Python harness; scripted faults; pass/fail reports with sequence evidence |
| E8 stress & metrics | Not started | 1–24 h runs; published `docs/results/embedded/` summaries |
| E9 stretch (optional) | Not started | CAN FD transport; MCUboot A/B with rollback |

**Resume-worthy minimum:** E1–E8 complete (E9 optional).

## 4. Development phases (strict order)

Do not skip phases. Each phase ends with a runnable demo and recorded metrics.

### Phase 1 — E1: Bring-up and interrupt acquisition

1. STM32H7 + Zephyr board support under `embedded/boards/`
2. Custom SPI IMU driver (`embedded/drivers/imu/`) — init, registers, SPI
   transactions, interrupts, timeouts, reset/recovery (no opaque wrapper-only
   implementation)
3. Optional I2C sensor
4. **Polling baseline** first, then **data-ready ISR → SPI read → bounded queue**
5. Target **500 Hz** acquisition (1 kHz stretch)

### Phase 2 — E2: DMA and RTOS architecture

1. Replace polling SPI with **ISR → SPI DMA → DMA completion → queue**
2. Benchmark: CPU %, acquisition jitter, missed samples, ISR latency
3. Zephyr thread model (fixed priorities, no arbitrary threads):

```text
Highest  — watchdog / safety supervisor
           sensor acquisition
           local persistence
           Orbit transport
Lowest   — telemetry / diagnostics
```

4. Use semaphores, message queues, event flags, mutexes (document why each)
5. **Zero heap allocation** on the steady-state acquisition path (static pools +
   ring buffers)

### Phase 3 — E3: Persistent offline buffering

1. Append-only **circular flash log** (header, sequence, timestamp, length,
   payload, CRC)
2. Survive reboot and interrupted writes; sector/page boundaries; wear awareness
3. Track queue occupancy, high-water marks, dropped records, stack HWM

### Phase 4 — E4: Transport and edge gateway

1. Embedded binary protocol (`embedded/protocol/`) — type, device ID, sequence,
   timestamp, length, payload, CRC
2. **Orbit Edge Gateway** on Linux (`gateway/` evolution — see §10) — decode,
   validate, session state, gRPC bridge to existing backend
3. End-to-end: sample → gateway → `orbitd` → ACK → local removal

### Phase 5 — E5: Disconnect, replay, and semantics

Reuse distributed Orbit semantics on constrained hardware:

- Per-device ordering
- Retries and idempotency keys
- Backpressure when gateway or backend is saturated
- Ordered replay after long offline intervals

### Phase 6 — E6: Watchdog and health supervision

Subsystems report health: acquisition, storage, network, transport.

```text
fault detected → subsystem recovery → on failure → record reason → watchdog reset
```

Persist reboot reason (`WATCHDOG_RESET`, `SPI_TIMEOUT`, `QUEUE_CORRUPTION`,
`NETWORK_STALL`, …) and surface through Orbit telemetry.

### Phase 7 — E7: HIL testing

Extend the existing fault simulator mindset to hardware:

```text
hil/
├── scenarios/
├── runner.py
├── telemetry.py
└── reports/
```

Example scenarios: `network_disconnect`, `mcu_reboot`, `sensor_disconnect`,
`queue_pressure`, `watchdog`, `reconnect_replay`.

Collect TX/RX sequences, timestamps, MCU diagnostics, recovery times, drops,
duplicates — automated pass/fail.

### Phase 8 — E8: Stress benchmarks and published results

Instrument first; **do not invent resume numbers before measurement.**

| Category | Examples |
|----------|----------|
| Real-time | Sample rate, P50/P95/P99 acquisition latency, jitter, ISR latency, missed samples |
| Resources | CPU, RAM, flash, stack HWM, queue occupancy |
| Transport | msg/s, reconnect latency, replay throughput |
| Reliability | Loss during outage, duplicates, recovery after reset/sensor fault, max offline replay |
| Comparisons | Polling vs DMA; RAM-only vs flash buffer; immediate vs rate-limited replay |

Run **1 h, 6 h, 12 h, 24 h** stress with injected outages. Publish under
`docs/results/embedded/` and `benchmarks/realtime/`, `benchmarks/reconnect/`, etc.

### Phase 9 — E9: Stretch (optional)

- **CAN FD** as secondary transport (MCU → CAN → edge gateway → gRPC)
- **MCUboot** A/B slots, signed images, test boot, rollback after failed OTA

## 5. Sensor acquisition requirements

- At least one **SPI IMU** (accelerometer + gyroscope + timestamps + sequence)
- Enough custom driver code to demonstrate register-level understanding
- Initial **500 Hz**; **1 kHz** stretch goal

## 6. Store-and-forward on the MCU

**Network up:**

```text
sample → sequence → local queue → gateway → ACK → remove locally
```

**Network down:**

```text
sample → local queue → persistent buffer → wait
```

**Network returns:**

```text
persistent records → ordered replay → gateway → dedupe/idempotency → ACK
```

## 7. Metrics policy

- Instrument every category in §Phase 8 before claiming performance on a resume.
- Compare implementations (polling vs DMA, volatile vs persistent queue) with
  the same workload.
- Long-duration tests are evidence, not optional polish.

## 8. Planned repository layout (target)

Evolution from the current tree (incremental; no big-bang rename of `cmd/`):

```text
orbit/
├── cmd/                          # existing Go binaries (frozen at v1.0)
├── internal/                     # existing Go libraries
├── simulator/                    # existing C++20 fault engine
├── embedded/                     # NEW — firmware (Zephyr west module or app)
│   ├── boards/
│   ├── drivers/imu/
│   ├── acquisition/
│   ├── storage/
│   ├── transport/
│   ├── health/
│   ├── protocol/
│   └── app/
├── gateway/                      # NEW — edge gateway (evolves from cmd/gateway)
│   ├── transport/                #   embedded protocol + CAN/Ethernet
│   ├── device_sessions/
│   └── grpc_bridge/
├── hil/                          # NEW — Python HIL harness
├── benchmarks/
│   ├── …                         # existing harness configs
│   ├── realtime/
│   ├── reconnect/
│   ├── storage/
│   └── dma/
└── docs/
    ├── embedded-expansion-plan.md
    ├── embedded-architecture.md
    ├── embedded-protocol.md
    ├── embedded-failure-model.md
    └── results/embedded/
```

## 9. HIL test matrix (target)

| Scenario | Injects |
|----------|---------|
| Network failure | Link loss, partition |
| Sensor failure | IMU disconnect, SPI timeout |
| MCU reboot | Power cycle, watchdog |
| Gateway restart | Edge gateway process kill |
| Queue saturation | Backpressure at MCU |
| Long offline | Extended store-and-forward |
| Watchdog reset | Supervisor-forced reset |
| Corrupted record | CRC/log tamper |
| CAN disconnect | Secondary transport loss |
| Repeated reconnects | Churn across gateways |

## 10. Non-goals for embedded v1

- Replacing gRPC on the MCU
- Multi-region backend changes
- Rewriting the PostgreSQL storage layer
- Arduino-level sensor abstractions as the sole implementation
- OTA/bootloader before E1–E8 gates pass

## 11. Related documents

- [Embedded architecture](embedded-architecture.md) — components and boundaries
- [Embedded protocol sketch](embedded-protocol.md) — framing and CRC
- [Embedded failure model](embedded-failure-model.md) — faults and recovery
- [Current status](current-status.md) — M0–M7 + E-milestone ledger
- [Implementation plan](implementation-plan.md) — distributed phases (complete)
