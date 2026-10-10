# Build: web -> embed -> Go binary
# Usage: powershell -ExecutionPolicy Bypass -File scripts/build.ps1 [-SkipWeb] [-Test] [-Os linux] [-Arch amd64]
param([switch]$SkipWeb, [switch]$Test, [string]$Os = '', [string]$Arch = '')
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        $local = Join-Path $env:USERPROFILE 'sdk\go1.23.4\go\bin'
        if (Test-Path $local) { $env:PATH = "$local;$env:PATH" } else { throw 'Go toolchain not found' }
    }
    if (-not $env:GOPROXY) { $env:GOPROXY = 'https://goproxy.cn,direct' }
    if (-not $SkipWeb) {
        if (-not (Test-Path web/node_modules)) { npm --prefix web install }
        npm --prefix web run build
        if ($LASTEXITCODE) { throw 'web build failed' }
    }
    if ($Test) { go test ./...; if ($LASTEXITCODE) { throw 'go test failed' } }
    $ver = (git describe --tags --always --dirty 2>$null); if (-not $ver) { $ver = 'dev' }
    $out = 'bin/poolsvc.exe'
    if ($Os) { $env:GOOS = $Os; if ($Os -ne 'windows') { $out = "bin/poolsvc-$Os-$(if ($Arch) { $Arch } else { 'amd64' })" } }
    if ($Arch) { $env:GOARCH = $Arch }
    $env:CGO_ENABLED = '0'
    go build -trimpath -ldflags "-s -w -X github.com/orangeruan/poolservice/internal/panelapi.Version=$ver" -o $out ./cmd/poolsvc
    if ($LASTEXITCODE) { throw 'go build failed' }
    Write-Host "built $out ($ver)"
} finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}
