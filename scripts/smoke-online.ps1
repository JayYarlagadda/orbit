<#
.SYNOPSIS
Runs the online producer-to-device-to-acknowledgement path across separate
orbitd, gateway, and client processes, then restarts orbitd and repeats.

.DESCRIPTION
Builds the executables, starts each component as its own process, submits one
command with orbitctl, and waits for the durable state to reach ACKNOWLEDGED.
Built executables are used rather than "go run" because a Ctrl+C delivered to
the "go run" wrapper on Windows can leave the child process alive.
#>
[CmdletBinding()]
param(
    [string]$ToolRoot,
    [string]$DatabaseURL = 'postgres://orbit:orbit-local-only@127.0.0.1:5432/orbit?sslmode=disable',
    [string]$DeviceID = 'edge-smoke',
    [string]$GatewayID = 'gateway-smoke',
    [int]$TimeoutSeconds = 60
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not $ToolRoot) {
    $ToolRoot = Join-Path (Split-Path -Parent $repoRoot) '.toolchains'
}
$ToolRoot = [IO.Path]::GetFullPath($ToolRoot)

function Resolve-OrbitTool {
    param(
        [Parameter(Mandatory)] [string]$Command,
        [Parameter(Mandatory)] [string]$Fallback
    )

    $installed = Get-Command $Command -ErrorAction SilentlyContinue
    if ($installed) {
        return $installed.Source
    }
    if (Test-Path -LiteralPath $Fallback) {
        return $Fallback
    }
    throw "Required tool '$Command' was not found. Run scripts/bootstrap-tools.ps1."
}

function Wait-OrbitPort {
    param(
        [Parameter(Mandatory)] [int]$Port,
        [Parameter(Mandatory)] [int]$Seconds
    )

    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $probe = New-Object Net.Sockets.TcpClient
            $probe.Connect('127.0.0.1', $Port)
            $probe.Close()
            return $true
        }
        catch {
            Start-Sleep -Milliseconds 150
        }
    }
    return $false
}

function Start-OrbitProcess {
    param(
        [Parameter(Mandatory)] [string]$Name,
        [Parameter(Mandatory)] [string]$Path,
        [Parameter(Mandatory)] [string]$LogDirectory
    )

    $process = Start-Process -FilePath $Path -PassThru -NoNewWindow `
        -RedirectStandardOutput (Join-Path $LogDirectory "$Name.out.log") `
        -RedirectStandardError (Join-Path $LogDirectory "$Name.err.log")
    Write-Host "==> started $Name (pid $($process.Id))"
    return $process
}

$go = Resolve-OrbitTool -Command 'go' -Fallback (Join-Path $ToolRoot 'go-1.26.7\go\bin\go.exe')
$env:GOTOOLCHAIN = 'local'

$runDirectory = Join-Path $repoRoot 'build\smoke'
$binDirectory = Join-Path $repoRoot 'build\go'
$statePath = Join-Path $runDirectory 'client-state.json'
$processes = [Collections.Generic.List[Diagnostics.Process]]::new()

