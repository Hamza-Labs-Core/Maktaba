# What the server agent's installs rest on (agent_orders.rs), checked on a CI runner (never a
# person's PC): the agent runs as the sign-in task's process and starts the installer, which
# then stops the agent and, at its end, runs the task again (--server-start) to bring the
# server back. So a task's process that started a child and exited must
#   1. leave the child running (Task Scheduler ends no one else with it), and
#   2. let the task run again while that child still runs (the task, IgnoreNew as the
#      agent's, is not "running" because of it).
# A throwaway task here runs a script that starts a 20-second child and exits; the task is
# run twice, and both children must finish. The task is removed at the end.
$ErrorActionPreference = 'Stop'

$dir = Join-Path $env:RUNNER_TEMP ("task-children-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force $dir | Out-Null
$main = Join-Path $dir 'main.ps1'
$mainLog = Join-Path $dir 'main.log'
$childLog = Join-Path $dir 'child.log'
@"
Add-Content -LiteralPath '$mainLog' -Value "main `$PID"
Start-Process -WindowStyle Hidden -FilePath "`$env:ComSpec" -ArgumentList '/c', 'ping -n 21 127.0.0.1 >nul & echo child-done>> "$childLog"'
exit 0
"@ | Set-Content -Encoding UTF8 -LiteralPath $main

$name = 'MaktabaTaskChildrenCheck-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"$main`""
$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount
Register-ScheduledTask -TaskName $name -Action $action -Settings $settings -Principal $principal | Out-Null

function Lines($path) { if (Test-Path $path) { @(Get-Content -LiteralPath $path).Count } else { 0 } }
function WaitFor($what, $seconds, [scriptblock]$ok) {
  $end = (Get-Date).AddSeconds($seconds)
  while (-not (& $ok)) {
    if ((Get-Date) -gt $end) { throw "timed out waiting for $what" }
    Start-Sleep -Milliseconds 250
  }
}

$failed = $null
try {
  Start-ScheduledTask -TaskName $name
  WaitFor 'the first run' 60 { (Lines $mainLog) -ge 1 }
  # its process gone (its child still running): what the agent's exit leaves
  $first = [int]((Get-Content -LiteralPath $mainLog | Select-Object -First 1) -replace '^main ', '')
  WaitFor 'the first run''s process to exit' 30 { -not (Get-Process -Id $first -ErrorAction SilentlyContinue) }
  Start-Sleep -Seconds 1
  $state = (Get-ScheduledTask -TaskName $name).State
  Write-Host "after its process exited, with its child running, the task is: $state"

  # the installer's --server-start, while the installer still runs
  Start-ScheduledTask -TaskName $name
  try {
    WaitFor 'the second run while the first child runs' 15 { (Lines $mainLog) -ge 2 }
  } catch {
    throw "the task did not run again while the child of its last run lived (state: $state): an installer started by the agent would keep the server from coming back until it ends"
  }
  WaitFor 'both children' 60 { (Lines $childLog) -ge 2 }
  Write-Host 'both children outlived the task process that started them, and the task ran again meanwhile'
} catch {
  $failed = $_
} finally {
  Unregister-ScheduledTask -TaskName $name -Confirm:$false -ErrorAction SilentlyContinue
  Write-Host "main.log:"; if (Test-Path $mainLog) { Get-Content -LiteralPath $mainLog }
  Write-Host "child.log:"; if (Test-Path $childLog) { Get-Content -LiteralPath $childLog }
  Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
}
if ($failed) { throw $failed }
