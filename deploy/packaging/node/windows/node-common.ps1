# Shared by install-node.ps1, uninstall-node.ps1 and run-node.ps1 (dot-sourced).
# Maktaba node worker on Windows (Epic 31). Windows PowerShell 5.1 compatible.

Set-StrictMode -Version 2.0

# Where things live (per user: mapped drives, saved share credentials and the node
# config are all the user's).
$script:NodeConfDir = Join-Path $env:APPDATA 'Maktaba'                 # node.env (node.json before Epic 38)
$script:NodeDir = Join-Path $env:LOCALAPPDATA 'Maktaba\node'           # venv + these scripts
$script:NodeLogDir = Join-Path $env:LOCALAPPDATA 'Maktaba\Logs'
$script:NodeVenv = Join-Path $script:NodeDir 'venv'
$script:NodePython = Join-Path $script:NodeVenv 'Scripts\python.exe'
$script:NodeCli = Join-Path $script:NodeVenv 'Scripts\maktaba-pipeline.exe'
$script:NodeInstallConf = Join-Path $script:NodeDir 'install.conf'
$script:WindowsPowerShell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'

# Get-MachineId: this PC's id, which a node config records as machine_id
# (cluster/config.py machine_id): MAKTABA_MACHINE_ID, else Windows' MachineGuid (the
# 64-bit view); '' when it cannot be read.
function Get-MachineId {
    if ($env:MAKTABA_MACHINE_ID) { return $env:MAKTABA_MACHINE_ID.Trim() }
    try {
        $hklm = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::LocalMachine,
            [Microsoft.Win32.RegistryView]::Registry64)
        $key = $hklm.OpenSubKey('SOFTWARE\Microsoft\Cryptography')
        if ($key) { return ([string]$key.GetValue('MachineGuid')).Trim() }
    } catch { }
    return ''
}

# Test-NodeConfigOfAnotherPC: the node config at Path records another PC (its
# machine_id) than this one. One no PC has claimed, one that cannot be read, or a PC
# whose id cannot be known is never another PC's.
function Test-NodeConfigOfAnotherPC {
    param([Parameter(Mandatory = $true)][string]$Path)
    try {
        $config = Get-Content -LiteralPath $Path -Raw -ErrorAction Stop | ConvertFrom-Json -ErrorAction Stop
    } catch { return $false }
    $field = $config.PSObject.Properties['machine_id']
    $owner = if ($field) { ([string]$field.Value).Trim() } else { '' }
    if (-not $owner) { return $false }
    $this = Get-MachineId
    return [bool]$this -and ($owner -ine $this)
}

# Test-NodeConfigWhole: the file at Path holds a node config with its identity (a
# node_id and a private_key), not a cut one.
function Test-NodeConfigWhole {
    param([Parameter(Mandatory = $true)][string]$Path)
    try {
        $config = Get-Content -LiteralPath $Path -Raw -ErrorAction Stop | ConvertFrom-Json -ErrorAction Stop
    } catch { return $false }
    if ($config -isnot [System.Management.Automation.PSCustomObject]) { return $false }
    foreach ($key in @('node_id', 'private_key')) {
        $field = $config.PSObject.Properties[$key]
        if (-not $field -or -not ([string]$field.Value).Trim()) { return $false }
    }
    return $true
}

# Get-NodeConfigLeftovers: what a write of the node config at Path that could not finish
# left beside it (cluster/config.py _leftovers): the old file under a backup name and the
# new one as a temp file (.node.json-<time>-<pid>-<hex>.bak and .tmp; .node-* for
# node.json before they carried the file's name).
function Get-NodeConfigLeftovers {
    param([Parameter(Mandatory = $true)][string]$Path)
    $dir = Split-Path -Parent $Path
    $name = Split-Path -Leaf $Path
    if (-not (Test-Path -LiteralPath $dir -PathType Container)) { return }
    Get-ChildItem -LiteralPath $dir -Force -File -ErrorAction SilentlyContinue | Where-Object {
        ($_.Name.StartsWith(".$name-", [System.StringComparison]::OrdinalIgnoreCase) -and
            ($_.Extension -eq '.bak' -or $_.Extension -eq '.tmp')) -or
        ($name -eq 'node.json' -and $_.Name -cmatch '^\.node-(\d+-[0-9a-f]{8}\.bak|[a-z0-9_]{8}\.tmp)$')
    } | Sort-Object -Property Name
}

