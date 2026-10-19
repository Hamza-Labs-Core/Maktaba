<#
.SYNOPSIS
    Runs one Maktaba node worker and keeps it running (Epic 31).

.DESCRIPTION
    Started by the "maktaba-node" / "maktaba-node@<N>" scheduled task (or NSSM service)
    that install-node.ps1 registers; it lives in %LOCALAPPDATA%\Maktaba\node. Before
    every start it:

      1. sets the worker's defaults, then reads %APPDATA%\Maktaba\node.env and
         node@<instance>.env (KEY=VALUE lines, which win over the defaults);
      2. rotates the instance's log files in %LOCALAPPDATA%\Maktaba\Logs past
         MAKTABA_NODE_LOG_MAX_MB (default 50; three old files kept);
      3. checks the machine has joined a home (node_check.py) and, until it has,
         checks again every 30 s, so the worker starts by itself after
         `maktaba-pipeline join`;
      4. runs `python -m maktaba_pipeline run`, appending its output to the log files.

    A worker that exits 0 (stopped, or revoked on the home) is not started again; any
    other exit restarts it after 5 s, doubling to at most 30 s (reset after a run of
    10 minutes), which is how a home that is unreachable at logon is ridden out.

    The worker processes join a job object that closes with this script, so stopping
    the task (which kills only this script) also stops the worker.

.PARAMETER Instance
    "main" for the machine's main worker, else the extra worker's name (1, 2, ...).

.PARAMETER Stages
    MAKTABA_PIPELINE_STAGES default for this worker (node.env files may override it).

.PARAMETER ExcludeStages
    MAKTABA_PIPELINE_EXCLUDE_STAGES default for this worker.
#>
[CmdletBinding()]
param(
    [string]$Instance = 'main',
    [string]$Stages = '',
    [string]$ExcludeStages = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'node-common.ps1')

if (-not (Test-NodeInstanceName $Instance)) { throw "bad instance name '$Instance'" }

$base = Get-NodeLogBase $Instance
$outLog = Join-Path $script:NodeLogDir "$base.log"
$errLog = Join-Path $script:NodeLogDir "$base.err.log"
$checkScript = Join-Path $script:NodeDir 'node_check.py'
$cmdExe = Join-Path $env:SystemRoot 'System32\cmd.exe'
New-Item -ItemType Directory -Force -Path $script:NodeLogDir | Out-Null

function Write-Supervisor {
    param([Parameter(Mandatory = $true)][string]$Message)
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    try {
        [System.IO.File]::AppendAllText($errLog, "$stamp [run-node $Instance] $Message`r`n")
    } catch {
        # the log is not writable: nothing better to do from a hidden task
    }
}

function Move-LogIfLarge {
    param([Parameter(Mandatory = $true)][string]$Path, [Parameter(Mandatory = $true)][long]$MaxBytes)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return }
    if ((Get-Item -LiteralPath $Path).Length -le $MaxBytes) { return }
    Remove-Item -LiteralPath "$Path.3" -Force -ErrorAction SilentlyContinue
    foreach ($i in 2, 1) {
        $from = "$Path.$i"
        if (Test-Path -LiteralPath $from) { Move-Item -LiteralPath $from -Destination "$Path.$($i + 1)" -Force }
    }
    Move-Item -LiteralPath $Path -Destination "$Path.1" -Force
}

