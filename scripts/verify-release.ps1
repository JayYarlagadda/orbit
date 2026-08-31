[CmdletBinding()]
param(
    [string]$ToolRoot,
    [switch]$SkipCpp
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

& (Join-Path $PSScriptRoot 'verify.ps1') -ToolRoot $ToolRoot @PSBoundParameters

$go = Resolve-OrbitTool -Command 'go' -Fallback (Join-Path $ToolRoot 'go-1.26.7\go\bin\go.exe')
$env:GOTOOLCHAIN = 'local'
Push-Location $repoRoot
try {
    Write-Host '==> Benchmark config contract'
    & $go run ./cmd/benchmark-check ./benchmarks/b0-harness-calibration.v1.json
    if ($LASTEXITCODE -ne 0) { throw "benchmark config check failed with exit code $LASTEXITCODE" }

    $summaryPath = Join-Path $repoRoot 'docs/results/b0-harness-calibration/summary.json'
    if (-not (Test-Path -LiteralPath $summaryPath)) {
        throw "missing committed benchmark summary at docs/results/b0-harness-calibration/summary.json"
    }

    Write-Host '==> Benchmark result schema'
    $summary = Get-Content -LiteralPath $summaryPath -Raw | ConvertFrom-Json
    if ($summary.schema_version -ne '1') {
        throw 'benchmark summary schema_version must be 1'
    }
    if ($summary.trials.Count -lt 1) {
        throw 'benchmark summary must include at least one trial'
    }
    if (-not $summary.aggregate.throughput_ack_per_second.median) {
        throw 'benchmark summary is missing aggregate throughput'
    }

    $releaseDoc = Join-Path $repoRoot 'docs/release.md'
    if (-not (Test-Path -LiteralPath $releaseDoc)) {
        throw 'missing docs/release.md'
    }
}
finally {
    Pop-Location
}

Write-Host 'Orbit release verification passed.'
