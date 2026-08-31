# Orbit embedded firmware

**Status:** E1 in progress on branch `feature/embedded-endpoint`.

Firmware for STM32H743 Nucleo + Zephyr RTOS. The distributed Go runtime is
frozen at git tag `v1.0-distributed-runtime`; all new code lives here.

## Layout

```text
embedded/
├── west.yml                 # Zephyr west manifest (v3.7.0)
├── boards/                  # Devicetree overlays
├── dts/bindings/            # Custom sensor bindings
├── drivers/imu/             # Register-level LSM6DSO SPI driver
├── acquisition/             # Bounded sample queue (E1)
├── app/                     # E1 Zephyr application
└── build/                   # west build output (gitignored)
```

## Hardware (E1)

| Signal | Nucleo pin | Notes |
|--------|------------|-------|
| SPI SCK/MISO/MOSI | Arduino D13/D12/D11 | SPI1 |
| CS | Arduino D10 (PD14) | Active low |
| INT1 | Arduino D3 (PB0) | Data-ready, active high |

Sensor: **ST LSM6DSO** on SPI (WHO_AM_I `0x6C`). ODR configured to **833 Hz**
(nearest standard rate to the 500 Hz E1 target).

## Toolchain

1. Install [Zephyr getting started](https://docs.zephyrproject.org/latest/develop/getting_started/index.html) prerequisites (Python, west, Zephyr SDK, cmake, ninja).
2. Bootstrap the west workspace:

```powershell
./scripts/bootstrap-embedded.ps1
```

3. Build (and optionally flash) E1:

```powershell
./scripts/build-embedded-e1.ps1
./scripts/build-embedded-e1.ps1 -Flash
```

4. Open serial console (115200 8N1) to see polling → ISR stats.

## E1 demo behavior

1. **Polling baseline** (`CONFIG_ORBIT_E1_POLL_DURATION_SEC`, default 5 s)
2. **ISR path** — data-ready GPIO → semaphore → SPI burst read → bounded queue
3. Per-second UART stats: enqueued, dropped, high-water, missed ISR

Record measured rates under `benchmarks/realtime/` once hardware is available.

## Related docs

- [Embedded expansion plan](../docs/embedded-expansion-plan.md) — E0–E9 milestones
- [Embedded architecture](../docs/embedded-architecture.md)
- [Embedded protocol sketch](../docs/embedded-protocol.md)
