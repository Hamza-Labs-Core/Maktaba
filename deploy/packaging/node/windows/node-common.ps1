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
$script:NodeCli = Join-Path $script:NodeVenv 'Scripts\maktaba-pipeline.exe'
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

# The pipeline command, as @{ File; Prefix }: the venv's maktaba-pipeline.exe, or, for a
# pipeline without that entry point, its Python with -m maktaba_pipeline.
function Get-NodeCommand {
    if (Test-Path -LiteralPath $script:NodeCli -PathType Leaf) {
        return @{ File = $script:NodeCli; Prefix = @() }
    }
    return @{ File = $script:NodePython; Prefix = @('-m', 'maktaba_pipeline') }
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
# a worker's command line names this node's venv and `run --node`.
function Stop-NodeWorkerProcesses {
    $me = $env:USERNAME
    $filter = "Name = 'python.exe' OR Name = 'maktaba-pipeline.exe' OR Name = 'cmd.exe' OR Name = 'powershell.exe'"
    $processes = @(Get-CimInstance -ClassName Win32_Process -Filter $filter -ErrorAction SilentlyContinue |
        Where-Object {
            $_.CommandLine -and (
                ($_.CommandLine -like '*\Maktaba\node\venv\*' -and $_.CommandLine -like '* run --node*') -or
                $_.CommandLine -like '*\Maktaba\node\run-node.ps1*')
        })
    foreach ($p in $processes) {
        if ($p.ProcessId -eq $PID) { continue }
        $owner = Invoke-CimMethod -InputObject $p -MethodName GetOwner -ErrorAction SilentlyContinue
        if ($null -ne $owner -and $owner.User -ne $me) { continue }
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    }
}

# --- where the pipeline comes from ----------------------------------------------------------
function Test-NodeUrl {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Spec)
    return ($Spec -match '^https?://')
}

# The spec's file name without a URL's query or fragment.
function Get-NodeSpecLeaf {
    param([Parameter(Mandatory = $true)][string]$Spec)
    $bare = ($Spec -split '[?#]', 2)[0]
    return ($bare -split '[\\/]')[-1]
}

