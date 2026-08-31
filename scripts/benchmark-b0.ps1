<#
.SYNOPSIS
Runs the B0 harness-calibration benchmark against a live Orbit stack.
#>
[CmdletBinding()]
param(
    [string]$ToolRoot,
    [string]$DatabaseURL = 'postgres://orbit:orbit-local-only@127.0.0.1:5432/orbit?sslmode=disable',
    [string]$ConfigPath = 'benchmarks/b0-harness-calibration.v1.json',
    [string]$OutputPath = 'docs/results/b0-harness-calibration/summary.json'
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
    if ($installed) { return $installed.Source }
    if (Test-Path -LiteralPath $Fallback) { return $Fallback }
    throw "Required tool '$Command' was not found. Run scripts/bootstrap-tools.ps1."
}

function Wait-OrbitPort {
    param([Parameter(Mandatory)] [int]$Port, [Parameter(Mandatory)] [int]$Seconds)
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $probe = New-Object Net.Sockets.TcpClient
            $probe.Connect('127.0.0.1', $Port)
            $probe.Close()
            return $true
        } catch {
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

function Stop-OrbitListeners {
    foreach ($port in 50051, 50052) {
        $connections = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue
        foreach ($connection in $connections) {
            Stop-Process -Id $connection.OwningProcess -Force -ErrorAction SilentlyContinue
        }
    }
}

$go = Resolve-OrbitTool -Command 'go' -Fallback (Join-Path $ToolRoot 'go-1.26.7\go\bin\go.exe')
$env:GOTOOLCHAIN = 'local'
$binDirectory = Join-Path $repoRoot 'build\go'
$runDirectory = Join-Path $repoRoot 'build\benchmark-b0'
$stateRoot = Join-Path $runDirectory 'client-state'
$processes = [Collections.Generic.List[Diagnostics.Process]]::new()

Push-Location $repoRoot
try {
    Stop-OrbitListeners
    Remove-Item -LiteralPath $runDirectory -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $binDirectory, $stateRoot, (Split-Path $OutputPath) | Out-Null

    Write-Host '==> building release binaries'
    & $go build -trimpath -ldflags '-s -w' -o ./build/go/ ./cmd/...
    if ($LASTEXITCODE -ne 0) { throw "build failed with exit code $LASTEXITCODE" }

    Write-Host '==> applying migrations'
    & (Join-Path $binDirectory 'orbit-migrate.exe') -direction up -database-url $DatabaseURL
    if ($LASTEXITCODE -ne 0) { throw "migration failed with exit code $LASTEXITCODE" }

    Write-Host '==> resetting benchmark database tables'
    wsl -e docker exec orbit-postgres-1 psql -U orbit -d orbit -v ON_ERROR_STOP=1 -c `
        "TRUNCATE orbit.audit_events, orbit.delivery_attempts, orbit.commands, orbit.device_cursors RESTART IDENTITY CASCADE;"
    if ($LASTEXITCODE -ne 0) { throw "database reset failed with exit code $LASTEXITCODE" }

    $env:ORBIT_DATABASE_URL = $DatabaseURL
    $env:ORBIT_LISTEN_ADDRESS = '127.0.0.1:50051'
    $env:ORBIT_METRICS_ADDRESS = '127.0.0.1:9090'
    $env:ORBIT_OTEL_ENABLED = 'false'
    $env:ORBIT_GATEWAY_ID = 'gateway-bench'
    $env:ORBIT_CONTROL_ADDRESS = '127.0.0.1:50051'
    $env:ORBIT_GATEWAY_LISTEN_ADDRESS = '127.0.0.1:50052'
    $env:ORBIT_DB_MAX_CONNECTIONS = '32'
    $env:ORBIT_GLOBAL_ADMISSION_LIMIT = '100000'
    $env:ORBIT_PER_DEVICE_ADMISSION_LIMIT = '1024'
    $env:ORBIT_GATEWAY_MAX_RECONNECT_ATTEMPTS = '0'

    $processes.Add((Start-OrbitProcess -Name 'orbitd' -Path (Join-Path $binDirectory 'orbitd.exe') -LogDirectory $runDirectory))
    if (-not (Wait-OrbitPort -Port 50051 -Seconds 45)) { throw 'orbitd did not open port 50051' }

    $env:ORBIT_METRICS_ADDRESS = '127.0.0.1:9092'
    $processes.Add((Start-OrbitProcess -Name 'gateway' -Path (Join-Path $binDirectory 'gateway.exe') -LogDirectory $runDirectory))
    if (-not (Wait-OrbitPort -Port 50052 -Seconds 45)) { throw 'gateway did not open port 50052' }

    Write-Host '==> running orbit-bench'
    & (Join-Path $binDirectory 'orbit-bench.exe') -config $ConfigPath -output $OutputPath -state-root $stateRoot
    if ($LASTEXITCODE -ne 0) { throw "orbit-bench failed with exit code $LASTEXITCODE" }

    Write-Host "==> wrote $OutputPath"
}
finally {
    foreach ($process in $processes) {
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Stop-OrbitListeners
    Pop-Location
}