# Children of this script die with it: a job object that kills its processes when its
# last handle (this process's) closes.
function Enable-KillChildrenOnExit {
    if (-not ('MaktabaNodeJob' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class MaktabaNodeJob {
    [StructLayout(LayoutKind.Sequential)]
    struct BasicLimits {
        public long PerProcessUserTimeLimit;
        public long PerJobUserTimeLimit;
        public uint LimitFlags;
        public UIntPtr MinimumWorkingSetSize;
        public UIntPtr MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public UIntPtr Affinity;
        public uint PriorityClass;
        public uint SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct IoCounters {
        public ulong ReadOperationCount, WriteOperationCount, OtherOperationCount;
        public ulong ReadTransferCount, WriteTransferCount, OtherTransferCount;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct ExtendedLimits {
        public BasicLimits Basic;
        public IoCounters Io;
        public UIntPtr ProcessMemoryLimit;
        public UIntPtr JobMemoryLimit;
        public UIntPtr PeakProcessMemoryUsed;
        public UIntPtr PeakJobMemoryUsed;
    }

    const int JobObjectExtendedLimitInformation = 9;
    const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000;

    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern IntPtr CreateJobObject(IntPtr attributes, string name);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool SetInformationJobObject(IntPtr job, int infoClass, ref ExtendedLimits info, uint length);

    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

    [DllImport("kernel32.dll")]
    static extern IntPtr GetCurrentProcess();

    static IntPtr job = IntPtr.Zero;

    public static bool Enable() {
        if (job != IntPtr.Zero) return true;
        IntPtr handle = CreateJobObject(IntPtr.Zero, null);
        if (handle == IntPtr.Zero) return false;
        ExtendedLimits info = new ExtendedLimits();
        info.Basic.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        uint size = (uint)Marshal.SizeOf(typeof(ExtendedLimits));
        if (!SetInformationJobObject(handle, JobObjectExtendedLimitInformation, ref info, size)) return false;
        if (!AssignProcessToJobObject(handle, GetCurrentProcess())) return false;
        job = handle;
        return true;
    }
}
'@
    }
    return [MaktabaNodeJob]::Enable()
}

# Run one command line through cmd.exe (for its >> appends) with no window; its exit code.
function Invoke-Cmd {
    param([Parameter(Mandatory = $true)][string]$CommandLine)
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $cmdExe
    # /s: cmd removes only the outer pair of quotes and keeps the inner ones as written.
    $psi.Arguments = '/d /s /c "' + $CommandLine + '"'
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $process = [System.Diagnostics.Process]::Start($psi)
    $process.WaitForExit()
    return $process.ExitCode
}

try {
    if (-not (Enable-KillChildrenOnExit)) {
        Write-Supervisor 'warning: no job object; stopping the task may leave the worker running'
    }
} catch {
    Write-Supervisor "warning: no job object ($($_.Exception.Message)); stopping the task may leave the worker running"
}

$delay = 5
while ($true) {
    # Defaults, then the settings files (which win).
    $env:MAKTABA_ENV = 'prod'
    $env:PYTHONUNBUFFERED = '1'
    $env:PYTHONUTF8 = '1'          # log files and Arabic paths in UTF-8, not the ANSI code page
    $env:PYTHONIOENCODING = 'utf-8'
    $env:MAKTABA_PIPELINE_STAGES = $Stages
    $env:MAKTABA_PIPELINE_EXCLUDE_STAGES = $ExcludeStages
    if ($Instance -ne 'main') { $env:MAKTABA_STT_SLOT = "stt-$Instance" }
    try {
        Import-NodeEnvFile (Join-Path $script:NodeConfDir 'node.env')
        if ($Instance -ne 'main') { Import-NodeEnvFile (Join-Path $script:NodeConfDir "node@$Instance.env") }
    } catch {
        Write-Supervisor "cannot read the settings: $($_.Exception.Message)"
    }
    # A node takes its database from its node config; MAKTABA_DATABASE_URL would make
    # the worker a home worker on that database.
    Remove-Item Env:MAKTABA_DATABASE_URL -ErrorAction SilentlyContinue

    $maxMb = 50
    if ($env:MAKTABA_NODE_LOG_MAX_MB -match '^\d+$') { $maxMb = [int]$env:MAKTABA_NODE_LOG_MAX_MB }
    try {
        Move-LogIfLarge -Path $outLog -MaxBytes ([long]$maxMb * 1MB)
        Move-LogIfLarge -Path $errLog -MaxBytes ([long]$maxMb * 1MB)
    } catch {
        Write-Supervisor "log rotation failed: $($_.Exception.Message)"
    }

    if (-not (Test-Path -LiteralPath $script:NodePython -PathType Leaf)) {
        Write-Supervisor "no Python at $($script:NodePython); run install-node.ps1 again"
        exit 78
    }

    $code = Invoke-Cmd ('"{0}" "{1}" >> "{2}" 2>> "{3}"' -f $script:NodePython, $checkScript, $outLog, $errLog)
    if ($code -ne 0) {
        Write-Supervisor "not joined to a home yet (node_check exit $code); checking again in 30 s"
        Start-Sleep -Seconds 30
        continue
    }

    $started = Get-Date
    Write-Supervisor 'starting the worker'
    # -X maktaba_node=<instance> marks the process (Python keeps unknown -X options), so
    # the installers can find it.
    $code = Invoke-Cmd ('"{0}" -X maktaba_node={1} -m maktaba_pipeline run >> "{2}" 2>> "{3}"' -f $script:NodePython, $Instance, $outLog, $errLog)
    if ($code -eq 0) {
        Write-Supervisor 'the worker stopped cleanly (stopped, or revoked on the home); not restarting'
        exit 0
    }
    $ran = ((Get-Date) - $started).TotalSeconds
    if ($ran -ge 600) { $delay = 5 }
    Write-Supervisor ('the worker exited with code {0} after {1:N0} s; restarting in {2} s' -f $code, $ran, $delay)
    Start-Sleep -Seconds $delay
    $delay = [Math]::Min($delay * 2, 30)
}
