[CmdletBinding()]
param(
    [string]$ToolRoot
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not $ToolRoot) {
    $ToolRoot = Join-Path (Split-Path -Parent $repoRoot) '.toolchains'
}
$ToolRoot = [IO.Path]::GetFullPath($ToolRoot)

$versions = @{
    Go = '1.26.7'
    CMake = '4.4.2'
    Protobuf = '35.1'
    Msys2 = '20260611'
	ProtocGenGo = '1.36.12'
	ProtocGenGoGRPC = '1.6.2'
}

function Get-VerifiedDownload {
    param(
        [Parameter(Mandatory)] [string]$Name,
        [Parameter(Mandatory)] [uri]$Uri,
        [Parameter(Mandatory)] [string]$Sha256,
        [Parameter(Mandatory)] [string]$Extension
    )

    $path = Join-Path ([IO.Path]::GetTempPath()) ("orbit-{0}-{1}{2}" -f $Name, [guid]::NewGuid(), $Extension)
    Write-Host "Downloading $Name from $Uri"
    & curl.exe --fail --location --silent --show-error --output $path $Uri.AbsoluteUri
    if ($LASTEXITCODE -ne 0) {
        Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
        throw "$Name download failed with exit code $LASTEXITCODE"
    }

    $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $Sha256.ToLowerInvariant()) {
        Remove-Item -LiteralPath $path -Force
        throw "$Name checksum mismatch: expected $Sha256, got $actual"
    }
    return $path
}

function Install-Zip {
    param(
        [Parameter(Mandatory)] [string]$Name,
        [Parameter(Mandatory)] [uri]$Uri,
        [Parameter(Mandatory)] [string]$Sha256,
        [Parameter(Mandatory)] [string]$Destination,
        [Parameter(Mandatory)] [string[]]$Markers
    )

    $complete = $true
    foreach ($marker in $Markers) {
        if (-not (Test-Path -LiteralPath (Join-Path $Destination $marker))) {
            $complete = $false
            break
        }
    }
    if ($complete) {
        Write-Host "$Name is already installed at $Destination"
        return
    }

    $archive = Get-VerifiedDownload -Name $Name -Uri $Uri -Sha256 $Sha256 -Extension '.zip'
    try {
        if (Test-Path -LiteralPath $Destination) {
            throw "$Name destination exists but is incomplete: $Destination"
        }
        New-Item -ItemType Directory -Path $Destination | Out-Null
        & tar.exe -xf $archive -C $Destination
        if ($LASTEXITCODE -ne 0) {
            throw "$Name extraction failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
    }
}

New-Item -ItemType Directory -Force -Path $ToolRoot | Out-Null

$goRoot = Join-Path $ToolRoot "go-$($versions.Go)"
Install-Zip `
    -Name "Go $($versions.Go)" `
    -Uri "https://go.dev/dl/go$($versions.Go).windows-amd64.zip" `
    -Sha256 'f4f534a486e4bc3387fa18f08208f2f854b7aaea8a08f2a2d829a914a05abb11' `
    -Destination $goRoot `
    -Markers @('go\bin\go.exe', 'go\src\runtime\runtime.go')

$cmakeRoot = Join-Path $ToolRoot "cmake-$($versions.CMake)"
Install-Zip `
    -Name "CMake $($versions.CMake)" `
    -Uri "https://github.com/Kitware/CMake/releases/download/v$($versions.CMake)/cmake-$($versions.CMake)-windows-x86_64.zip" `
    -Sha256 'e8139d85b3813bc38833142ae1940472e9a587e9b5d2718ac1804c60f4e57a64' `
    -Destination $cmakeRoot `
    -Markers @("cmake-$($versions.CMake)-windows-x86_64\bin\cmake.exe")

$protobufRoot = Join-Path $ToolRoot "protobuf-$($versions.Protobuf)"
Install-Zip `
    -Name "Protobuf $($versions.Protobuf)" `
    -Uri "https://github.com/protocolbuffers/protobuf/releases/download/v$($versions.Protobuf)/protoc-$($versions.Protobuf)-win64.zip" `
    -Sha256 '5d3ff218d7d91eea95f7569bcb5a98f3030f8996d44151279d9772edcff76082' `
    -Destination $protobufRoot `
    -Markers @('bin\protoc.exe', 'include\google\protobuf\descriptor.proto')

$go = Join-Path $goRoot 'go\bin\go.exe'
$goBin = Join-Path $ToolRoot 'go-bin'
New-Item -ItemType Directory -Force -Path $goBin | Out-Null
$env:GOBIN = $goBin
if (-not (Test-Path -LiteralPath (Join-Path $goBin 'protoc-gen-go.exe'))) {
    & $go install "google.golang.org/protobuf/cmd/protoc-gen-go@v$($versions.ProtocGenGo)"
    if ($LASTEXITCODE -ne 0) {
        throw "protoc-gen-go installation failed with exit code $LASTEXITCODE"
    }
}
if (-not (Test-Path -LiteralPath (Join-Path $goBin 'protoc-gen-go-grpc.exe'))) {
    & $go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v$($versions.ProtocGenGoGRPC)"
    if ($LASTEXITCODE -ne 0) {
        throw "protoc-gen-go-grpc installation failed with exit code $LASTEXITCODE"
    }
}

$msysRoot = Join-Path $ToolRoot 'msys64'
$bash = Join-Path $msysRoot 'usr\bin\bash.exe'
if (-not (Test-Path -LiteralPath $bash)) {
    $archive = Get-VerifiedDownload `
        -Name "MSYS2 $($versions.Msys2)" `
        -Uri "https://github.com/msys2/msys2-installer/releases/download/2026-06-11/msys2-base-x86_64-$($versions.Msys2).tar.xz" `
        -Sha256 'a2d047e8ee213c3c6a49a8de427eb1069df12207c0422ff1b3cbb5c905c34221' `
        -Extension '.tar.xz'
    try {
        & tar.exe -xf $archive -C $ToolRoot
        if ($LASTEXITCODE -ne 0) {
            throw "MSYS2 extraction failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
    }
}

$gcc = Join-Path $msysRoot 'ucrt64\bin\g++.exe'
$ninja = Join-Path $msysRoot 'ucrt64\bin\ninja.exe'
if (-not (Test-Path -LiteralPath $gcc) -or -not (Test-Path -LiteralPath $ninja)) {
    Write-Host 'Installing the pinned MSYS2 UCRT64 C++ toolchain'
    $env:MSYSTEM = 'UCRT64'
    $env:CHERE_INVOKING = '1'
    & $bash -lc 'pacman -Sy --needed --noconfirm mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-ninja'
    if ($LASTEXITCODE -ne 0) {
        throw "MSYS2 package installation failed with exit code $LASTEXITCODE"
    }
}

Write-Host ''
Write-Host 'Orbit toolchain is ready:'
Write-Host "  Go:       $(Join-Path $goRoot 'go\bin\go.exe')"
Write-Host "  CMake:    $(Join-Path $cmakeRoot "cmake-$($versions.CMake)-windows-x86_64\bin\cmake.exe")"
Write-Host "  Protobuf: $(Join-Path $protobufRoot 'bin\protoc.exe')"
Write-Host "  C++:      $gcc"
Write-Host "  Ninja:    $ninja"
