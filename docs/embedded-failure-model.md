# Orbit embedded failure model

How the embedded node and edge gateway are expected to behave under fault. HIL
scenarios in **E7** must map to these cases.

## Fault classes

| Class | Example | Expected behavior |
|-------|---------|-------------------|
| Sensor | SPI timeout, IMU not ready | Retry, reset device, drop sample with counter, supervisor alert |
| Storage | Flash write interrupt, bad CRC | Isolate corrupt record, continue append, report `QUEUE_CORRUPTION` |
| Network | Ethernet down, CAN bus-off | Buffer to flash, stop TX, resume ordered replay on link up |
| Transport | Gateway unreachable | Exponential backoff, preserve sequence order, no duplicate ACK application |
| MCU | Watchdog reset | Persist reboot reason, replay un-ACKed records after boot |
| Gateway | Process crash | Embedded retries; backend idempotency prevents duplicate commands |
| Backend | `orbitd` restart | Existing session epoch + lease semantics (distributed runtime) |

## Recovery telemetry

After reboot, the node should expose (via health frame and/or local log):

- `WATCHDOG_RESET`
- `SPI_TIMEOUT`
- `QUEUE_CORRUPTION`
- `NETWORK_STALL`
- Last acknowledged sequence
- Queue depth and high-water mark

## HIL pass criteria (template)

Each `hil/scenarios/*.yaml` (or Python module) should assert:

- No acknowledged sequence gaps at the backend
- No duplicate application of the same sequence on the MCU after ACK
- Recovery time under a documented bound for the scenario class
- Zero steady-state heap growth on the firmware path (when instrumented)

See [embedded-expansion-plan.md](embedded-expansion-plan.md) §9 for the full
scenario matrix.