# Get-NodeConfigSource: the file that holds the node config at Path, as the pipeline
# reads it (cluster/config.py _source): Path, unless it is missing or holds no whole node
# config (one that cannot be read is Path: its reader says why); then what the
# pipeline's next load puts in its place, the newest whole backup a write left, else its
# newest whole temp file; else Path.
function Get-NodeConfigSource {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (Test-Path -LiteralPath $Path) {
        try { $null = [System.IO.File]::ReadAllBytes($Path) } catch { return $Path }
        if (Test-NodeConfigWhole -Path $Path) { return $Path }
    }
    $left = @(Get-NodeConfigLeftovers -Path $Path)
    foreach ($ext in @('.bak', '.tmp')) {
        $best = $null
        foreach ($file in $left) {
            if ($file.Extension -ne $ext -or -not (Test-NodeConfigWhole -Path $file.FullName)) { continue }
            if ($null -eq $best -or $file.LastWriteTimeUtc -gt $best.LastWriteTimeUtc) { $best = $file }
        }
        if ($best) { return $best.FullName }
    }
    return $Path
}

# Get-NodeConfigPath: this machine's one node config, where the pipeline reads it
# (cluster/config.py config_path): MAKTABA_NODE_CONFIG, else the
# %APPDATA%\Maktaba\node.json of a machine that joined before Epic 38 (it stays where
# it is) while it is this PC's or no PC's yet, else %LOCALAPPDATA%\Maktaba\node.json,
# where a new join writes. Another PC's roaming config is not this PC's; what a write
# that could not finish left of it counts as it (Get-NodeConfigSource).
function Get-NodeConfigPath {
    if ($env:MAKTABA_NODE_CONFIG) { return $env:MAKTABA_NODE_CONFIG }
    $old = Join-Path $script:NodeConfDir 'node.json'
    $held = Get-NodeConfigSource -Path $old
    if ((Test-Path -LiteralPath $held -PathType Leaf) -and -not (Test-NodeConfigOfAnotherPC -Path $held)) { return $old }
    return (Join-Path $env:LOCALAPPDATA 'Maktaba\node.json')
}

# Get-NodeConfigKeptBroken: the broken node.json a load kept beside Path when a leftover
# took its name (cluster/config.py _broken_name: <name>.broken-<time>-<hex>). It is no
# node config and never read as one; it may still hold part of the credential.
function Get-NodeConfigKeptBroken {
    param([Parameter(Mandatory = $true)][string]$Path)
    $dir = Split-Path -Parent $Path
    if (-not (Test-Path -LiteralPath $dir -PathType Container)) { return }
    Get-ChildItem -LiteralPath $dir -Force -File -Filter ((Split-Path -Leaf $Path) + '.broken-*') -ErrorAction SilentlyContinue |
        Sort-Object -Property Name
}

# Get-NodeConfigLock: the write lock beside the node config at Path (.<name>.lock, which
# the pipeline and maktaba-server hold while they write it; gone when nobody holds it, but
# a process that stopped mid-write leaves it), when it is there.
function Get-NodeConfigLock {
    param([Parameter(Mandatory = $true)][string]$Path)
    $lock = Join-Path (Split-Path -Parent $Path) ('.' + (Split-Path -Leaf $Path) + '.lock')
    if (Test-Path -LiteralPath $lock -PathType Leaf) { $lock }
}

# Get-NodeConfigFiles: every file of a node config that is this PC's (both places on
# Windows, or MAKTABA_NODE_CONFIG; never another PC's roaming one): its node.json, what a
# write that could not finish left beside it, a broken node.json kept there, and its
# write lock. To forget the home, all of them go, or the second would take the first
# one's place (and a leftover would be put back).
function Get-NodeConfigFiles {
    if ($env:MAKTABA_NODE_CONFIG) {
        $env:MAKTABA_NODE_CONFIG
        Get-NodeConfigLeftovers -Path $env:MAKTABA_NODE_CONFIG | ForEach-Object { $_.FullName }
        Get-NodeConfigKeptBroken -Path $env:MAKTABA_NODE_CONFIG | ForEach-Object { $_.FullName }
        Get-NodeConfigLock -Path $env:MAKTABA_NODE_CONFIG
        return
    }
    foreach ($path in @((Join-Path $script:NodeConfDir 'node.json'), (Join-Path $env:LOCALAPPDATA 'Maktaba\node.json'))) {
        $held = Get-NodeConfigSource -Path $path
        if (-not (Test-Path -LiteralPath $held -PathType Leaf) -or (Test-NodeConfigOfAnotherPC -Path $held)) { continue }
        if (Test-Path -LiteralPath $path -PathType Leaf) { $path }
        Get-NodeConfigLeftovers -Path $path | ForEach-Object { $_.FullName }
        Get-NodeConfigKeptBroken -Path $path | ForEach-Object { $_.FullName }
        Get-NodeConfigLock -Path $path
    }
}

