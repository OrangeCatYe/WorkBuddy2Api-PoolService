# PoolService autostart helper for Windows.
#
# Creates (or removes) a per-user scheduled task that starts PoolService when you log on.
# It does NOT need administrator rights, writes nothing to the registry and does not touch
# any other startup entry. Everything it does is visible in this file and in Task Scheduler.
#
# Usage (run from the folder you unpacked, or give the full path to this script):
#   powershell -NoProfile -ExecutionPolicy Bypass -File autostart\windows.ps1 install
#   powershell -NoProfile -ExecutionPolicy Bypass -File autostart\windows.ps1 install -StartNow
#   powershell -NoProfile -ExecutionPolicy Bypass -File autostart\windows.ps1 status
#   powershell -NoProfile -ExecutionPolicy Bypass -File autostart\windows.ps1 uninstall
#
# The task runs:  poolsvc.exe -background
#   -background releases the console window and writes the log to data\logs\poolsvc.log,
#   so nothing stays on screen (a console may flash for a moment at logon).
#   Software updates keep this mode: the updated service is also restarted without a window.
#
# NOTE: keep this file ASCII-only. Windows PowerShell 5 reads BOM-less files as ANSI.

param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'uninstall', 'status')]
    [string]$Action = 'status',
    [string]$TaskName = 'PoolService',
    [switch]$StartNow
)

$ErrorActionPreference = 'Stop'

# This script lives in <package>\autostart\, the program is one level up.
$Dir = Split-Path -Parent $PSScriptRoot
$Exe = Join-Path $Dir 'poolsvc.exe'
$User = "$env:USERDOMAIN\$env:USERNAME"

function Get-Task() { Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue }

switch ($Action) {
    'install' {
        if (-not (Test-Path $Exe)) { throw "poolsvc.exe not found next to the autostart folder: $Exe" }
        $act = New-ScheduledTaskAction -Execute $Exe -Argument '-background' -WorkingDirectory $Dir
        $trg = New-ScheduledTaskTrigger -AtLogOn -User $User
        # No time limit, restart up to 3 times (1 minute apart) if it crashes, never run two copies.
        $set = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
            -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) `
            -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew
        $prn = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Limited
        Register-ScheduledTask -TaskName $TaskName -Action $act -Trigger $trg -Settings $set -Principal $prn `
            -Description 'PoolService gateway, starts at logon' -Force | Out-Null
        Write-Host "Installed scheduled task '$TaskName' for $User"
        Write-Host "  program : $Exe"
        Write-Host "  log     : $(Join-Path $Dir 'data\logs\poolsvc.log')"
        if ($StartNow) {
            Start-ScheduledTask -TaskName $TaskName
            Write-Host 'Started now. If PoolService is already running, the new copy exits because the port is taken.'
        } else {
            Write-Host 'It will start at your next logon. Use -StartNow to start it immediately.'
        }
    }
    'uninstall' {
        if (Get-Task) {
            Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
            Write-Host "Removed scheduled task '$TaskName'. A running PoolService is not stopped."
        } else {
            Write-Host "Scheduled task '$TaskName' is not installed."
        }
    }
    'status' {
        $t = Get-Task
        if (-not $t) {
            Write-Host "Autostart: NOT installed (task '$TaskName')"
        } else {
            $i = Get-ScheduledTaskInfo -TaskName $TaskName
            Write-Host "Autostart: installed (task '$TaskName', state $($t.State))"
            Write-Host "  last run    : $($i.LastRunTime)"
            Write-Host "  last result : $($i.LastTaskResult)"
        }
        $p = Get-Process -Name 'poolsvc' -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $Exe }
        if ($p) { Write-Host "Running   : yes (pid $(($p | ForEach-Object Id) -join ', '))" } else { Write-Host 'Running   : no' }
    }
}
