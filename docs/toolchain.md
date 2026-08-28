# Toolchain

Orbit uses a conservative Go compatibility floor and current stable build
tools. Versions are pinned so local and CI behavior can be compared directly.

| Tool | Pinned version | Purpose |
|---|---:|---|
| Go | 1.26.7 | Services, runner, reference client, contract validation |
| Go compatibility CI | 1.27.0 | Detect breakage on the current Go release |
| CMake | 4.4.2 | C++20 simulator builds |
| Protobuf compiler | 35.1 | Protocol generation beginning in Phase 1 |
| protoc-gen-go | 1.36.12 | Go message bindings |
| protoc-gen-go-grpc | 1.6.2 | Go gRPC service bindings |
| MSYS2 base | 20260611 | Reproducible Windows UCRT64 compiler environment |
| PostgreSQL | 18.6 | Authoritative state store |

## Windows bootstrap

The bootstrap downloads official archives, verifies committed SHA-256 digests,
and installs them under `D:\creqate\.toolchains` by default. It does not modify
the machine-wide `PATH`.

```powershell
./scripts/bootstrap-tools.ps1
./scripts/generate.ps1
./scripts/verify.ps1
```

Pass `-ToolRoot` to both scripts to use another location. The directory must
remain outside version control.

## Linux and CI

CI uses Go setup binaries, the Ubuntu C++ toolchain, CMake, Ninja, and sanitizer
instrumentation. GitHub Actions are pinned to immutable commit SHAs. The module
is tested on both the compatibility floor and the current Go release.

## Container prerequisite

PostgreSQL development uses Docker Compose. Docker Desktop is not installed by
the bootstrap script because it is a machine-level prerequisite. Once Docker is
available:

```powershell
docker compose --env-file .env.example -f deployments/compose/compose.yaml up -d
```