function Write-NodeStep {
    param([Parameter(Mandatory = $true)][string]$Message)
    Write-Host "==> $Message" -ForegroundColor Cyan
}

# maktaba-node for the main worker, maktaba-node@<N> for extra worker N (task and
# service names, as the systemd units on Linux).
# Find-MaktabaServer: maktaba-server.exe in ServerDir, else on the PATH ('' when none).
function Find-MaktabaServer {
    param([string]$ServerDir = '')
    if ($ServerDir) {
        $p = Join-Path $ServerDir 'maktaba-server.exe'
        if (Test-Path -LiteralPath $p -PathType Leaf) { return $p }
    }
    $cmd = Get-Command 'maktaba-server.exe' -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    return ''
}

# Get-FullNodeArguments: the `maktaba-server setup --node` arguments of install-node.ps1
# -Full (Epic 35 story 35.8): the release folder, the worker's own Python environment,
# its extra workers ([pipeline] instances) and the Task / Service mode.
function Get-FullNodeArguments {
    param([string]$ServerDir = '', [Parameter(Mandatory = $true)][string]$Python, [int]$Instances = -1,
        [string]$Mode = 'Task')
    $a = @('setup', '--node')
    if ($ServerDir) { $a += @('--from', $ServerDir) }
    $a += @('--python', $Python)
    if ($Instances -ge 0) { $a += @('--instances', "$Instances") }
    $a += @('--mode', $Mode.ToLowerInvariant())
    return ,$a
}

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
        [string[]]$ArgumentList = @(),
        [string]$WorkingDirectory = ''
    )
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    $psi.Arguments = (@($ArgumentList | ForEach-Object { ConvertTo-CommandLineArgument $_ }) -join ' ')
    $psi.UseShellExecute = $false
    if ($WorkingDirectory) { $psi.WorkingDirectory = $WorkingDirectory }
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

# True for an http(s) URL with user info (user:password@host) or a query string (a token,
# a signed URL's signature): such a URL is neither logged nor remembered in install.conf.
function Test-NodeUrlHasCredential {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Spec)
    if (-not (Test-NodeUrl $Spec)) { return $false }
    $authority = (($Spec -replace '^https?://', '') -split '[/?#]', 2)[0]
    return ($authority.Contains('@') -or $Spec.Contains('?'))
}

# Spec for a message: a URL's user info and query string replaced by ***.
function Format-NodeSpecForLog {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Spec)
    if (-not (Test-NodeUrl $Spec)) { return $Spec }
    $m = [regex]::Match($Spec, '^(https?://)([^/?#]*)([^?#]*)([?#].*)?$')
    $authority = $m.Groups[2].Value
    if ($authority.Contains('@')) { $authority = '***@' + $authority.Substring($authority.LastIndexOf('@') + 1) }
    $rest = $m.Groups[3].Value
    if ($m.Groups[4].Success -and $m.Groups[4].Value) { $rest += '?***' }
    return $m.Groups[1].Value + $authority + $rest
}

# A file:// URL for a local path (spaces and the like escaped).
function ConvertTo-NodeFileUrl {
    param([Parameter(Mandatory = $true)][string]$Path)
    return ([System.Uri]::new($Path)).AbsoluteUri
}

# The spec's file name without a URL's query or fragment.
function Get-NodeSpecLeaf {
    param([Parameter(Mandatory = $true)][string]$Spec)
    $bare = ($Spec -split '[?#]', 2)[0]
    return ($bare -split '[\\/]')[-1]
}

# A SHA-256 given on the command line, lower-cased; throws unless it is 64 hex digits.
function ConvertTo-NodeSha256 {
    param([Parameter(Mandatory = $true)][string]$Name, [Parameter(Mandatory = $true)][string]$Value)
    $v = $Value.ToLowerInvariant()
    if ($v -notmatch '^[0-9a-f]{64}$') { throw "$Name takes a SHA-256 in hex (64 characters): $Value" }
    return $v
}

