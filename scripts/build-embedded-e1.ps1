[CmdletBinding()]
param(
    [string]$EmbeddedRoot,
    [string]$Board = 'nucleo_h743zi',
    [switch]$Flash
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not $EmbeddedRoot) {
    $EmbeddedRoot = Join-Path $repoRoot 'embedded'
}
$EmbeddedRoot = [IO.Path]::GetFullPath($EmbeddedRoot)
$appDir = Join-Path $EmbeddedRoot 'app'
$buildDir = Join-Path $EmbeddedRoot 'build'

if (-not (Test-Path -LiteralPath (Join-Path $EmbeddedRoot '.west'))) {
    Write-Host "West workspace not initialized. Run ./scripts/bootstrap-embedded.ps1 first."
    exit 1
}

Push-Location $EmbeddedRoot
try {
  $args = @('build', '-b', $Board, '-d', $buildDir, 'app')
  & west @args
  if ($LASTEXITCODE -ne 0) { throw "west build failed with exit code $LASTEXITCODE" }

  if ($Flash) {
    & west flash -d $buildDir
    if ($LASTEXITCODE -ne 0) { throw "west flash failed with exit code $LASTEXITCODE" }
  }
}
finally {
    Pop-Location
}

Write-Host ""
Write-Host "Build complete: $buildDir"
if (-not $Flash) {
    Write-Host "Flash with: ./scripts/build-embedded-e1.ps1 -Flash"
}
