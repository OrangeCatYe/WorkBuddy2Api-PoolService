# 一键构建：前端 -> embed -> Go 二进制
# 用法：pwsh scripts/build.ps1 [-SkipWeb] [-Test]
param([switch]$SkipWeb, [switch]$Test)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        $local = Join-Path $env:USERPROFILE 'sdk\go1.23.4\go\bin'
        if (Test-Path $local) { $env:PATH = "$local;$env:PATH" } else { throw '未找到 Go 工具链' }
    }
    if (-not $env:GOPROXY) { $env:GOPROXY = 'https://goproxy.cn,direct' }
    if (-not $SkipWeb) {
        if (-not (Test-Path web/node_modules)) { npm --prefix web install --registry=https://mirrors.tencent.com/npm }
        npm --prefix web run build
        if ($LASTEXITCODE) { throw 'web build failed' }
    }
    if ($Test) { go test ./...; if ($LASTEXITCODE) { throw 'go test failed' } }
    $ver = (git describe --tags --always --dirty 2>$null); if (-not $ver) { $ver = 'dev' }
    go build -trimpath -ldflags "-s -w -X github.com/orangeruan/poolservice/internal/panelapi.Version=$ver" -o bin/poolsvc.exe ./cmd/poolsvc
    if ($LASTEXITCODE) { throw 'go build failed' }
    Write-Host "built bin/poolsvc.exe ($ver)"
} finally { Pop-Location }
