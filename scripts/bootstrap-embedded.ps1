[CmdletBinding()]
param(
    [string]$EmbeddedRoot
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not $EmbeddedRoot) {
    $EmbeddedRoot = Join-Path $repoRoot 'embedded'
}
$EmbeddedRoot = [IO.Path]::GetFullPath($EmbeddedRoot)

function Test-CommandAvailable {
    param([Parameter(Mandatory)] [string]$Name)
    return [bool](Get-Command $Name -ErrorAction SilentlyContinue)
}

Write-Host "Orbit embedded (E1) prerequisite check"
Write-Host "Workspace: $EmbeddedRoot"
Write-Host ""

$missing = @()
foreach ($tool in @('git', 'python', 'cmake', 'ninja', 'west')) {
    if (-not (Test-CommandAvailable $tool)) {
        $missing += $tool
    }
}

if ($missing.Count -gt 0) {
    Write-Host "Missing tools: $($missing -join ', ')"
    Write-Host ""
    Write-Host "Install Zephyr SDK + west per:"
    Write-Host "  https://docs.zephyrproject.org/latest/develop/getting_started/index.html"
    Write-Host ""
    Write-Host "Windows summary:"
    Write-Host "  1. Install Python 3.11+ and Git"
    Write-Host "  2. pip install west"
    Write-Host "  3. Install Zephyr SDK (includes cmake, ninja, toolchains)"
    Write-Host "  4. Set ZEPHYR_SDK_INSTALL_DIR and activate Zephyr environment"
    exit 1
}

if (-not (Test-Path -LiteralPath (Join-Path $EmbeddedRoot 'west.yml'))) {
    throw "west.yml not found under $EmbeddedRoot"
}

$westDir = Join-Path $EmbeddedRoot '.west'
if (-not (Test-Path -LiteralPath $westDir)) {
    Write-Host "Initializing west workspace..."
    Push-Location $EmbeddedRoot
    try {
        & west init -l .
        if ($LASTEXITCODE -ne 0) { throw "west init failed with exit code $LASTEXITCODE" }
    }
    finally {
        Pop-Location
    }
}

Write-Host "Updating west workspace (this may take several minutes on first run)..."
Push-Location $EmbeddedRoot
try {
    & west update
    if ($LASTEXITCODE -ne 0) { throw "west update failed with exit code $LASTEXITCODE" }
}
finally {
    Pop-Location
}

Write-Host ""
Write-Host "West workspace ready."
Write-Host "Build E1 firmware:"
Write-Host "  ./scripts/build-embedded-e1.ps1"
