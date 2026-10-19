<#
.SYNOPSIS
    Installs (or updates) this machine's Maktaba node worker on Windows (Epic 31).

.DESCRIPTION
    Makes this machine a worker of a Maktaba home:

      1. builds the pipeline's Python environment in %LOCALAPPDATA%\Maktaba\node\venv
         (uv when available, else Python 3.12+ and pip);
      2. writes %APPDATA%\Maktaba\node.env from node.env.example the first time, with
         MAKTABA_FFMPEG / MAKTABA_FFPROBE when ffmpeg is installed off the PATH;
      3. joins the home when this machine has not joined yet (`maktaba-pipeline join`
         shows a pairing code that the admin types on the home);
      4. registers the worker so it starts at logon and restarts when it fails.

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

    Running it again updates in place: stops the workers, rebuilds the environment from
    -Source (after a git pull), re-registers and restarts them.

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

.PARAMETER Source
    The pipeline source directory (default: pipeline\ of the repository holding this
    script; remembered).

.PARAMETER Ffmpeg
    Full path to ffmpeg.exe when it is not on the PATH (recorded in node.env).

.PARAMETER Mode
    Task (default) or Service (see the description; remembered).

.PARAMETER NoJoin
    Do not join now; the worker waits and starts once `maktaba-pipeline join` completes.

.PARAMETER NoStart
    Register the worker but do not start it now.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File deploy\packaging\node\windows\install-node.ps1 -HomeUrl http://192.168.1.20:8080
#>
[CmdletBinding()]
param(
    [string]$HomeUrl = '',
    [string]$Name = '',
    [int]$Instances = -1,
    [string]$Extras = '',
    [string]$Source = '',
    [string]$Ffmpeg = '',
    [ValidateSet('Task', 'Service')][string]$Mode = '',
    [switch]$NoJoin,
    [switch]$NoStart
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
if (-not $Source) { $Source = Read-NodeKeyValue $script:NodeInstallConf 'SOURCE' }
if (-not $Source) { $Source = Join-Path (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PackagingDir))) 'pipeline' }
if (-not $Mode) {
    # (assigning '' to $Mode would fail its ValidateSet)
    if ((Read-NodeKeyValue $script:NodeInstallConf 'MODE') -eq 'Service') { $Mode = 'Service' } else { $Mode = 'Task' }
}

$pyproject = Join-Path $Source 'pyproject.toml'
if (-not (Test-Path -LiteralPath $pyproject -PathType Leaf)) { throw "no pipeline source at $Source (pass -Source)" }
if (-not (Select-String -LiteralPath $pyproject -Pattern '^name = "maktaba-pipeline"' -Quiet)) {
    throw "$Source is not the maktaba-pipeline project"
}
$Source = (Resolve-Path -LiteralPath $Source).Path

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
if ($uv) {
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

# --- the scripts the task runs, and maktaba-pipeline.cmd ----------------------------------
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'run-node.ps1') -Destination $script:NodeDir -Force
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'node-common.ps1') -Destination $script:NodeDir -Force
Copy-Item -LiteralPath (Join-Path $PackagingDir 'node_check.py') -Destination $script:NodeDir -Force
Get-ChildItem -LiteralPath $script:NodeDir -File | Unblock-File
$shim = "@echo off`r`nrem maktaba-pipeline for this Maktaba node (written by install-node.ps1)`r`n" +
    "`"%LOCALAPPDATA%\Maktaba\node\venv\Scripts\python.exe`" -m maktaba_pipeline %*`r`n"
[System.IO.File]::WriteAllText((Join-Path $script:NodeDir 'maktaba-pipeline.cmd'), $shim, [System.Text.Encoding]::ASCII)
$conf = "# install-node.ps1 remembers these for updates`r`nEXTRAS=$Extras`r`nSOURCE=$Source`r`nMODE=$Mode`r`n"
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
    $joinArgs = @('-m', 'maktaba_pipeline', 'join')
    if ($HomeUrl) { $joinArgs += @('--home', $HomeUrl) }
    if ($Name) { $joinArgs += @('--name', $Name) }
    Assert-ExitCode (Invoke-NativeCommand -FilePath $script:NodePython -ArgumentList $joinArgs) 'join'
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
    # but not after a clean exit (stopped, or revoked on the home).
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
Write-Host "  Command:      $(Join-Path $script:NodeDir 'maktaba-pipeline.cmd') (join, cluster logs, ...)"
Write-Host "  Logs:         Get-Content -Wait '$(Join-Path $script:NodeLogDir 'node.log')'"
if ($Mode -eq 'Task') {
    Write-Host "  Status:       Get-ScheduledTask -TaskName 'maktaba-node*' | Get-ScheduledTaskInfo"
    Write-Host "  Restart:      Stop-ScheduledTask -TaskName maktaba-node; Start-ScheduledTask -TaskName maktaba-node"
} else {
    Write-Host "  Status:       Get-Service 'maktaba-node*'"
    Write-Host "  Restart:      Restart-Service maktaba-node"
}