# Where the worker's pipeline comes from, decided as install-node.sh decides it (first
# match wins): -FromWheel / -Source as given; the last install's choice (install.conf);
# release.conf in BundleDir (the installer came from a maktaba-node-<version> archive:
# that release's wheel); the checkout's pipeline\. Returns a hashtable: From (release,
# wheel or source), Wheel, WheelSha256, Constraints, ConstraintsSha256, Python, Source,
# Tag. The digests are the release's, the given ones (-WheelSha256, -ConstraintsSha256),
# or the ones the last install pinned; empty when not known yet.
# --- torch's build (story 38.9) -----------------------------------------------------------
# A release ships two pin sets: the default one (torch's CPU build on Windows, from PyPI,
# 124 MB) and the cuda one (its CUDA build, from PyTorch's index, 2.0 GB), which only the
# second listen needs, and only an NVIDIA GPU that runs it: as the pipeline checks it
# (stt/cohere_cuda.py: MIN_GPU_GB on Windows, MIN_COMPUTE, MIN_DRIVER; a test keeps these
# equal), asked of nvidia-smi. Something nvidia-smi does not report is not held against it.
$script:NodeSecondListenMinMiB = 9728       # 9.5 GiB
$script:NodeSecondListenMinCompute = '7.5'  # Turing
$script:NodeSecondListenMinDriver = 580

# One nvidia-smi line ("NVIDIA GeForce RTX 3080 Ti, 12288, 8.6, 591.44", or an older
# driver's "name, MiB, driver") as @{ Name; MiB; Compute; Driver } (Compute and Driver $null
# when it does not say); $null when it names no GPU.
function ConvertFrom-NodeNvidiaSmi {
    param([string]$Line)
    $f = @($Line.Split(',') | ForEach-Object { $_.Trim() })
    if ($f.Count -lt 2) { return $null }
    $mib = 0
    if (-not [int]::TryParse($f[1], [ref]$mib) -or $mib -le 0) { return $null }
    $gpu = @{ Name = $f[0]; MiB = $mib; Compute = $null; Driver = $null }
    if ($f.Count -ge 3 -and $f[$f.Count - 1] -and $f[$f.Count - 1] -ne '[N/A]') { $gpu.Driver = $f[$f.Count - 1] }
    if ($f.Count -ge 4 -and $f[2] -match '^\d+\.\d+$') { $gpu.Compute = $f[2] }
    return $gpu
}

# The first NVIDIA GPU (CUDA device 0), or $null without one or its driver.
function Get-NodeNvidiaGpu {
    $smi = Get-Command nvidia-smi.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    $exe = $null
    if ($smi) { $exe = $smi.Path }
    if (-not $exe -and $env:SystemRoot) {
        $p = Join-Path $env:SystemRoot 'System32\nvidia-smi.exe'
        if (Test-Path -LiteralPath $p -PathType Leaf) { $exe = $p }
    }
    if (-not $exe) { return $null }
    foreach ($query in @('name,memory.total,compute_cap,driver_version', 'name,memory.total,driver_version')) {
        $r = Invoke-NativeCapture -FilePath $exe -ArgumentList @("--query-gpu=$query", '--format=csv,noheader,nounits')
        if ($r.ExitCode -eq 0) {
            $line = @($r.StdOut -split "`r?`n" | Where-Object { $_.Trim() }) | Select-Object -First 1
            if ($line) { return (ConvertFrom-NodeNvidiaSmi $line) }
            return $null
        }
    }
    return $null
}

# Why this GPU (Get-NodeNvidiaGpu) does not run the second listen, or '' when it does.
function Get-NodeSecondListenGap {
    param($Gpu)
    if (-not $Gpu) { return 'no NVIDIA GPU' }
    if ($Gpu.MiB -lt $script:NodeSecondListenMinMiB) {
        return ('{0} has {1:N1} GiB, under {2:N1}' -f $Gpu.Name, ($Gpu.MiB / 1024), ($script:NodeSecondListenMinMiB / 1024))
    }
    if ($Gpu.Compute -and ([version]$Gpu.Compute -lt [version]$script:NodeSecondListenMinCompute)) {
        return "$($Gpu.Name) is compute capability $($Gpu.Compute), under $($script:NodeSecondListenMinCompute)"
    }
    if ($Gpu.Driver) {
        $major = 0
        if ([int]::TryParse(($Gpu.Driver.Split('.')[0]), [ref]$major) -and $major -lt $script:NodeSecondListenMinDriver) {
            return "the NVIDIA driver $($Gpu.Driver) is older than R$($script:NodeSecondListenMinDriver)"
        }
    }
    return ''
}

