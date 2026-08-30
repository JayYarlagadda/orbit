# API and Configuration

## Command API

The source contract is `proto/orbit/v1/command.proto`. Generated Go bindings
are committed under `gen/orbit/v1` and verified against the source during local
foundation verification.

`SubmitCommand` accepts a producer-scoped idempotency key, target device,
priority, payload, and absolute expiration timestamp. The server owns the
command UUID, device sequence, state, and audit time. Repeating the same
normalized request returns the original command. Reusing the key with different
device, priority, payload, or expiration returns `AlreadyExists`.

`GetCommand` returns authoritative durable state. `CancelCommand` is idempotent
after cancellation but succeeds initially only from `QUEUED` or `RETRY_WAIT`.
Once a command is leased, cancellation returns `FailedPrecondition` because a
device may already be applying it.

Current request limits:

| Field | Contract |
|---|---|
| Producer ID | 1-64 bytes; letters, digits, `.`, `_`, `:`, `-` |
| Idempotency key | 1-128 bytes; no NUL or line breaks |
| Device ID | 1-64 bytes; letters, digits, `.`, `_`, `:`, `-` |
| Priority | Integer from 0 through 9 |
| Payload | 1-65,536 bytes |
| Expiration | Must be after server time at validation and persistence |
| Command ID | RFC 4122-compatible UUID text |
| Correlation ID | Optional `x-correlation-id` metadata, at most 128 bytes |

The local server currently uses plaintext gRPC on a loopback address. TLS and
authenticated producer identity are required before any non-local deployment.

## Streaming APIs

`GatewayControlService.Connect` is the separate gateway-to-control stream. Its
first frame must be `GatewayHello`. Later frames register online devices,
release offline sessions, report delivery start, and forward acknowledgements.
The control plane responds with acquired session epochs and fenced command
assignments. One bounded writer serializes all server sends on a stream.

`DeviceService.Connect` is hosted by the standalone gateway. Its first frame
must be `DeviceHello`; later client frames are acknowledgements or heartbeats.
The gateway returns `SessionOpened` followed by `CommandDelivery` frames.
Delivery and acknowledgement frames carry both the authoritative session epoch
and the command lease token. Both sides exchange empty `Heartbeat` frames on a
bounded interval; silence longer than the configured timeout ends the stream.

The gateway and reference client both retry failed streams with bounded,
jittered backoff. Device sessions cannot outlive the control stream that
created them: an `orbitd` restart makes the gateway drop device connections so
they re-register for fresh session epochs.

## Runtime configuration

`orbitd` reads configuration once at startup and fails closed on invalid values.

| Environment variable | Default | Constraint |
|---|---|---|
| `ORBIT_DATABASE_URL` | none | Required PostgreSQL URL |
| `ORBIT_LISTEN_ADDRESS` | `127.0.0.1:50051` | Non-empty TCP listen address |
| `ORBIT_SHUTDOWN_TIMEOUT` | `10s` | 1 second through 2 minutes |
| `ORBIT_DB_MAX_CONNECTIONS` | `10` | 1 through 100 |
| `ORBIT_GATEWAY_OUTBOUND_BUFFER` | `128` | 1 through 4,096 frames |
| `ORBIT_SCHEDULER_LEASE_BATCH` | `32` | 1 through 256 commands |
| `ORBIT_SCHEDULER_SWEEP_BATCH` | `64` | 1 through 256 commands |
| `ORBIT_SCHEDULER_LEASE_DURATION` | `15s` | 1 second through 5 minutes |
| `ORBIT_SCHEDULER_POLL_INTERVAL` | `250ms` | 10 milliseconds through 1 minute |
| `ORBIT_MAX_DELIVERY_ATTEMPTS` | `5` | 1 through 100 lease attempts before dead letter |
| `ORBIT_RETRY_BASE_DELAY` | `250ms` | 10 milliseconds through 1 minute |
| `ORBIT_RETRY_MAX_DELAY` | `30s` | 1 second through 30 minutes |
| `ORBIT_GLOBAL_ADMISSION_LIMIT` | `10000` | 1 through 1000000 outstanding commands |
| `ORBIT_PER_DEVICE_ADMISSION_LIMIT` | `256` | 1 through 100000 outstanding commands per device |
| `ORBIT_CONTROL_HEARTBEAT_INTERVAL` | `5s` | 10 milliseconds through 1 minute |
| `ORBIT_CONTROL_HEARTBEAT_TIMEOUT` | `15s` | 100 milliseconds through 5 minutes, and not below the interval |

