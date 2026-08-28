[CmdletBinding()]
param(
    [string]$ToolRoot,
    [switch]$Check
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not $ToolRoot) {
    $ToolRoot = Join-Path (Split-Path -Parent $repoRoot) '.toolchains'
}
$ToolRoot = [IO.Path]::GetFullPath($ToolRoot)

$protoc = Join-Path $ToolRoot 'protobuf-35.1\bin\protoc.exe'
$protobufInclude = Join-Path $ToolRoot 'protobuf-35.1\include'
$generatorBin = Join-Path $ToolRoot 'go-bin'
foreach ($tool in @($protoc, (Join-Path $generatorBin 'protoc-gen-go.exe'), (Join-Path $generatorBin 'protoc-gen-go-grpc.exe'))) {
    if (-not (Test-Path -LiteralPath $tool)) {
        throw "Required generator was not found at $tool. Run scripts/bootstrap-tools.ps1."
    }
}

$env:PATH = "$generatorBin;$env:PATH"
$generatedFiles = @(
    (Join-Path $repoRoot 'gen\orbit\v1\command.pb.go'),
	(Join-Path $repoRoot 'gen\orbit\v1\command_grpc.pb.go'),
	(Join-Path $repoRoot 'gen\orbit\v1\device.pb.go'),
	(Join-Path $repoRoot 'gen\orbit\v1\device_grpc.pb.go'),
	(Join-Path $repoRoot 'gen\orbit\v1\gateway.pb.go'),
	(Join-Path $repoRoot 'gen\orbit\v1\gateway_grpc.pb.go')
)
$before = @{}
if ($Check) {
    foreach ($file in $generatedFiles) {
        if (Test-Path -LiteralPath $file) {
            $before[$file] = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash
        }
    }
}

Push-Location $repoRoot
try {
    & $protoc `
        --proto_path=proto `
        --proto_path=$protobufInclude `
        --go_out=. `
        --go_opt=module=github.com/JayYarlagadda/orbit `
        --go-grpc_out=. `
        --go-grpc_opt=module=github.com/JayYarlagadda/orbit `
        proto/orbit/v1/command.proto `
        proto/orbit/v1/device.proto `
        proto/orbit/v1/gateway.proto
    if ($LASTEXITCODE -ne 0) {
        throw "Protocol generation failed with exit code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}

if ($Check) {
    foreach ($file in $generatedFiles) {
        if (-not $before.ContainsKey($file) -or -not (Test-Path -LiteralPath $file)) {
            throw "Generated protocol file was missing before verification: $file"
        }
        $after = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash
        if ($after -ne $before[$file]) {
            throw "Generated protocol file is stale: $file"
        }
    }
}

Write-Host 'Generated Go protocol bindings.'