function Resolve-NodePipelineOrigin {
    param(
        [string]$FromWheel = '',
        [string]$Constraints = '',
        [string]$WheelSha256 = '',
        [string]$ConstraintsSha256 = '',
        [string]$Source = '',
        [Parameter(Mandatory = $true)][string]$BundleDir,
        [Parameter(Mandatory = $true)][string]$CheckoutPipeline,
        [Parameter(Mandatory = $true)][string]$InstallConf,
        # the release's cuda pin set (torch's CUDA build) instead of its default one
        [switch]$CudaTorch
    )
    if ($FromWheel -and $Source) { throw '-FromWheel and -Source: give one (a wheel, or a source directory to build)' }
    if ($Constraints -and $Source) { throw '-Constraints goes with -FromWheel (a source build uses its uv.lock)' }
    if ($WheelSha256) {
        if (-not $FromWheel) { throw "-Sha256 goes with -FromWheel (a release's wheel has its digest in release.conf)" }
        $WheelSha256 = ConvertTo-NodeSha256 '-Sha256' $WheelSha256
    }
    if ($ConstraintsSha256) {
        if (-not $Constraints) { throw '-ConstraintsSha256 goes with -Constraints' }
        $ConstraintsSha256 = ConvertTo-NodeSha256 '-ConstraintsSha256' $ConstraintsSha256
    }
    $o = @{ From = ''; Wheel = $FromWheel; WheelSha256 = $WheelSha256; Constraints = $Constraints;
        ConstraintsSha256 = $ConstraintsSha256; Python = ''; Source = $Source; Tag = '' }
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
            $o.WheelSha256 = Read-NodeKeyValue $InstallConf 'WHEEL_SHA256'
            if (-not $o.Constraints) {
                $o.Constraints = Read-NodeKeyValue $InstallConf 'CONSTRAINTS'
                $o.ConstraintsSha256 = Read-NodeKeyValue $InstallConf 'CONSTRAINTS_SHA256'
            }
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
            $key = 'CONSTRAINTS'
            # a release before story 38.9 has no cuda set: its one set
            if ($CudaTorch -and (Read-NodeKeyValue $releaseConf 'CONSTRAINTS_CUDA')) { $key = 'CONSTRAINTS_CUDA' }
            $cons = Read-NodeKeyValue $releaseConf $key
            if ($cons) {
                $local = Join-Path $BundleDir $cons
                if (Test-Path -LiteralPath $local -PathType Leaf) { $o.Constraints = $local } else { $o.Constraints = "$base/$cons" }
                $o.ConstraintsSha256 = Read-NodeKeyValue $releaseConf ($key + '_SHA256')
            }
        }
        $o.Python = Read-NodeKeyValue $releaseConf 'PYTHON'
    }
    if ($o.From -eq 'source') {
        if (-not $o.Source) { $o.Source = $CheckoutPipeline }
    } else {
        $leaf = Get-NodeSpecLeaf $o.Wheel
        if ($leaf -notlike '*.whl') {
            throw "-FromWheel takes a wheel (maktaba_pipeline-<version>-py3-none-any.whl): $(Format-NodeSpecForLog $o.Wheel)"
        }
        if ($leaf -notlike 'maktaba_pipeline-*') {
            throw "$(Format-NodeSpecForLog $o.Wheel) is not a maktaba-pipeline wheel (maktaba_pipeline-<version>-...whl)"
        }
        if (-not $o.Python) { $o.Python = '3.12' }
        # A URL is printed while it downloads and kept in install.conf for updates: one that
        # carries a credential would leave it in logs and on disk.
        foreach ($u in @($o.Wheel, $o.Constraints)) {
            if ($u -and (Test-NodeUrlHasCredential $u)) {
                throw "$(Format-NodeSpecForLog $u) carries a credential (user:password@ or a ?query such as a token), which would be logged and remembered in install.conf: use a plain URL pinned with -Sha256 / -ConstraintsSha256, or download the file and pass its path"
            }
        }
    }
    return $o
}