The gateway process reads a separate bounded configuration:

| Environment variable | Default | Constraint |
|---|---|---|
| `ORBIT_GATEWAY_ID` | none | Required 1-64 byte identifier |
| `ORBIT_CONTROL_ADDRESS` | `127.0.0.1:50051` | Non-empty control-plane address |
| `ORBIT_GATEWAY_LISTEN_ADDRESS` | `127.0.0.1:50052` | Non-empty device listen address |
| `ORBIT_GATEWAY_SHUTDOWN_TIMEOUT` | `10s` | 1 second through 2 minutes |
| `ORBIT_GATEWAY_CONTROL_BUFFER` | `256` | 1 through 4,096 frames |
| `ORBIT_DEVICE_CONNECTION_BUFFER` | `16` | 1 through 256 frames per device |
| `ORBIT_GATEWAY_MAX_RECONNECT_ATTEMPTS` | `0` | 0 through 1,000; 0 retries while the process runs |
| `ORBIT_GATEWAY_RECONNECT_INITIAL_DELAY` | `250ms` | 10 milliseconds through 10 seconds |
| `ORBIT_GATEWAY_RECONNECT_MAX_DELAY` | `10s` | 100 milliseconds through 2 minutes, and not below the initial delay |
| `ORBIT_GATEWAY_HEARTBEAT_INTERVAL` | `5s` | 10 milliseconds through 1 minute |
| `ORBIT_GATEWAY_HEARTBEAT_TIMEOUT` | `15s` | 100 milliseconds through 5 minutes, and not below the interval |

The reference client process reads its own bounded configuration:

| Environment variable | Default | Constraint |
|---|---|---|
| `ORBIT_DEVICE_ID` | none | Required 1-64 byte identifier |
| `ORBIT_CLIENT_INSTANCE_ID` | generated | Optional 1-64 byte identifier |
| `ORBIT_CLIENT_GATEWAY_ADDRESS` | `127.0.0.1:50052` | Non-empty gateway address |
| `ORBIT_CLIENT_STATE_PATH` | `data/orbit-client-state.json` | Non-empty durable state path |
| `ORBIT_CLIENT_DEDUP_RETENTION` | `1024` | 1 through 100,000 retained command IDs |
| `ORBIT_CLIENT_MAX_RECONNECT_ATTEMPTS` | `0` | 0 through 1,000; 0 retries while the process runs |
| `ORBIT_CLIENT_RECONNECT_INITIAL_DELAY` | `250ms` | 10 milliseconds through 10 seconds |
| `ORBIT_CLIENT_RECONNECT_MAX_DELAY` | `10s` | 100 milliseconds through 2 minutes, and not below the initial delay |
| `ORBIT_CLIENT_HEARTBEAT_INTERVAL` | `5s` | 10 milliseconds through 1 minute |
| `ORBIT_CLIENT_HEARTBEAT_TIMEOUT` | `15s` | 100 milliseconds through 5 minutes, and not below the interval |

Reconnect delay doubles after each consecutive failure until it reaches the
maximum, and each wait is jittered across the upper half of its window. A
session or control stream that stays open for at least 30 seconds resets the
delay. The gateway and client share that policy.

The gRPC server limits sent and received messages to 70 KiB. The domain payload
limit is lower so envelope overhead cannot exceed the transport bound.

## Local workflow

```powershell
docker compose --env-file .env.example -f deployments/compose/compose.yaml up -d
$env:ORBIT_DATABASE_URL = 'postgres://orbit:orbit-local-only@127.0.0.1:5432/orbit?sslmode=disable'
go run ./cmd/orbit-migrate -direction up
go run ./cmd/orbitd
```

Use `orbitctl` from a second terminal. It applies a five-second request deadline,
generates correlation metadata when none is supplied, and emits protobuf JSON.
The client uses plaintext transport and is intended only for the local release.

The standalone gateway can be started after `orbitd`:

```powershell
$env:ORBIT_GATEWAY_ID = 'gateway-1'
go run ./cmd/gateway
```

The reference client connects to the gateway and persists its durable dedup
state:

```powershell
$env:ORBIT_DEVICE_ID = 'edge-1'
go run ./cmd/client
```

To exercise all three processes together against real PostgreSQL, run the
online smoke path. It builds the executables, starts each component as its own
process, submits one command, waits for durable `ACKNOWLEDGED`, restarts
`orbitd` while the gateway stays up, and asserts a second command still
reaches `ACKNOWLEDGED`:

```powershell
./scripts/smoke-online.ps1
```
