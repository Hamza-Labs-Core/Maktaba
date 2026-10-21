<#
.SYNOPSIS
    Installs (or updates) this machine's Maktaba node worker on Windows (Epic 31).

.DESCRIPTION
    Makes this machine a worker of a Maktaba home:

      1. builds the pipeline's Python environment in %LOCALAPPDATA%\Maktaba\node\venv
         (uv when available, else Python 3.12+ and pip), from a release's wheel or from a
         checkout's pipeline\;
      2. writes %APPDATA%\Maktaba\node.env from node.env.example the first time, with
         MAKTABA_FFMPEG / MAKTABA_FFPROBE when ffmpeg is installed off the PATH;
      3. joins the home when this machine has not joined yet (`maktaba-pipeline join`
         shows a pairing code that the admin types on the home);
      4. registers the worker so it starts at logon and restarts when it fails.

    Each worker runs the venv's `maktaba-pipeline run --node` through run-node.ps1:
    exit 78 (not joined yet) is retried every 30 s, exit 77 (the home removed this
    machine) leaves it stopped.

    Default (-Mode Task): a scheduled task per worker, "At log on" of this user, run as
    this user. Mapped drive letters and saved share credentials (net use, Credential
    Manager) belong to the logged-on user, and so does the node config in %APPDATA%, so
    the worker runs where they are. It needs no administrator rights; it runs only while
    the user is logged on (use automatic logon on a dedicated worker PC).

    -Mode Service: a Windows service per worker through NSSM (https://nssm.cc), started at
    boot with no logon, running as this user's account (its password is asked for and
    handed to NSSM). Needs administrator rights and nssm.exe. Services do not see mapped
    drive letters: give the share as a UNC path (\\nas\share) in the path map. sc.exe
    alone cannot host the worker: Python is not a service program and would be killed
    by the service manager after 30 s.

    Where the pipeline comes from, first match wins: -FromWheel / -Source as given; the
    last install's choice (install.conf); release.conf in the folder above this script
    (it came from a maktaba-node-<version> archive: that release's wheel); pipeline\ of
    the checkout holding this script.

    Running it again updates in place: stops the workers, reinstalls the pipeline (a newer
    release's archive installs that release; a checkout rebuilds after a git pull),
    re-registers and restarts them.

.PARAMETER HomeUrl
    The home's address, for example http://192.168.1.20:8080 (skips discovery).

.PARAMETER Name
    This machine's name on the home.

.PARAMETER Instances
    Number of extra transcription workers (maktaba-node@1..N, MAKTABA_STT_SLOT=stt-<i>,
    only the transcribe stage; the main worker then leaves transcribe to them). Default:
    keep the installed ones. Transcription runs on Apple Silicon only today, so on
    Windows these claim nothing unless node@<N>.env gives them other stages.

.PARAMETER Extras
    Pipeline extras, comma-separated, or "none" (default: none; remembered).

.PARAMETER FromWheel
    Install the pipeline from this wheel (maktaba_pipeline-*.whl), a file or an http(s)
    URL (default: the release this script came with; remembered).

.PARAMETER Sha256
    With -FromWheel: the wheel's SHA-256; a file that differs is refused (remembered; a
    wheel downloaded without one is pinned to the digest of its first download).

.PARAMETER Constraints
    With -FromWheel: pin the wheel's dependencies with this constraints file, a file or
    an http(s) URL (maktaba-pipeline-<version>-constraints.txt of the release; the
    release's own when installing the release).

.PARAMETER ConstraintsSha256
    With -Constraints: its SHA-256 (remembered, pinned the same way).

.PARAMETER Source
    Build the pipeline from this source directory instead (default in a checkout:
    pipeline\ of the repository holding this script; remembered).

.PARAMETER Ffmpeg
    Full path to ffmpeg.exe when it is not on the PATH (recorded in node.env).

.PARAMETER Mode
    Task (default) or Service (see the description; remembered).

.PARAMETER Full
    Then make this machine a full node (Epic 35 story 35.8): runs `maktaba-server setup
    --node`, which verifies the home's release in process (Sigstore + checksums), installs
    it side by side in %LOCALAPPDATA%\Maktaba, keeps node.json and this worker's Python
    environment, registers the "Maktaba" logon task (or -Mode Service) and removes the
    maktaba-node tasks once the node answers.

.PARAMETER ServerDir
    With -Full: a folder holding the release (its assets, or the extracted maktaba-server
    archive with release.json, checksums.txt and checksums.txt.sigstore.json).
    maktaba-server.exe is taken from it, else from the PATH. Default: setup --node fetches
    the home's version itself.

.PARAMETER NoJoin
    Do not join now; the worker waits and starts once `maktaba-pipeline join` completes.

.PARAMETER NoStart
    Register the worker but do not start it now.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\maktaba-node-1.4.0\windows\install-node.ps1 -HomeUrl http://192.168.1.20:8080

    From an unpacked release archive: installs that release's wheel.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File deploy\packaging\node\windows\install-node.ps1 -HomeUrl http://192.168.1.20:8080

    From a git checkout: builds its pipeline\.
#>
[CmdletBinding()]
param(
    [string]$HomeUrl = '',
    [string]$Name = '',
    [int]$Instances = -1,
    [string]$Extras = '',
    [string]$Source = '',
    [string]$FromWheel = '',
    [string]$Sha256 = '',
    [string]$Constraints = '',
    [string]$ConstraintsSha256 = '',
    [string]$Ffmpeg = '',
    [ValidateSet('Task', 'Service')][string]$Mode = '',
    [switch]$NoJoin,
    [switch]$NoStart,
    [switch]$Full,
    [string]$ServerDir = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'node-common.ps1')

$PackagingDir = Split-Path -Parent $PSScriptRoot           # deploy\packaging\node
$RunScript = Join-Path $script:NodeDir 'run-node.ps1'
$UserId = [Security.Principal.WindowsIdentity]::GetCurrent().Name

function Assert-ExitCode {
    param([Parameter(Mandatory = $true)][int]$Code, [Parameter(Mandatory = $true)][string]$What)
    if ($Code -ne 0) { throw "$What failed (exit $Code)" }
}

# --- choices (remembered from the last install) ---------------------------------------
if ($Instances -gt 16) { throw '-Instances: at most 16' }
if (-not $Extras) { $Extras = Read-NodeKeyValue $script:NodeInstallConf 'EXTRAS' }
if (-not $Extras) { $Extras = 'none' }
$Origin = Resolve-NodePipelineOrigin -FromWheel $FromWheel -Constraints $Constraints -Source $Source `
    -WheelSha256 $Sha256 -ConstraintsSha256 $ConstraintsSha256 `
    -BundleDir $PackagingDir -InstallConf $script:NodeInstallConf `
    -CheckoutPipeline (Join-Path (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PackagingDir))) 'pipeline')
if (-not $Mode) {
    # (assigning '' to $Mode would fail its ValidateSet)
    if ((Read-NodeKeyValue $script:NodeInstallConf 'MODE') -eq 'Service') { $Mode = 'Service' } else { $Mode = 'Task' }
}

if ($Origin.From -eq 'source') {
    $Source = $Origin.Source
    $pyproject = Join-Path $Source 'pyproject.toml'
    if (-not (Test-Path -LiteralPath $pyproject -PathType Leaf)) { throw "no pipeline source at $Source (pass -Source, or -FromWheel)" }
    if (-not (Select-String -LiteralPath $pyproject -Pattern '^name = "maktaba-pipeline"' -Quiet)) {
        throw "$Source is not the maktaba-pipeline project"
    }
    $Source = (Resolve-Path -LiteralPath $Source).Path
    $PipelineOrigin = $Source
} elseif ($Origin.From -eq 'release') {
    $PipelineOrigin = "release $($Origin.Tag) ($($Origin.Wheel))"
} else {
    $PipelineOrigin = $Origin.Wheel
}

if ($UserId -like 'NT AUTHORITY\*') { throw "run this as the user the worker should run as, not $UserId" }
$Nssm = $null
$Credential = $null
if ($Mode -eq 'Service') {
    if (-not (Test-IsAdministrator)) { throw '-Mode Service needs an elevated PowerShell (Run as administrator)' }
    $nssmCmd = Get-Command nssm.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $nssmCmd) { throw '-Mode Service needs nssm.exe on the PATH (https://nssm.cc); or use the default -Mode Task' }
    $Nssm = $nssmCmd.Path
    # The service must run as this user: the join below saves the node config in this
    # user's %APPDATA%, and the share's credentials are this user's.
    $Credential = Get-Credential -UserName $UserId -Message 'The worker service runs as this account (the one that reaches the share). Its password:'
    if (-not $Credential) { throw 'no password given' }
    $given = $Credential.UserName -replace '^\.\\', ($env:COMPUTERNAME + '\')
    if ($given -ne $UserId) {
        throw "the service must run as $UserId (whose node config and share access this installs), not $($Credential.UserName)"
    }
}

# --- which workers -----------------------------------------------------------------------
function Get-InstalledInstances {
    $names = @()
    $tasks = @(Get-ScheduledTask -TaskPath '\' -ErrorAction SilentlyContinue | Where-Object { $_.TaskName -like 'maktaba-node@*' })
    foreach ($t in $tasks) { $names += $t.TaskName.Substring(13) }
    $services = @(Get-Service -Name 'maktaba-node@*' -ErrorAction SilentlyContinue)
    foreach ($s in $services) { if ($names -notcontains $s.Name.Substring(13)) { $names += $s.Name.Substring(13) } }
    return $names
}

$installed = @(Get-InstalledInstances)
if ($Instances -lt 0) {
    $wanted = $installed
} else {
    $wanted = @()
    for ($i = 1; $i -le $Instances; $i++) { $wanted += "$i" }
}
$unwanted = @($installed | Where-Object { $wanted -notcontains $_ })
foreach ($inst in $wanted) { if (-not (Test-NodeInstanceName $inst)) { throw "bad instance name '$inst'" } }

Write-NodeStep ("Maktaba node: {0}, mode {1}, extras: {2}, extra workers: {3}" -f $UserId, $Mode, $Extras, $wanted.Count)
Write-NodeStep "Pipeline: $PipelineOrigin"

# --- stop what runs (Windows cannot replace files a running Python has open) -----------
function Stop-NodeWorkers {
    $tasks = @(Get-ScheduledTask -TaskPath '\' -ErrorAction SilentlyContinue |
        Where-Object { $_.TaskName -eq 'maktaba-node' -or $_.TaskName -like 'maktaba-node@*' })
    foreach ($t in $tasks) { Stop-ScheduledTask -TaskPath '\' -TaskName $t.TaskName -ErrorAction SilentlyContinue }
    $services = @(Get-Service -Name 'maktaba-node', 'maktaba-node@*' -ErrorAction SilentlyContinue)
    foreach ($s in $services) {
        if ($s.Status -ne 'Stopped') { Stop-Service -Name $s.Name -Force -ErrorAction SilentlyContinue }
    }
    Stop-NodeWorkerProcesses
}
Stop-NodeWorkers

# --- Python environment --------------------------------------------------------------------
function Find-Uv {
    $cmd = Get-Command uv.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { return $cmd.Path }
    $candidates = @(
        (Join-Path $env:USERPROFILE '.local\bin\uv.exe'),
        (Join-Path $env:USERPROFILE '.cargo\bin\uv.exe'),
        (Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Links\uv.exe')
    )
    foreach ($c in $candidates) { if (Test-Path -LiteralPath $c -PathType Leaf) { return $c } }
    return $null
}

# A venv's own pip (no unchecked upgrade from PyPI) does not take hashes from a constraints
# file: install its pins as requirements, each checked, then the wheel, checked (ReqFile),
# without resolving again (every extra's pins come along). From InstallDir, naming the files
# .\<file>.
function Install-NodePinsWithPip {
    param(
        [Parameter(Mandatory = $true)][string]$ConsFile,
        [Parameter(Mandatory = $true)][string]$ReqFile,
        [Parameter(Mandatory = $true)][string]$InstallDir
    )
    Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList @('-m', 'pip', 'install', '--quiet', '--require-hashes', '--no-deps', '-r', ('.\' + (Split-Path -Leaf $ConsFile))) -WorkingDirectory $InstallDir) 'pip install (dependencies)'
    Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList @('-m', 'pip', 'install', '--quiet', '--require-hashes', '--no-deps', '--force-reinstall', '-r', ('.\' + (Split-Path -Leaf $ReqFile))) -WorkingDirectory $InstallDir) 'pip install (the wheel)'
}

function Find-Python312 {
    $probe = 'import sys; print(sys.executable) if sys.version_info >= (3, 12) else sys.exit(1)'
    $launcher = Get-Command py.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($launcher) {
        foreach ($v in @('-3.12', '-3.13', '-3.14', '-3')) {
            $r = Invoke-NativeCapture -FilePath $launcher.Path -ArgumentList @($v, '-c', $probe)
            if ($r.ExitCode -eq 0 -and $r.StdOut.Trim()) { return $r.StdOut.Trim() }
        }
    }
    # python.exe on the PATH, but not the Microsoft Store stub that opens the Store
    $python = Get-Command python.exe -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -notlike '*\WindowsApps\*' } | Select-Object -First 1
    if ($python) {
        $r = Invoke-NativeCapture -FilePath $python.Path -ArgumentList @('-c', $probe)
        if ($r.ExitCode -eq 0 -and $r.StdOut.Trim()) { return $r.StdOut.Trim() }
    }
    return $null
}

$extrasList = @()
if ($Extras -ne 'none') {
    $extrasList = @($Extras.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ })
}
New-Item -ItemType Directory -Force -Path $script:NodeDir | Out-Null
$uv = Find-Uv
if ($Origin.From -ne 'source') {
    # The release's (or a given) wheel, its dependencies pinned by the constraints file.
    $downloads = Join-Path $script:NodeDir 'dist'
    $got = Get-NodePipelineFile -Spec $Origin.Wheel -Sha256 $Origin.WheelSha256 -DownloadDir $downloads
    $wheelFile = $got.Path
    $wheelPin = $Origin.WheelSha256
    if (-not $wheelPin) { $wheelPin = $got.Sha256 }
    $consFile = ''
    $consPin = ''
    # The installs run from the constraints file's folder and name it .\<file>: the path
    # (C:\Users\Jane Doe\...) never reaches a command line split at spaces.
    $installDir = $script:NodeDir
    if ($Origin.Constraints) {
        $got = Get-NodePipelineFile -Spec $Origin.Constraints -Sha256 $Origin.ConstraintsSha256 -DownloadDir $downloads
        $consFile = $got.Path
        $consPin = $Origin.ConstraintsSha256
        if (-not $consPin) { $consPin = $got.Sha256 }
        $installDir = Split-Path -Parent $consFile
    } else {
        Write-Warning 'no constraints file: the wheel''s dependencies are not pinned (pass -Constraints)'
    }
    $spec = $wheelFile
    if ($extrasList.Count -gt 0) { $spec = '{0}[{1}]' -f $wheelFile, ($extrasList -join ',') }
    $installArgs = @($spec)
    $reqFile = ''
    $pipPins = $false
    if ($consFile) {
        # Hash-checking mode: every dependency must match a --hash of the constraints file,
        # the wheel its own SHA-256 (a one-line requirements file next to the constraints).
        $reqFile = New-NodeHashedRequirements -WheelFile $wheelFile -WheelSha256 $wheelPin -Extras $extrasList -ConstraintsFile $consFile
        # The constraints name PyTorch's index beside PyPI (torch's CUDA build), which also
        # serves packages PyPI has: uv looks for each pin on every index (by itself it stops
        # at the first that has the package) and takes the file it finds, refusing it when
        # it is not one of the pin's hashes.
        $installArgs = @('--require-hashes', '--index-strategy', 'unsafe-best-match', '--constraint', ('.\' + (Split-Path -Leaf $consFile)), '-r', ('.\' + (Split-Path -Leaf $reqFile)))
        # The stt and search extras bring torch, whose pins PyTorch's index also matches
        # with other files on Linux (its torch 2.14.1+cu130 for "torch==2.14.1", its triton):
        # pip installs those, as install-node.sh does, taking for each pin the file its
        # hashes name, whichever index has it (every extra's pins come along).
        $pipPins = ($extrasList -contains 'stt') -or ($extrasList -contains 'search')
    }
    $basePython = $null
    if (-not $uv) { $basePython = Find-Python312 }
    if ($pipPins) {
        if (-not (Test-Path -LiteralPath $script:NodePython -PathType Leaf)) {
            if ($uv) {
                Assert-ExitCode (Invoke-NativeCommand -FilePath $uv -ArgumentList @('venv', '--python', $Origin.Python, $script:NodeVenv)) 'uv venv'
            } elseif ($basePython) {
                Assert-ExitCode (Invoke-NativeCommand -FilePath $basePython -ArgumentList @('-m', 'venv', $script:NodeVenv)) 'python -m venv'
            } else {
                throw 'Neither uv nor Python 3.12+ was found. Install uv (https://docs.astral.sh/uv/) and run this again.'
            }
        }
        Write-NodeStep "Installing $(Split-Path -Leaf $wheelFile) and its pinned dependencies with pip ($($script:NodeVenv))"
        # a venv uv made has no pip: its Python's own (no download, no unchecked upgrade)
        Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList @('-m', 'ensurepip', '--default-pip')) 'ensurepip'
        Install-NodePinsWithPip -ConsFile $consFile -ReqFile $reqFile -InstallDir $installDir
    } elseif ($uv) {
        Write-NodeStep "Installing $(Split-Path -Leaf $wheelFile) with uv ($($script:NodeVenv))"
        if (-not (Test-Path -LiteralPath $script:NodePython -PathType Leaf)) {
            Assert-ExitCode (Invoke-NativeCommand -FilePath $uv -ArgumentList @('venv', '--python', $Origin.Python, $script:NodeVenv)) 'uv venv'
        }
        # --reinstall-package: a rebuilt wheel of an unchanged version replaces the old one
        $uvArgs = @('pip', 'install', '--python', $script:NodePython, '--reinstall-package', 'maktaba-pipeline') + $installArgs
        Assert-ExitCode (Invoke-NativeCommand -FilePath $uv -ArgumentList $uvArgs -WorkingDirectory $installDir) 'uv pip install'
    } else {
        if (-not $basePython) {
            throw 'Neither uv nor Python 3.12+ was found. Install uv (https://docs.astral.sh/uv/) and run this again.'
        }
        Write-Warning "uv not found: using $basePython and pip."
        if (-not (Test-Path -LiteralPath $script:NodePython -PathType Leaf)) {
            Assert-ExitCode (Invoke-NativeCommand -FilePath $basePython -ArgumentList @('-m', 'venv', $script:NodeVenv)) 'python -m venv'
        }
        if ($reqFile) {
            Install-NodePinsWithPip -ConsFile $consFile -ReqFile $reqFile -InstallDir $installDir
        } else {
            Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList @('-m', 'pip', 'install', '--quiet', '--upgrade', 'pip')) 'pip upgrade'
            Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList (@('-m', 'pip', 'install', '--quiet') + @($spec)) -WorkingDirectory $installDir) 'pip install'
            Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList @('-m', 'pip', 'install', '--quiet', '--force-reinstall', '--no-deps', $wheelFile)) 'pip reinstall'
        }
    }
    if ($reqFile) { Remove-Item -LiteralPath $reqFile -Force -ErrorAction SilentlyContinue }
    # installed: earlier updates' downloads are of no more use
    Remove-NodeOldDownloads -Dir $downloads -Keep @($wheelFile, $consFile)
} elseif ($uv) {
    Write-NodeStep "Building the worker's Python environment with uv ($($script:NodeVenv))"
    # --no-editable: the worker runs a snapshot, not the checkout being edited;
    # --reinstall-package: uv would otherwise keep a stale build of an unchanged version.
    $uvArgs = @('sync', '--project', $Source, '--frozen', '--no-dev', '--no-editable',
        '--reinstall-package', 'maktaba-pipeline')
    $pythonVersionFile = Join-Path $Source '.python-version'
    if (Test-Path -LiteralPath $pythonVersionFile -PathType Leaf) {
        $uvArgs += @('--python', ([System.IO.File]::ReadAllText($pythonVersionFile)).Trim())
    }
    foreach ($e in $extrasList) { $uvArgs += @('--extra', $e) }
    $env:UV_PROJECT_ENVIRONMENT = $script:NodeVenv
    try {
        Assert-ExitCode (Invoke-NativeCommand -FilePath $uv -ArgumentList $uvArgs) 'uv sync'
    } finally {
        Remove-Item Env:UV_PROJECT_ENVIRONMENT -ErrorAction SilentlyContinue
    }
} else {
    $basePython = Find-Python312
    if (-not $basePython) {
        throw 'Neither uv nor Python 3.12+ was found. Install uv (https://docs.astral.sh/uv/) and run this again.'
    }
    Write-Warning "uv not found: using $basePython and pip (the lock file is not enforced)."
    if (-not (Test-Path -LiteralPath $script:NodePython -PathType Leaf)) {
        Assert-ExitCode (Invoke-NativeCommand -FilePath $basePython -ArgumentList @('-m', 'venv', $script:NodeVenv)) 'python -m venv'
    }
    Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList @('-m', 'pip', 'install', '--quiet', '--upgrade', 'pip')) 'pip upgrade'
    $spec = $Source
    if ($extrasList.Count -gt 0) { $spec = '{0}[{1}]' -f $Source, ($extrasList -join ',') }
    Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList @('-m', 'pip', 'install', '--quiet', $spec)) 'pip install'
    Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList @('-m', 'pip', 'install', '--quiet', '--force-reinstall', '--no-deps', $Source)) 'pip reinstall'
}

# --- the scripts the task runs; the pipeline command ---------------------------------------
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'run-node.ps1') -Destination $script:NodeDir -Force
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'node-common.ps1') -Destination $script:NodeDir -Force
Copy-Item -LiteralPath (Join-Path $PackagingDir 'node_check.py') -Destination $script:NodeDir -Force
Get-ChildItem -LiteralPath $script:NodeDir -File | Unblock-File
# The venv's maktaba-pipeline.exe is the command (the workers run it too). Only for a
# pipeline without that entry point, a stand-in maktaba-pipeline.cmd beside the venv.
$standIn = Join-Path $script:NodeDir 'maktaba-pipeline.cmd'
if (Test-Path -LiteralPath $script:NodeCli -PathType Leaf) {
    $NodeCommandPath = $script:NodeCli
    Remove-Item -LiteralPath $standIn -Force -ErrorAction SilentlyContinue
} else {
    Write-Warning "the pipeline has no maktaba-pipeline entry point: using a stand-in ($standIn)"
    $NodeCommandPath = $standIn
    $shim = "@echo off`r`nrem Stand-in for the venv's maktaba-pipeline entry point (written by install-node.ps1)`r`n" +
        "`"%LOCALAPPDATA%\Maktaba\node\venv\Scripts\python.exe`" -m maktaba_pipeline %*`r`n"
    [System.IO.File]::WriteAllText($standIn, $shim, [System.Text.Encoding]::ASCII)
}

# The workers run `maktaba-pipeline run --node` (exit 78 until the machine has joined,
# 77 once the home has removed it); a pipeline without that option would fail on every
# start, so refuse it here.
$nodeCommand = Get-NodeCommand
$help = Invoke-NativeCapture -FilePath $nodeCommand.File -ArgumentList (@($nodeCommand.Prefix) + @('run', '--help'))
if ($help.StdOut -notmatch '--node') {
    throw "the pipeline from $PipelineOrigin has no 'run --node': install a newer one and run this again"
}
# FROM=release re-reads release.conf on the next run, so a newer release's archive
# installs its own wheel; FROM=wheel keeps the wheel (and constraints) given.
$wheelKv = ''
$wheelShaKv = ''
$consKv = ''
$consShaKv = ''
$sourceKv = ''
if ($Origin.From -eq 'source') { $sourceKv = $Source }
if ($Origin.From -eq 'wheel') {
    # A download is pinned to what this install got (trust on first use) unless a digest
    # was given; a local file only to a given digest, so a rebuilt wheel at the same path
    # still installs.
    $wheelKv = $Origin.Wheel
    $wheelShaKv = $Origin.WheelSha256
    if (Test-NodeUrl $wheelKv) { $wheelShaKv = $wheelPin } else { $wheelKv = $wheelFile }
    $consKv = $Origin.Constraints
    $consShaKv = $Origin.ConstraintsSha256
    if ($consKv) {
        if (Test-NodeUrl $consKv) { $consShaKv = $consPin } else { $consKv = $consFile }
    }
}
$conf = "# install-node.ps1 remembers these for updates`r`nFROM=$($Origin.From)`r`nEXTRAS=$Extras`r`n" +
    "SOURCE=$sourceKv`r`nWHEEL=$wheelKv`r`nWHEEL_SHA256=$wheelShaKv`r`nCONSTRAINTS=$consKv`r`n" +
    "CONSTRAINTS_SHA256=$consShaKv`r`nMODE=$Mode`r`n"
[System.IO.File]::WriteAllText($script:NodeInstallConf, $conf)

# --- settings ------------------------------------------------------------------------------
New-Item -ItemType Directory -Force -Path $script:NodeConfDir | Out-Null
$envFile = Join-Path $script:NodeConfDir 'node.env'
if (-not (Test-Path -LiteralPath $envFile -PathType Leaf)) {
    Write-NodeStep "Writing default settings to $envFile"
    Copy-Item -LiteralPath (Join-Path $PackagingDir 'node.env.example') -Destination $envFile
}

function Find-FfmpegOffPath {
    if ($Ffmpeg) {
        if (-not (Test-Path -LiteralPath $Ffmpeg -PathType Leaf)) { throw "-Ffmpeg: $Ffmpeg not found" }
        return (Resolve-Path -LiteralPath $Ffmpeg).Path
    }
    if (Get-Command ffmpeg.exe -ErrorAction SilentlyContinue) { return '' }   # on the PATH
    $candidates = @(
        (Join-Path $env:ProgramFiles 'ffmpeg\bin\ffmpeg.exe'),
        'C:\ffmpeg\bin\ffmpeg.exe',
        (Join-Path $env:ProgramData 'chocolatey\bin\ffmpeg.exe'),
        (Join-Path $env:USERPROFILE 'scoop\shims\ffmpeg.exe'),
        (Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Links\ffmpeg.exe')
    )
    foreach ($c in $candidates) { if (Test-Path -LiteralPath $c -PathType Leaf) { return $c } }
    $wingetPackages = Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages'
    if (Test-Path -LiteralPath $wingetPackages) {
        $found = Get-ChildItem -LiteralPath $wingetPackages -Filter 'ffmpeg.exe' -Recurse -Depth 5 -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if ($found) { return $found.FullName }
    }
    return $null
}

$ffmpegPath = Find-FfmpegOffPath
$envText = [System.IO.File]::ReadAllText($envFile)
if ($null -eq $ffmpegPath) {
    Write-Warning 'ffmpeg not found: stages that read media will report it missing. Install it (winget install Gyan.FFmpeg) or pass -Ffmpeg C:\path\to\ffmpeg.exe.'
} elseif ($ffmpegPath -and $envText -notmatch '(?m)^MAKTABA_FFMPEG=') {
    Write-NodeStep "ffmpeg is at $ffmpegPath, off the PATH: recording it in $envFile"
    $add = "`r`n# found by install-node.ps1`r`nMAKTABA_FFMPEG=$ffmpegPath`r`n"
    $probe = Join-Path (Split-Path -Parent $ffmpegPath) 'ffprobe.exe'
    if ((Test-Path -LiteralPath $probe -PathType Leaf) -and $envText -notmatch '(?m)^MAKTABA_FFPROBE=') {
        $add += "MAKTABA_FFPROBE=$probe`r`n"
    }
    [System.IO.File]::AppendAllText($envFile, $add)
}

# --- join ------------------------------------------------------------------------------------
# What the worker will see: node.env applied (MAKTABA_NODE_CONFIG included), no database URL.
Import-NodeEnvFile $envFile
Remove-Item Env:MAKTABA_DATABASE_URL -ErrorAction SilentlyContinue
if (Test-NodeJoined) {
    Write-NodeStep 'This machine has joined its home already'
} elseif ($NoJoin) {
    Write-Warning 'Not joined yet (-NoJoin): the worker waits until you run: maktaba-pipeline join'
} else {
    Write-NodeStep "Joining a Maktaba home. On the home: Admin -> Machines, turn on 'Accept new machines', then type the code shown below."
    $joinArgs = @($nodeCommand.Prefix) + @('join')
    if ($HomeUrl) { $joinArgs += @('--home', $HomeUrl) }
    if ($Name) { $joinArgs += @('--name', $Name) }
    Assert-ExitCode (Invoke-NativeCommand -FilePath $nodeCommand.File -ArgumentList $joinArgs) 'join'
    if (-not (Test-NodeJoined)) { throw 'the join did not complete; run this again (or pass -NoJoin to install anyway)' }
}

# --- register the workers ------------------------------------------------------------------
function Get-RunArguments {
    param([Parameter(Mandatory = $true)][string]$Instance)
    $list = @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-WindowStyle', 'Hidden',
        '-File', $RunScript, '-Instance', $Instance)
    if ($Instance -eq 'main') {
        if ($wanted.Count -gt 0) { $list += @('-ExcludeStages', 'transcribe') }
    } else {
        $list += @('-Stages', 'transcribe')
    }
    return (@($list | ForEach-Object { ConvertTo-CommandLineArgument $_ }) -join ' ')
}

function Register-NodeTask {
    param([Parameter(Mandatory = $true)][string]$Instance)
    $taskName = Get-NodeUnitName $Instance
    $action = New-ScheduledTaskAction -Execute $script:WindowsPowerShell -Argument (Get-RunArguments $Instance) -WorkingDirectory $script:NodeDir
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $UserId
    $trigger.Delay = 'PT30S'   # let mapped drives reconnect first
    $principal = New-ScheduledTaskPrincipal -UserId $UserId -LogonType Interactive -RunLevel Limited
    # No time limit (the default stops a task after 72 h), normal priority (the default
    # is below normal), never on battery rules, restart when the task itself fails.
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
        -StartWhenAvailable -DontStopOnIdleEnd -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew -Priority 6
    $task = New-ScheduledTask -Action $action -Trigger $trigger -Principal $principal -Settings $settings `
        -Description "Maktaba node worker ($Instance): works for the Maktaba home this machine joined."
    Register-ScheduledTask -TaskPath '\' -TaskName $taskName -InputObject $task -Force | Out-Null
}

function Set-NssmValue {
    param([Parameter(Mandatory = $true)][string]$Service, [Parameter(Mandatory = $true)][string[]]$Values)
    Assert-ExitCode (Invoke-NativeCommand -FilePath $Nssm -ArgumentList (@('set', $Service) + $Values)) "nssm set $Service $($Values[0])"
}

function Register-NodeService {
    param([Parameter(Mandatory = $true)][string]$Instance)
    $service = Get-NodeUnitName $Instance
    if (-not (Get-Service -Name $service -ErrorAction SilentlyContinue)) {
        Assert-ExitCode (Invoke-NativeCommand -FilePath $Nssm -ArgumentList @('install', $service, $script:WindowsPowerShell)) "nssm install $service"
    }
    Set-NssmValue $service @('Application', $script:WindowsPowerShell)
    Set-NssmValue $service @('AppParameters', (Get-RunArguments $Instance))
    Set-NssmValue $service @('AppDirectory', $script:NodeDir)
    Set-NssmValue $service @('DisplayName', "Maktaba node worker ($Instance)")
    Set-NssmValue $service @('Description', 'Works for the Maktaba home this machine joined.')
    Set-NssmValue $service @('Start', 'SERVICE_DELAYED_AUTO_START')
    # run-node.ps1 restarts the worker itself; NSSM restarts run-node.ps1 if it dies,
    # but not after it ends with 0: the worker stopped, or the home removed this machine
    # (run-node.ps1 turns the worker's 77 into 0).
    Set-NssmValue $service @('AppExit', 'Default', 'Restart')
    Set-NssmValue $service @('AppExit', '0', 'Exit')
    Set-NssmValue $service @('AppRestartDelay', '30000')
    Set-NssmValue $service @('AppKillProcessTree', '1')
    $password = $Credential.GetNetworkCredential().Password
    Set-NssmValue $service @('ObjectName', $Credential.UserName, $password)
}

function Remove-NodeUnit {
    param([Parameter(Mandatory = $true)][string]$Instance)
    $unit = Get-NodeUnitName $Instance
    if (Get-ScheduledTask -TaskPath '\' -TaskName $unit -ErrorAction SilentlyContinue) {
        Unregister-ScheduledTask -TaskPath '\' -TaskName $unit -Confirm:$false
    }
    if (Get-Service -Name $unit -ErrorAction SilentlyContinue) {
        if ($Nssm) {
            Assert-ExitCode (Invoke-NativeCommand -FilePath $Nssm -ArgumentList @('remove', $unit, 'confirm')) "nssm remove $unit"
        } else {
            Write-Warning "service $unit is installed; remove it from an elevated PowerShell: nssm remove $unit confirm"
        }
    }
}

foreach ($inst in $unwanted) {
    Write-NodeStep "Removing worker $inst"
    Remove-NodeUnit $inst
}

if ($Full) {
    # Epic 35 story 35.8 AC7: maktaba-server setup --node makes this worker a full node;
    # it removes the maktaba-node tasks itself once the node answers
    $server = Find-MaktabaServer -ServerDir $ServerDir
    if (-not $server) { throw '-Full needs maktaba-server.exe: give -ServerDir (the release, extracted) or put it on the PATH' }
    $python = Join-Path $script:NodeVenv 'Scripts\python.exe'
    $fullArgs = Get-FullNodeArguments -ServerDir $ServerDir -Python $python -Instances $Instances -Mode $Mode
    Write-NodeStep "Converting this worker into a full node: $server $($fullArgs -join ' ')"
    & $server @fullArgs
    Assert-ExitCode $LASTEXITCODE 'maktaba-server setup --node'
    Write-NodeStep 'Done: this machine is a full node (the "Maktaba" task runs maktaba-server).'
    return
}

foreach ($inst in @('main') + $wanted) {
    $unit = Get-NodeUnitName $inst
    if ($Mode -eq 'Task') {
        if (Get-Service -Name $unit -ErrorAction SilentlyContinue) {
            Write-Warning "a service named $unit exists too; remove it (uninstall-node.ps1 from an elevated PowerShell) so the worker does not run twice"
        }
        Register-NodeTask $inst
        if (-not $NoStart) { Start-ScheduledTask -TaskPath '\' -TaskName $unit }
    } else {
        if (Get-ScheduledTask -TaskPath '\' -TaskName $unit -ErrorAction SilentlyContinue) {
            Unregister-ScheduledTask -TaskPath '\' -TaskName $unit -Confirm:$false
        }
        Register-NodeService $inst
        if (-not $NoStart) { Start-Service -Name $unit }
    }
    Write-NodeStep "Registered $unit$(if ($NoStart) { ' (not started: -NoStart)' } else { ' and started it' })"
}

# --- summary ---------------------------------------------------------------------------------
Write-Host ''
Write-NodeStep 'Done.'
Write-Host "  Node config:  $(Join-Path $script:NodeConfDir 'node.json')"
Write-Host "  Settings:     $envFile (node@<N>.env per extra worker)"
Write-Host "  Python:       $($script:NodeVenv)"
Write-Host "  Pipeline:     $PipelineOrigin"
Write-Host "  Command:      $NodeCommandPath (join, cluster logs, ...)"
Write-Host "  Logs:         Get-Content -Wait '$(Join-Path $script:NodeLogDir 'node.log')'"
if ($Mode -eq 'Task') {
    Write-Host "  Status:       Get-ScheduledTask -TaskName 'maktaba-node*' | Get-ScheduledTaskInfo"
    Write-Host "  Restart:      Stop-ScheduledTask -TaskName maktaba-node; Start-ScheduledTask -TaskName maktaba-node"
} else {
    Write-Host "  Status:       Get-Service 'maktaba-node*'"
    Write-Host "  Restart:      Restart-Service maktaba-node"
}
