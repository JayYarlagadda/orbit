# Orbit embedded firmware

**Status:** Not started (milestone **E1**).

Firmware for STM32H743 Nucleo + Zephyr RTOS. The distributed Go runtime is
frozen at git tag `v1.0-distributed-runtime`; all new code lives here.

## Planned layout

```text
embedded/
├── boards/          # Board support and devicetree overlays
├── drivers/imu/     # SPI IMU driver (register-level)
├── acquisition/     # ISR, DMA, sampling loop
├── storage/         # Flash circular log
├── transport/       # Ethernet (primary), CAN FD (E9)
├── health/          # Supervisor and watchdog integration
├── protocol/        # Frame encode/decode
└── app/             # Main Zephyr application
```

## Start here

1. Read [docs/embedded-expansion-plan.md](../docs/embedded-expansion-plan.md)
2. Follow **Phase 1 (E1)** only — do not skip to transport or HIL
3. Record metrics under `benchmarks/realtime/` as features land

## Toolchain (when E1 starts)

- Zephyr SDK + `west`
- OpenOCD or pyOCD for Nucleo-H743
- Logic analyzer optional for SPI verification
