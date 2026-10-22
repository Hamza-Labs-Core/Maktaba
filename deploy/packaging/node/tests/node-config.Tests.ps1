# Tests for where the node installer's scripts find this machine's node config
# (Get-NodeConfigPath, Get-NodeConfigFiles in node-common.ps1): the roaming profile's
# node.json of a machine that joined before Epic 38 is used while it is this PC's (its
# machine_id) or no PC's yet; another PC's is skipped. Plain asserts, no Pester: runs
# under Windows PowerShell 5.1 and PowerShell 7, on Windows or elsewhere.
#
#   pwsh -NoProfile -File deploy/packaging/node/tests/node-config.Tests.ps1
$ErrorActionPreference = 'Stop'

# a profile of its own and a fake id for this PC: never the real ones
$T = Join-Path ([System.IO.Path]::GetTempPath()) ('node-config-test-' + [guid]::NewGuid().ToString('N'))
$roaming = Join-Path $T 'Roaming'
$local = Join-Path $T 'Local'
New-Item -ItemType Directory -Path (Join-Path $roaming 'Maktaba'), (Join-Path $local 'Maktaba') | Out-Null
[System.Environment]::SetEnvironmentVariable('APPDATA', $roaming, 'Process')
[System.Environment]::SetEnvironmentVariable('LOCALAPPDATA', $local, 'Process')
[System.Environment]::SetEnvironmentVariable('MAKTABA_NODE_CONFIG', $null, 'Process')
[System.Environment]::SetEnvironmentVariable('MAKTABA_MACHINE_ID', 'pc-a', 'Process')
if (-not $env:SystemRoot) { [System.Environment]::SetEnvironmentVariable('SystemRoot', (Join-Path $T 'SystemRoot'), 'Process') }
. (Join-Path (Split-Path -Parent $PSScriptRoot) 'windows/node-common.ps1')

$script:Pass = 0
$script:Fail = 0
function Ok([string]$What) { $script:Pass++; Write-Host "ok    $What" }
function Bad([string]$What) { $script:Fail++; Write-Host "FAIL  $What" -ForegroundColor Red }
function Assert-Equal($Expected, $Actual, [string]$What) {
    if ($Expected -eq $Actual) { Ok $What } else { Bad "${What}: expected '$Expected', got '$Actual'" }
}

$old = Join-Path $roaming 'Maktaba\node.json'
$new = Join-Path $local 'Maktaba\node.json'
try {
    Assert-Equal $new (Get-NodeConfigPath) 'not joined: the new place, where a join writes'
    Assert-Equal 0 @(Get-NodeConfigFiles).Count 'not joined: no node config to forget'

    Set-Content -LiteralPath $old -Value '{"node_id":"n1","database_url":"postgres://h"}'
    Assert-Equal $old (Get-NodeConfigPath) 'an unclaimed roaming node config is read (the pipeline claims it)'

    Set-Content -LiteralPath $old -Value '{"node_id":"n1","database_url":"postgres://h","machine_id":"PC-A"}'
    Assert-Equal $old (Get-NodeConfigPath) 'this PC''s roaming node config is read'
    Assert-Equal $old ((@(Get-NodeConfigFiles)) -join ';') 'this PC''s is the one to forget'

    $theirs = '{"node_id":"n1","database_url":"postgres://h","machine_id":"pc-b"}'
    Set-Content -LiteralPath $old -Value $theirs
    Assert-Equal $new (Get-NodeConfigPath) 'another PC''s roaming node config is skipped: this PC''s own place'
    Assert-Equal 0 @(Get-NodeConfigFiles).Count 'another PC''s node config is never one to forget here'
    Set-Content -LiteralPath $new -Value '{"node_id":"n2","machine_id":"pc-a"}'   # this PC's join
    Assert-Equal $new (Get-NodeConfigPath) 'after this PC''s join: its own file'
    Assert-Equal $theirs (Get-Content -LiteralPath $old -Raw).Trim() 'the other PC''s node config stays as it was'
    Assert-Equal $new ((@(Get-NodeConfigFiles)) -join ';') 'only this PC''s own file is one to forget'

    Set-Content -LiteralPath $old -Value 'not json'
    Assert-Equal $old (Get-NodeConfigPath) 'a broken roaming node config is never skipped (its reader says what is wrong)'
} finally {
    Remove-Item -LiteralPath $T -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "$($script:Pass) passed, $($script:Fail) failed"
if ($script:Fail -ne 0) { exit 1 }
