# ADR 0003: Begin with a Modular Control Plane

- Status: accepted
- Date: 2026-08-27

## Context

The design has API, scheduling, storage, gateway, client, simulation, and
verification responsibilities. Splitting every responsibility into an
independent service before behavior exists would add deployment and network
failure modes without proving the core delivery model.

## Decision

The command API and scheduler begin in one Go deployable with internal package
boundaries. Gateways remain separate because connection ownership and failover
are part of the product behavior. The scenario runner, reference client, and
C++ simulator are separate executables.

## Consequences

- Transaction and retry behavior can be tested before internal RPCs are added.
- Packages cannot import across ownership boundaries except through narrow
  interfaces defined by the caller.
- A service split requires measurements or a concrete failure-isolation need
  and must preserve the documented state authority.
