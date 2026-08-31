[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
$ToolRoot = Join-Path (Split-Path -Parent $repoRoot) '.toolchains'
if (-not (Test-Path -LiteralPath $ToolRoot)) {
    $ToolRoot = Join-Path $repoRoot '.toolchains'
}
$goFallback = Join-Path $ToolRoot 'go-1.26.7\go\bin\go.exe'
$go = if ($env:GOEXE) { $env:GOEXE } elseif (Get-Command go -ErrorAction SilentlyContinue) { 'go' } elseif (Test-Path -LiteralPath $goFallback) { $goFallback } else { throw 'go not found' }
$env:GOTOOLCHAIN = 'local'

Push-Location $repoRoot
try {
    $configs = Get-ChildItem -Path (Join-Path $repoRoot 'benchmarks') -Filter '*.v1.json' -File
    if ($configs.Count -eq 0) {
        throw 'no benchmark configs found under benchmarks/'
    }
    foreach ($config in $configs) {
        if ($config.Name -eq 'matrix.v1.json') { continue }
        Write-Host "==> $($config.Name)"
        & $go run ./cmd/benchmark-check $config.FullName
        if ($LASTEXITCODE -ne 0) {
            throw "benchmark-check failed for $($config.Name)"
        }
    }

    $matrixPath = Join-Path $repoRoot 'benchmarks/matrix.json'
    if (-not (Test-Path -LiteralPath $matrixPath)) {
        throw 'missing benchmarks/matrix.json'
    }
    $matrix = Get-Content -LiteralPath $matrixPath -Raw | ConvertFrom-Json
    foreach ($entry in $matrix.entries) {
        if ($entry.runner -eq 'scenario') {
            $scenarioPath = Join-Path $repoRoot ($entry.scenario -replace '/', [IO.Path]::DirectorySeparatorChar)
            if (-not (Test-Path -LiteralPath $scenarioPath)) {
                throw "matrix $($entry.matrix_id) references missing scenario $scenarioPath"
            }
            Write-Host "==> matrix $($entry.matrix_id) scenario $($entry.scenario)"
            & $go run ./cmd/scenario-check $scenarioPath
            if ($LASTEXITCODE -ne 0) {
                throw "scenario-check failed for $($entry.scenario)"
            }
        }
    }
}
finally {
    Pop-Location
}

Write-Host 'All benchmark matrix entries validated.'