# Where the worker's pipeline comes from, decided as install-node.sh decides it (first
# match wins): -FromWheel / -Source as given; the last install's choice (install.conf);
# release.conf in BundleDir (the installer came from a maktaba-node-<version> archive:
# that release's wheel); the checkout's pipeline\. Returns a hashtable: From (release,
# wheel or source), Wheel, WheelSha256, Constraints, ConstraintsSha256, Python, Source,
# Tag.
function Resolve-NodePipelineOrigin {
    param(
        [string]$FromWheel = '',
        [string]$Constraints = '',
        [string]$Source = '',
        [Parameter(Mandatory = $true)][string]$BundleDir,
        [Parameter(Mandatory = $true)][string]$CheckoutPipeline,
        [Parameter(Mandatory = $true)][string]$InstallConf
    )
    if ($FromWheel -and $Source) { throw '-FromWheel and -Source: give one (a wheel, or a source directory to build)' }
    if ($Constraints -and $Source) { throw '-Constraints goes with -FromWheel (a source build uses its uv.lock)' }
    $o = @{ From = ''; Wheel = $FromWheel; WheelSha256 = ''; Constraints = $Constraints; ConstraintsSha256 = '';
        Python = ''; Source = $Source; Tag = '' }
    $releaseConf = Join-Path $BundleDir 'release.conf'

    if ($FromWheel) {
        $o.From = 'wheel'
    } elseif ($Source) {
        $o.From = 'source'
    } else {
        $remembered = Read-NodeKeyValue $InstallConf 'FROM'
        if ($remembered -eq 'wheel') {
            $o.From = 'wheel'
            $o.Wheel = Read-NodeKeyValue $InstallConf 'WHEEL'
            if (-not $o.Constraints) { $o.Constraints = Read-NodeKeyValue $InstallConf 'CONSTRAINTS' }
            if (-not $o.Wheel) { throw "$InstallConf says FROM=wheel but names no WHEEL: pass -FromWheel" }
        } elseif ($remembered -eq 'source') {
            $o.From = 'source'
            $o.Source = Read-NodeKeyValue $InstallConf 'SOURCE'
        } elseif ($remembered -eq 'release') {
            $o.From = 'release'
        } elseif ($remembered) {
            throw "${InstallConf}: unknown FROM=$remembered"
        } else {
            # an install from before -FromWheel remembered only its source
            $o.Source = Read-NodeKeyValue $InstallConf 'SOURCE'
            if ($o.Source) { $o.From = 'source' }
        }
        if (-not $o.From) {
            if (Test-Path -LiteralPath $releaseConf -PathType Leaf) { $o.From = 'release' } else { $o.From = 'source' }
        }
    }

    if ($o.From -eq 'release') {
        if (-not (Test-Path -LiteralPath $releaseConf -PathType Leaf)) {
            throw "this machine's pipeline came from a release archive, and this script is not from one: run that release's install-node.ps1, or pass -FromWheel or -Source"
        }
        $o.Tag = Read-NodeKeyValue $releaseConf 'TAG'
        $repo = Read-NodeKeyValue $releaseConf 'REPO'
        $wheel = Read-NodeKeyValue $releaseConf 'WHEEL'
        if (-not ($o.Tag -and $repo -and $wheel)) { throw "$releaseConf is incomplete (TAG, REPO, WHEEL)" }
        $base = "https://github.com/$repo/releases/download/$($o.Tag)"
        # the archive carries the wheel; the release has it too, for an archive without it
        $local = Join-Path $BundleDir $wheel
        if (Test-Path -LiteralPath $local -PathType Leaf) { $o.Wheel = $local } else { $o.Wheel = "$base/$wheel" }
        $o.WheelSha256 = Read-NodeKeyValue $releaseConf 'WHEEL_SHA256'
        if (-not $o.Constraints) {
            $cons = Read-NodeKeyValue $releaseConf 'CONSTRAINTS'
            if ($cons) {
                $local = Join-Path $BundleDir $cons
                if (Test-Path -LiteralPath $local -PathType Leaf) { $o.Constraints = $local } else { $o.Constraints = "$base/$cons" }
                $o.ConstraintsSha256 = Read-NodeKeyValue $releaseConf 'CONSTRAINTS_SHA256'
            }
        }
        $o.Python = Read-NodeKeyValue $releaseConf 'PYTHON'
    }
    if ($o.From -eq 'source') {
        if (-not $o.Source) { $o.Source = $CheckoutPipeline }
    } else {
        $leaf = Get-NodeSpecLeaf $o.Wheel
        if ($leaf -notlike '*.whl') {
            throw "-FromWheel takes a wheel (maktaba_pipeline-<version>-py3-none-any.whl): $($o.Wheel)"
        }
        if ($leaf -notlike 'maktaba_pipeline-*') {
            throw "$($o.Wheel) is not a maktaba-pipeline wheel (maktaba_pipeline-<version>-...whl)"
        }
        if (-not $o.Python) { $o.Python = '3.12' }
    }
    return $o
}

# A local file for Spec, a path or an http(s) URL (downloaded into DownloadDir). Sha256,
# when given, must match.
function Get-NodePipelineFile {
    param(
        [Parameter(Mandatory = $true)][string]$Spec,
        [string]$Sha256 = '',
        [Parameter(Mandatory = $true)][string]$DownloadDir
    )
    if (Test-NodeUrl $Spec) {
        New-Item -ItemType Directory -Force -Path $DownloadDir | Out-Null
        $dest = Join-Path $DownloadDir (Get-NodeSpecLeaf $Spec)
        Write-NodeStep "Downloading $Spec"
        # Windows PowerShell 5.1 does not offer TLS 1.2 by default; GitHub needs it.
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        $part = "$dest.part"
        $saved = $ProgressPreference
        $ProgressPreference = 'SilentlyContinue'   # the progress bar slows 5.1's download a lot
        try {
            Invoke-WebRequest -Uri $Spec -OutFile $part -UseBasicParsing
        } catch {
            throw "could not download ${Spec}: $($_.Exception.Message)"
        } finally {
            $ProgressPreference = $saved
        }
        Move-Item -LiteralPath $part -Destination $dest -Force
    } else {
        if (-not (Test-Path -LiteralPath $Spec -PathType Leaf)) { throw "no such file: $Spec" }
        $dest = (Resolve-Path -LiteralPath $Spec).Path
    }
    if ($Sha256) {
        $got = (Get-FileHash -LiteralPath $dest -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($got -ne $Sha256.ToLowerInvariant()) { throw "${dest}: SHA-256 $got, the release says $Sha256" }
    }
    return $dest
}