Push-Location $repoRoot
try {
    Remove-Item -LiteralPath $runDirectory -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $runDirectory | Out-Null
    New-Item -ItemType Directory -Force -Path $binDirectory | Out-Null

    Write-Host '==> building executables'
    & $go build -o ./build/go/ ./cmd/...
    if ($LASTEXITCODE -ne 0) { throw "build failed with exit code $LASTEXITCODE" }

    Write-Host '==> applying migrations'
    & (Join-Path $binDirectory 'orbit-migrate.exe') -direction up -database-url $DatabaseURL
    if ($LASTEXITCODE -ne 0) { throw "migration failed with exit code $LASTEXITCODE" }

    $env:ORBIT_DATABASE_URL = $DatabaseURL
    $env:ORBIT_LISTEN_ADDRESS = '127.0.0.1:50051'
    $env:ORBIT_GATEWAY_ID = $GatewayID
    $env:ORBIT_CONTROL_ADDRESS = '127.0.0.1:50051'
    $env:ORBIT_GATEWAY_LISTEN_ADDRESS = '127.0.0.1:50052'
    $env:ORBIT_DEVICE_ID = $DeviceID
    $env:ORBIT_CLIENT_GATEWAY_ADDRESS = '127.0.0.1:50052'
    $env:ORBIT_CLIENT_STATE_PATH = $statePath
    $env:ORBIT_CLIENT_MAX_RECONNECT_ATTEMPTS = '5'
    $env:ORBIT_GATEWAY_MAX_RECONNECT_ATTEMPTS = '0'
    $env:ORBIT_GATEWAY_RECONNECT_INITIAL_DELAY = '50ms'
    $env:ORBIT_GATEWAY_RECONNECT_MAX_DELAY = '1s'

    $orbitd = Start-OrbitProcess -Name 'orbitd' -Path (Join-Path $binDirectory 'orbitd.exe') -LogDirectory $runDirectory
    $processes.Add($orbitd)
    if (-not (Wait-OrbitPort -Port 50051 -Seconds 20)) { throw 'orbitd did not begin listening on 50051' }

    $processes.Add((Start-OrbitProcess -Name 'gateway' -Path (Join-Path $binDirectory 'gateway.exe') -LogDirectory $runDirectory))
    if (-not (Wait-OrbitPort -Port 50052 -Seconds 20)) { throw 'gateway did not begin listening on 50052' }

    $processes.Add((Start-OrbitProcess -Name 'client' -Path (Join-Path $binDirectory 'client.exe') -LogDirectory $runDirectory))

    # The device session must be registered before a command can be leased,
    # because leasing only selects devices owned by the requesting gateway.
    Write-Host '==> waiting for the device session'
    $sessionDeadline = (Get-Date).AddSeconds(20)
    $sessionReady = $false
    while ((Get-Date) -lt $sessionDeadline) {
        if (Test-Path -LiteralPath $statePath) { $sessionReady = $true; break }
        Start-Sleep -Milliseconds 200
    }
    if (-not $sessionReady) { throw 'the client never persisted a device session' }

    $orbitctl = Join-Path $binDirectory 'orbitctl.exe'
    $idempotencyKey = "smoke-$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())"
    Write-Host '==> submitting a command'
    $submitted = & $orbitctl submit -producer smoke-producer -idempotency-key $idempotencyKey `
        -device $DeviceID -priority 4 -payload collect-diagnostics -expires-after 1h | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) { throw "submit failed with exit code $LASTEXITCODE" }
    $commandID = $submitted.command_id
    Write-Host "==> submitted $commandID"

    Write-Host '==> waiting for the durable ACKNOWLEDGED state'
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $state = ''
    while ((Get-Date) -lt $deadline) {
        $current = & $orbitctl get -command-id $commandID | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0) { throw "get failed with exit code $LASTEXITCODE" }
        $state = $current.state
        if ($state -eq 'COMMAND_STATE_ACKNOWLEDGED') { break }
        Start-Sleep -Milliseconds 250
    }
    if ($state -ne 'COMMAND_STATE_ACKNOWLEDGED') {
        throw "command $commandID reached state '$state' instead of COMMAND_STATE_ACKNOWLEDGED"
    }

    Write-Host "==> command $commandID is durably ACKNOWLEDGED"

    Write-Host '==> restarting orbitd while the gateway stays up'
    Stop-Process -Id $orbitd.Id -Force
    $orbitd.WaitForExit(10000) | Out-Null
    $orbitd = Start-OrbitProcess -Name 'orbitd-restart' -Path (Join-Path $binDirectory 'orbitd.exe') -LogDirectory $runDirectory
    $processes.Add($orbitd)
    if (-not (Wait-OrbitPort -Port 50051 -Seconds 20)) { throw 'restarted orbitd did not begin listening on 50051' }

    # The control plane released device sessions on the previous stream, so the
    # client must re-register before a command can be leased again.
    Start-Sleep -Seconds 2
    $restartKey = "smoke-restart-$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())"
    Write-Host '==> submitting a command after the orbitd restart'
    $restarted = & $orbitctl submit -producer smoke-producer -idempotency-key $restartKey `
        -device $DeviceID -priority 4 -payload collect-diagnostics-after-restart -expires-after 1h | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) { throw "submit after restart failed with exit code $LASTEXITCODE" }
    $restartCommandID = $restarted.command_id
    Write-Host "==> submitted $restartCommandID"

    Write-Host '==> waiting for the post-restart ACKNOWLEDGED state'
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $state = ''
    while ((Get-Date) -lt $deadline) {
        $current = & $orbitctl get -command-id $restartCommandID | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0) { throw "get after restart failed with exit code $LASTEXITCODE" }
        $state = $current.state
        if ($state -eq 'COMMAND_STATE_ACKNOWLEDGED') { break }
        Start-Sleep -Milliseconds 250
    }
    if ($state -ne 'COMMAND_STATE_ACKNOWLEDGED') {
        throw "command $restartCommandID reached state '$state' instead of COMMAND_STATE_ACKNOWLEDGED after orbitd restart"
    }

    Write-Host "==> command $restartCommandID is durably ACKNOWLEDGED after orbitd restart"
    Write-Host "Orbit online smoke path passed. Logs are in $runDirectory."
}
finally {
    foreach ($process in $processes) {
        if ($process -and -not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Pop-Location
}
