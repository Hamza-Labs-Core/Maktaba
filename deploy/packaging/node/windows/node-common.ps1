# Shared by install-node.ps1, uninstall-node.ps1 and run-node.ps1 (dot-sourced).
# Maktaba node worker on Windows (Epic 31). Windows PowerShell 5.1 compatible.

Set-StrictMode -Version 2.0

# Where things live (per user: mapped drives, saved share credentials and the node
# config are all the user's).
$script:NodeConfDir = Join-Path $env:APPDATA 'Maktaba'                 # node.json, node.env
$script:NodeDir = Join-Path $env:LOCALAPPDATA 'Maktaba\node'           # venv + these scripts
$script:NodeLogDir = Join-Path $env:LOCALAPPDATA 'Maktaba\Logs'
$script:NodeVenv = Join-Path $script:NodeDir 'venv'
$script:NodePython = Join-Path $script:NodeVenv 'Scripts\python.exe'
$script:NodeInstallConf = Join-Path $script:NodeDir 'install.conf'
$script:WindowsPowerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'

function Write-NodeStep {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Host "==> $Message" -ForegroundColor Cyan
}

# maktaba-node for the main worker, maktaba-node@<N> for extra worker N (task and
# service names, as the systemd units on Linux).
function Get-NodeUnitName {
    param([Parameter(Mandatory = $true)][string]$Instance)
    if ($Instance -eq 'main') { return 'maktaba-node' }
    return "maktaba-node@$Instance"
}

function Get-NodeLogBase {
    param([Parameter(Mandatory = $true)][string]$Instance)
    if ($Instance -eq 'main') { return 'node' }
    return "node@$Instance"
}

function Test-NodeInstanceName {
    param([Parameter(Mandatory = $true)][string]$Instance)
    return ($Instance -match '^[A-Za-z0-9_-]+$')
}

function Test-IsAdministrator {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# KEY=VALUE lines into this process's environment: the format of node.env, the same the
# macOS runner and systemd's EnvironmentFile= read (no expansion; # comments; optional
# "export "; one pair of surrounding quotes removed).
function Import-NodeEnvFile {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return }
    foreach ($raw in [System.IO.File]::ReadAllLines($Path)) {
        $line = $raw.Trim()
        if ($line.Length -eq 0 -or $line.StartsWith('#')) { continue }
        if ($line.StartsWith('export ')) { $line = $line.Substring(7).TrimStart() }
        $eq = $line.IndexOf('=')
        if ($eq -lt 1) { continue }
        $key = $line.Substring(0, $eq).Trim()
        $value = $line.Substring($eq + 1).Trim()
        if ($key -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') { continue }
        if ($value.Length -ge 2) {
            $first = $value[0]
            $last = $value[$value.Length - 1]
            if (($first -eq [char]'"' -and $last -eq [char]'"') -or ($first -eq [char]"'" -and $last -eq [char]"'")) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }
        [System.Environment]::SetEnvironmentVariable($key, $value, 'Process')
    }
}

# One KEY=VALUE value from a file, without touching the environment.
function Read-NodeKeyValue {
    param([Parameter(Mandatory = $true)][string]$Path, [Parameter(Mandatory = $true)][string]$Key)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return '' }
    $value = ''
    foreach ($line in [System.IO.File]::ReadAllLines($Path)) {
        if ($line.StartsWith("$Key=")) { $value = $line.Substring($Key.Length + 1).Trim() }
    }
    return $value
}

# Quote one argument for a Windows command line (the rules CommandLineToArgvW and the
# C runtime use), so paths with spaces and values with quotes arrive intact whatever
# PowerShell version runs this.
function ConvertTo-CommandLineArgument {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') { return $Value }
    $sb = New-Object System.Text.StringBuilder
    [void]$sb.Append([char]'"')
    $backslashes = 0
    foreach ($ch in $Value.ToCharArray()) {
        if ($ch -eq [char]'\') {
            $backslashes++
            continue
        }
        if ($ch -eq [char]'"') {
            [void]$sb.Append([char]'\', (2 * $backslashes) + 1)
        } elseif ($backslashes -gt 0) {
            [void]$sb.Append([char]'\', $backslashes)
        }
        [void]$sb.Append($ch)
        $backslashes = 0
    }
    if ($backslashes -gt 0) { [void]$sb.Append([char]'\', 2 * $backslashes) }
    [void]$sb.Append([char]'"')
    return $sb.ToString()
}

# Run a program in this console (its output shows; stdin stays the user's) and return
# its exit code. Avoids PowerShell's own native-argument quoting, which differs
# between versions and breaks on embedded quotes.
function Invoke-NativeCommand {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$ArgumentList = @()
    )
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    $psi.Arguments = (@($ArgumentList | ForEach-Object { ConvertTo-CommandLineArgument $_ }) -join ' ')
    $psi.UseShellExecute = $false
    $process = [System.Diagnostics.Process]::Start($psi)
    $process.WaitForExit()
    return $process.ExitCode
}

# Run a program quietly and return @{ ExitCode; StdOut; StdErr } (short outputs only).
function Invoke-NativeCapture {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$ArgumentList = @()
    )
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    $psi.Arguments = (@($ArgumentList | ForEach-Object { ConvertTo-CommandLineArgument $_ }) -join ' ')
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    try {
        $process = [System.Diagnostics.Process]::Start($psi)
    } catch {
        return @{ ExitCode = -1; StdOut = ''; StdErr = $_.Exception.Message }
    }
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $stdout = $process.StandardOutput.ReadToEnd()
    $process.WaitForExit()
    return @{ ExitCode = $process.ExitCode; StdOut = $stdout; StdErr = $stderrTask.Result }
}

# Has this machine joined a home? node_check.py reads the node config the way the worker
# does (MAKTABA_NODE_CONFIG included, so import node.env first).
function Test-NodeJoined {
    if (-not (Test-Path -LiteralPath $script:NodePython -PathType Leaf)) { return $false }
    $check = Join-Path $script:NodeDir 'node_check.py'
    $result = Invoke-NativeCapture -FilePath $script:NodePython -ArgumentList @($check)
    return ($result.ExitCode -eq 0)
}

# Stop this user's node workers that a stopped task left behind, and any supervisor:
# workers carry "-X maktaba_node=<instance>" on their command line for this.
function Stop-NodeWorkerProcesses {
    $me = $env:USERNAME
    $filter = "Name = 'python.exe' OR Name = 'cmd.exe' OR Name = 'powershell.exe'"
    $processes = @(Get-CimInstance -ClassName Win32_Process -Filter $filter -ErrorAction SilentlyContinue |
        Where-Object {
            $_.CommandLine -and (
                $_.CommandLine -like '*-X maktaba_node=*' -or
                $_.CommandLine -like '*\Maktaba\node\run-node.ps1*')
        })
    foreach ($p in $processes) {
        if ($p.ProcessId -eq $PID) { continue }
        $owner = Invoke-CimMethod -InputObject $p -MethodName GetOwner -ErrorAction SilentlyContinue
        if ($null -ne $owner -and $owner.User -ne $me) { continue }
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    }
}
