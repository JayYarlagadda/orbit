# Orbit hardware-in-the-loop (HIL)

**Status:** Not started (milestone **E7**).

Python harness for fault injection against real STM32 + edge gateway hardware.
Extends the philosophy of the C++ logical fault simulator to physical test runs.

## Planned layout

```text
hil/
├── scenarios/       # network_disconnect, mcu_reboot, …
├── runner.py        # Orchestrate faults and collect evidence
├── telemetry.py     # MCU + gateway + backend sequence alignment
└── reports/         # Pass/fail JSON for CI or manual review
```

## Prerequisites

- E4 transport + edge gateway operational
- Serial/Ethernet access to MCU diagnostics
- Documented test fixture wiring (see `docs/embedded-expansion-plan.md` §9)

## Example (future)

```bash
python hil/runner.py scenarios/network_disconnect.yaml
```
