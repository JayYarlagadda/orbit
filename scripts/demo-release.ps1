<#
.SYNOPSIS
Portfolio demo script for reviewers: smoke path, scenario, and observability pointers.
#>
[CmdletBinding()]
param(
    [string]$ToolRoot
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot

Write-Host @"
Orbit release demo
==================

This script runs the portfolio checks that do not require a long benchmark.
For the published B0 throughput number, run scripts/benchmark-b0.ps1 separately.

"@

& (Join-Path $PSScriptRoot 'smoke-online.ps1') -ToolRoot $ToolRoot

Push-Location $repoRoot
try {
    $go = if ($ToolRoot) {
        Join-Path $ToolRoot 'go-1.26.7\go\bin\go.exe'
    } else {
        (Get-Command go).Source
    }
    $env:GOTOOLCHAIN = 'local'
    $env:ORBIT_DATABASE_URL = 'postgres://orbit:orbit-local-only@127.0.0.1:5432/orbit?sslmode=disable'

    Write-Host '==> scenario-run online-smoke'
    & $go run ./cmd/scenario-run -scenario scenarios/examples/online-smoke.v1.json
    if ($LASTEXITCODE -ne 0) { throw "scenario-run failed with exit code $LASTEXITCODE" }
}
finally {
    Pop-Location
}

Write-Host @"

Next steps for reviewers
------------------------
- Benchmark results: docs/results/b0-harness-calibration/summary.json
- Claim evidence:    docs/release.md
- Operations guide:  docs/operations.md
- Grafana:           http://localhost:3000 (after docker compose up)
- Jaeger:            http://localhost:16686

"@
