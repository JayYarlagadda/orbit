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
    if ($installed) {
        return $installed.Source
    }
    if (Test-Path -LiteralPath $Fallback) {
        return $Fallback
    }
    throw "Required tool '$Command' was not found. Run scripts/bootstrap-tools.ps1."
}

function Invoke-Checked {
    param(
        [Parameter(Mandatory)] [string]$Label,
        [Parameter(Mandatory)] [scriptblock]$Action
    )

    Write-Host "==> $Label"
    & $Action
    if ($LASTEXITCODE -ne 0) {
        throw "$Label failed with exit code $LASTEXITCODE"
    }
}

function Invoke-ExpectedFailure {
    param(
        [Parameter(Mandatory)] [string]$Label,
        [Parameter(Mandatory)] [scriptblock]$Action
    )

    Write-Host "==> $Label"
    & $Action
    if ($LASTEXITCODE -eq 0) {
        throw "$Label unexpectedly succeeded"
    }
}

function Test-RepositoryText {
    $textExtensions = @(
        '.cmake', '.cpp', '.editorconfig', '.example', '.go', '.h', '.hpp',
        '.json', '.md', '.mod', '.ps1', '.sql', '.txt', '.yaml', '.yml'
    )
    $exactNames = @('CMakeLists.txt', 'Makefile')
    $failures = [Collections.Generic.List[string]]::new()
    $files = & git ls-files --cached --others --exclude-standard
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to enumerate repository files"
    }

    foreach ($relativePath in $files) {
        $name = Split-Path -Leaf $relativePath
        $extension = [IO.Path]::GetExtension($relativePath)
        if ($name -notin $exactNames -and $extension -notin $textExtensions) {
            continue
        }

        $content = [IO.File]::ReadAllText((Join-Path $repoRoot $relativePath))
        if ($content.Length -gt 0 -and -not $content.EndsWith("`n")) {
            $failures.Add("${relativePath}: missing final newline")
        }

        $lineNumber = 0
        foreach ($line in ($content -split "`n")) {
            $lineNumber++
            if ($line.TrimEnd("`r", " ", "`t").Length -ne $line.TrimEnd("`r").Length) {
                $failures.Add("${relativePath}:${lineNumber}: trailing whitespace")
            }
        }
    }

    if ($failures.Count -gt 0) {
        throw "Repository text check failed:`n$($failures -join [Environment]::NewLine)"
    }
}

$go = Resolve-OrbitTool -Command 'go' -Fallback (Join-Path $ToolRoot 'go-1.26.7\go\bin\go.exe')
$gofmt = Join-Path (Split-Path -Parent $go) 'gofmt.exe'
if (-not (Test-Path -LiteralPath $gofmt)) {
    throw "gofmt was not found next to $go"
}

$env:GOTOOLCHAIN = 'local'
Push-Location $repoRoot
try {
    Invoke-Checked -Label 'Go version' -Action { & $go version }

    $unformatted = & $gofmt -l .
    if ($LASTEXITCODE -ne 0) {
        throw "gofmt failed with exit code $LASTEXITCODE"
    }
    if ($unformatted) {
        throw "Go files require formatting:`n$($unformatted -join [Environment]::NewLine)"
    }

    Invoke-Checked -Label 'Go vet' -Action { & $go vet ./... }
    Invoke-Checked -Label 'Go tests' -Action { & $go test -count=1 ./... }
    New-Item -ItemType Directory -Force -Path (Join-Path $repoRoot 'build\go') | Out-Null
    Invoke-Checked -Label 'Go command build' -Action { & $go build -o ./build/go/ ./cmd/... }
    Invoke-Checked -Label 'Generated protocol bindings' -Action {
        & (Join-Path $PSScriptRoot 'generate.ps1') -ToolRoot $ToolRoot -Check
    }
    Invoke-Checked -Label 'Valid scenario contract' -Action {
        & $go run ./cmd/scenario-check ./scenarios/examples/offline-reconnect.v1.json
    }
    Invoke-ExpectedFailure -Label 'Invalid scenario contract' -Action {
        & $go run ./cmd/scenario-check ./scenarios/invalid/unknown-device.v1.json
    }

    if (-not $SkipCpp) {
        $cmake = Resolve-OrbitTool `
            -Command 'cmake' `
            -Fallback (Join-Path $ToolRoot 'cmake-4.4.2\cmake-4.4.2-windows-x86_64\bin\cmake.exe')
        $cppBin = Join-Path $ToolRoot 'msys64\ucrt64\bin'
        $compiler = Join-Path $cppBin 'g++.exe'
        if (-not (Test-Path -LiteralPath $compiler)) {
            throw "C++ compiler was not found. Run scripts/bootstrap-tools.ps1."
        }

        $env:PATH = "$cppBin;$env:PATH"
        $env:CC = Join-Path $cppBin 'gcc.exe'
        $env:CXX = $compiler

        Invoke-Checked -Label 'C++ configure' -Action { & $cmake --preset default --fresh }
        Invoke-Checked -Label 'C++ build' -Action { & $cmake --build --preset default }
        Invoke-Checked -Label 'C++ tests' -Action { & $cmake --build build/cpp/default --target test }
    }

    Invoke-Checked -Label 'Git whitespace check' -Action { & git diff --check }
    Write-Host '==> Repository text check'
    Test-RepositoryText
}
finally {
    Pop-Location
}

Write-Host 'Orbit foundation verification passed.'
