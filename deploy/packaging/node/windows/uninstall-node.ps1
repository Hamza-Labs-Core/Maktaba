<#
.SYNOPSIS
    Removes this machine's Maktaba node worker on Windows (Epic 31).

.DESCRIPTION
    Stops and removes the maktaba-node / maktaba-node@<N> scheduled tasks (and NSSM
    services, which needs an elevated PowerShell), stops any worker left running, and
    deletes the worker's Python environment (%LOCALAPPDATA%\Maktaba\node). -Purge also
    deletes the logs and the node*.env settings.

    The node config (%APPDATA%\Maktaba\node.json: this machine's identity and the home's
    credentials) is kept. Remove the machine on the home (Admin -> Machines), then delete
    that file to forget the home here.

.PARAMETER Purge
    Also delete the logs (%LOCALAPPDATA%\Maktaba\Logs\node*) and node*.env.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File deploy\packaging\node\windows\uninstall-node.ps1
#>
[CmdletBinding()]
param(
    [switch]$Purge
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'node-common.ps1')

$services = @(Get-Service -Name 'maktaba-node', 'maktaba-node@*' -ErrorAction SilentlyContinue)
if ($services.Count -gt 0 -and -not (Test-IsAdministrator)) {
    $names = ($services | ForEach-Object { $_.Name }) -join ', '
    throw "services $names are installed: run this from an elevated PowerShell (Run as administrator) to remove them"
}

$tasks = @(Get-ScheduledTask -TaskPath '\' -ErrorAction SilentlyContinue |
    Where-Object { $_.TaskName -eq 'maktaba-node' -or $_.TaskName -like 'maktaba-node@*' })
foreach ($t in $tasks) {
    Write-NodeStep "Stopping and removing the task $($t.TaskName)"
    Stop-ScheduledTask -TaskPath '\' -TaskName $t.TaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskPath '\' -TaskName $t.TaskName -Confirm:$false
}

if ($services.Count -gt 0) {
    $nssm = Get-Command nssm.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    $sc = Join-Path $env:SystemRoot 'System32\sc.exe'
    foreach ($s in $services) {
        Write-NodeStep "Stopping and removing the service $($s.Name)"
        Stop-Service -Name $s.Name -Force -ErrorAction SilentlyContinue
        if ($nssm) {
            $code = Invoke-NativeCommand -FilePath $nssm.Path -ArgumentList @('remove', $s.Name, 'confirm')
        } else {
            $code = Invoke-NativeCommand -FilePath $sc -ArgumentList @('delete', $s.Name)
        }
        if ($code -ne 0) { Write-Warning "removing the service $($s.Name) failed (exit $code)" }
    }
}

# Workers a stopped task left behind (normally none: they die with their task).
Stop-NodeWorkerProcesses

if (Test-Path -LiteralPath $script:NodeDir) {
    Write-NodeStep "Removing the worker's Python environment ($($script:NodeDir))"
    try {
        Remove-Item -LiteralPath $script:NodeDir -Recurse -Force
    } catch {
        Start-Sleep -Seconds 3   # a just-killed Python may still hold a file
        Remove-Item -LiteralPath $script:NodeDir -Recurse -Force
    }
}

if ($Purge) {
    Write-NodeStep 'Removing settings (node*.env) and logs'
    foreach ($pattern in @('node.env', 'node@*.env')) {
        Get-ChildItem -LiteralPath $script:NodeConfDir -Filter $pattern -File -ErrorAction SilentlyContinue |
            Remove-Item -Force
    }
    foreach ($pattern in @('node.log*', 'node.err.log*', 'node@*.log*')) {
        Get-ChildItem -LiteralPath $script:NodeLogDir -Filter $pattern -File -ErrorAction SilentlyContinue |
            Remove-Item -Force
    }
}

Write-Host ''
Write-NodeStep 'Removed. The node config (node.json) is kept: it is this machine''s identity on the home.'
Write-NodeStep "Remove the machine on the home (Admin -> Machines), then delete $(Join-Path $script:NodeConfDir 'node.json') to forget the home here."