# A local file for Spec, a path or an http(s) URL (downloaded into DownloadDir), as
# @{ Path; Sha256 }. Sha256, when given, must match (a download that does not is deleted);
# a download is always hashed, so the next update can pin it. Get-FileHash is part of
# every supported PowerShell (4.0+), so a digest is never skipped.
function Get-NodePipelineFile {
    param(
        [Parameter(Mandatory = $true)][string]$Spec,
        [string]$Sha256 = '',
        [Parameter(Mandatory = $true)][string]$DownloadDir
    )
    $isUrl = Test-NodeUrl $Spec
    if ($isUrl) {
        New-Item -ItemType Directory -Force -Path $DownloadDir | Out-Null
        $dest = Join-Path $DownloadDir (Get-NodeSpecLeaf $Spec)
        Write-NodeStep "Downloading $(Format-NodeSpecForLog $Spec)"
        # Windows PowerShell 5.1 does not offer TLS 1.2 by default; GitHub needs it.
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        $part = "$dest.part"
        $saved = $ProgressPreference
        $ProgressPreference = 'SilentlyContinue'   # the progress bar slows 5.1's download a lot
        try {
            Invoke-WebRequest -Uri $Spec -OutFile $part -UseBasicParsing
        } catch {
            throw "could not download $(Format-NodeSpecForLog $Spec): $($_.Exception.Message)"
        } finally {
            $ProgressPreference = $saved
        }
        Move-Item -LiteralPath $part -Destination $dest -Force
    } else {
        if (-not (Test-Path -LiteralPath $Spec -PathType Leaf)) { throw "no such file: $Spec" }
        $dest = (Resolve-Path -LiteralPath $Spec).Path
    }
    $got = ''
    if ($Sha256 -or $isUrl) {
        $got = (Get-FileHash -LiteralPath $dest -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($Sha256 -and $got -ne $Sha256.ToLowerInvariant()) {
            if ($isUrl) { Remove-Item -LiteralPath $dest -Force -ErrorAction SilentlyContinue }
            throw "${dest}: SHA-256 $got, expected $Sha256 (the release's, -Sha256's, or the one pinned at the first install; pass -FromWheel again to take a new file)"
        }
    }
    return @{ Path = $dest; Sha256 = $got }
}

# Hash-checking mode (uv/pip --require-hashes): the one-line requirements file that names
# the wheel by its file URL and SHA-256 (with its extras), written next to the constraints
# file, whose --hash lines check every dependency. Throws when the constraints file has no
# hashes. Returns the requirements file's path.
function New-NodeHashedRequirements {
    param(
        [Parameter(Mandatory = $true)][string]$WheelFile,
        [string]$WheelSha256 = '',
        [string[]]$Extras = @(),
        [Parameter(Mandatory = $true)][string]$ConstraintsFile
    )
    if (-not (Select-String -LiteralPath $ConstraintsFile -SimpleMatch -Pattern '--hash=sha256:' -Quiet)) {
        throw "$ConstraintsFile pins no hashes (--hash=sha256:...), so the dependencies downloaded from PyPI could not be checked: use the constraints file of a release made with hashes"
    }
    if (-not $WheelSha256) { $WheelSha256 = (Get-FileHash -LiteralPath $WheelFile -Algorithm SHA256).Hash }
    $name = 'maktaba-pipeline'
    if ($Extras.Count -gt 0) { $name = '{0}[{1}]' -f $name, ($Extras -join ',') }
    $line = '{0} @ {1} --hash=sha256:{2}' -f $name, (ConvertTo-NodeFileUrl $WheelFile), $WheelSha256.ToLowerInvariant()
    $req = Join-Path (Split-Path -Parent $ConstraintsFile) '.maktaba-node-requirements.txt'
    [System.IO.File]::WriteAllText($req, $line + "`n")
    return $req
}

# Remove the earlier downloads in Dir, keeping the files in Keep.
function Remove-NodeOldDownloads {
    param([Parameter(Mandatory = $true)][string]$Dir, [string[]]$Keep = @())
    if (-not (Test-Path -LiteralPath $Dir -PathType Container)) { return }
    foreach ($f in @(Get-ChildItem -LiteralPath $Dir -File)) {
        if ($Keep -notcontains $f.FullName) { Remove-Item -LiteralPath $f.FullName -Force }
    }
}
