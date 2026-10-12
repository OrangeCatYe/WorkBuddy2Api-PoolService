# Pre-commit local check - mirrors the CI jobs: backend + release-content + frontend.
# Not covered here: `go test -race` (needs gcc, not available locally) - left to CI.
#
# Usage (from repo root):
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/precheck.ps1
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/precheck.ps1 -Quick
#
# Exit code 0 = all passed; non-zero = at least one failure.
# Keep this file ASCII-only: PowerShell 5 reads BOM-less scripts as ANSI (GBK on
# Chinese Windows), so non-ASCII text would be mis-decoded and break parsing.
param([switch]$Quick)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# --- locate Go (not on PATH on this machine) ---
$go = $null
$gofmt = $null
$candidates = @(
    "$env:LOCALAPPDATA\Programs\wbtools\go\bin\go.exe",
    "$env:USERPROFILE\go\bin\go.exe",
    "C:\Program Files\Go\bin\go.exe",
    "C:\Go\bin\go.exe"
)
if ($env:GOROOT) { $candidates = @((Join-Path $env:GOROOT 'bin\go.exe')) + $candidates }

foreach ($c in $candidates) {
    if ($c -and (Test-Path $c)) { $go = $c; break }
}
if (-not $go) {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { $go = $cmd.Source }
}
if ($go) {
    $f = Join-Path (Split-Path $go) 'gofmt.exe'
    if (Test-Path $f) { $gofmt = $f }
}

$fail = 0
$swAll = [Diagnostics.Stopwatch]::StartNew()

function Step($name) { Write-Host "`n=== $name" -ForegroundColor Cyan }
function Ok($msg)   { Write-Host "  [OK] $msg" -ForegroundColor Green }
function Bad($msg)  { Write-Host "  [FAIL] $msg" -ForegroundColor Red; $script:fail = 1 }
function Skip($msg) { Write-Host "  [SKIP] $msg" -ForegroundColor Yellow }

# --- gofmt ---
Step 'gofmt'
if (-not $gofmt) {
    Skip 'gofmt not found'
} else {
    $u = & $gofmt -l . 2>&1
    if ($u) { Bad 'files not gofmt-clean:'; $u | ForEach-Object { Bad "    $_" } }
    else { Ok 'clean' }
}

# --- go vet ---
Step 'go vet'
if (-not $go) {
    Skip 'go not found'
} else {
    $v = & $go vet ./... 2>&1
    if ($LASTEXITCODE -eq 0) { Ok 'passed' }
    else { Bad 'go vet failed'; $v | Select-Object -First 15 | ForEach-Object { Bad "    $_" } }
}

if (-not $Quick) {
    # --- go test ---
    Step 'go test'
    if (-not $go) {
        Skip 'go not found'
    } else {
        $t = & $go test ./... 2>&1
        if ($LASTEXITCODE -eq 0) {
            $okCount = ($t | Select-String '^\s*ok\s+').Count
            Ok "passed ($okCount packages ok)"
        } else {
            Bad 'go test failed'
            $t | Select-String 'FAIL|panic:' | Select-Object -First 20 | ForEach-Object { Bad "    $_" }
        }
    }

    # --- go build ---
    Step 'go build'
    if (-not $go) {
        Skip 'go not found'
    } else {
        $b = & $go build ./... 2>&1
        if ($LASTEXITCODE -eq 0) { Ok 'passed' }
        else { Bad 'go build failed'; $b | Select-Object -First 15 | ForEach-Object { Bad "    $_" } }
    }
}

# --- release content ---
Step 'changelog sanitize'
$py = Get-Command python -ErrorAction SilentlyContinue
if (-not $py) {
    Skip 'python not found'
} else {
    $tmp = Join-Path $env:TEMP 'CHANGELOG.precheck.md'
    & python scripts/sanitize_changelog.py CHANGELOG.md $tmp 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Ok 'no internal codename left' }
    else { Bad 'residual internal codename detected' }
}

Step 'README codename scan'
if (Select-String -Path README.md -Pattern '\bmock\b' -AllMatches) {
    Bad 'internal codename found in README.md'
} else { Ok 'clean' }

# --- frontend ---
Step 'frontend typecheck'
$tc = npm --prefix web run typecheck 2>&1
if ($LASTEXITCODE -eq 0) { Ok 'passed' }
else { Bad 'typecheck failed'; $tc | Select-Object -Last 15 | ForEach-Object { Bad "    $_" } }

if (-not $Quick) {
    Step 'frontend build'
    $bd = npm --prefix web run build 2>&1
    if ($LASTEXITCODE -eq 0) { Ok 'passed' }
    else { Bad 'build failed'; $bd | Select-Object -Last 15 | ForEach-Object { Bad "    $_" } }
}

$swAll.Stop()
Write-Host "`n=====================================" -ForegroundColor Cyan
if ($fail -eq 0) {
    Write-Host "Precheck passed in $([math]::Round($swAll.Elapsed.TotalSeconds, 1))s" -ForegroundColor Green
    Write-Host 'Not covered: go test -race (needs gcc, CI only)' -ForegroundColor Yellow
} else {
    Write-Host 'Precheck FAILED - see red lines above' -ForegroundColor Red
}
exit $fail
