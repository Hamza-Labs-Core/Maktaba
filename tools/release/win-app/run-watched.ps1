# Runs a program (the app's installer, its uninstaller, Maktaba.exe) for check.sh (Epic 38,
# story 38.11) and reports everything it started, as check.sh needs to judge the installer's
# hooks. Windows PowerShell 5.1.
#
# The program is started suspended inside a job object of its own, so every process it starts,
# and theirs, is in the job: the uninstaller's copy of itself in %TEMP%, each hook's run of
# Maktaba.exe, a shell window's WebView2. The job tells of each new process at once (its
# completion port), and its command line is read then: a hook's run of a flag-taking
# Maktaba.exe is over in 0.02-1 s, far too soon for any process list. The run is over when the
# job holds no process (its accounting, not a message).
#
# A process from the install folder (-Dir) still running after -Grace seconds is ended, with
# everything it started (a shell that took no flag opened its window instead). Prints, one per
# line:
#   ran <command line>                 each process from the install folder, in order
#   child <name> exit <code>           each other process the program itself started (the
#                                      uninstaller's copy of itself)
#   ended <name> after <s> s: <cmd>    each process ended after the grace
#   missed <n>                         processes the job held that were not seen (never expected)
#   timeout after <s> s                the run did not end within -Timeout (all is ended)
#   exit <code>                        the program's own exit code, last
param(
    [Parameter(Mandatory = $true)][string]$Exe,
    [Parameter(Mandatory = $true)][string]$Arguments,
    [Parameter(Mandatory = $true)][string]$Dir,
    [int]$Grace = 20,
    [int]$Timeout = 900
)
$ErrorActionPreference = 'Stop'

Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;

public sealed class WatchedRun {
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct STARTUPINFO {
        public int cb; public string lpReserved, lpDesktop, lpTitle;
        public int dwX, dwY, dwXSize, dwYSize, dwXCountChars, dwYCountChars, dwFillAttribute, dwFlags;
        public short wShowWindow, cbReserved2; public IntPtr lpReserved2, hStdInput, hStdOutput, hStdError;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct PROCESS_INFORMATION { public IntPtr hProcess, hThread; public int dwProcessId, dwThreadId; }
    [StructLayout(LayoutKind.Sequential)]
    struct COMPLETION_PORT { public IntPtr CompletionKey, CompletionPort; }
    [StructLayout(LayoutKind.Sequential)]
    struct ACCOUNTING {
        public long TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime;
        public int TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct BASIC_INFORMATION { public IntPtr ExitStatus, PebBaseAddress, AffinityMask, BasePriority, UniqueProcessId, InheritedFromUniqueProcessId; }

    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool CreateProcess(string app, StringBuilder cmd, IntPtr pa, IntPtr ta, bool inherit, int flags,
        IntPtr env, string dir, ref STARTUPINFO si, out PROCESS_INFORMATION pi);
    [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr CreateJobObject(IntPtr attributes, string name);
    [DllImport("kernel32.dll", SetLastError = true)] static extern bool SetInformationJobObject(IntPtr job, int cls, ref COMPLETION_PORT info, int len);
    [DllImport("kernel32.dll", SetLastError = true)] static extern bool QueryInformationJobObject(IntPtr job, int cls, out ACCOUNTING info, int len, IntPtr ret);
    [DllImport("kernel32.dll", SetLastError = true)] static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
    [DllImport("kernel32.dll", SetLastError = true)] static extern bool TerminateJobObject(IntPtr job, int code);
    [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr CreateIoCompletionPort(IntPtr file, IntPtr port, IntPtr key, int threads);
    [DllImport("kernel32.dll")] static extern bool GetQueuedCompletionStatus(IntPtr port, out int message, out IntPtr key, out IntPtr overlapped, int ms);
    [DllImport("kernel32.dll")] static extern int ResumeThread(IntPtr thread);
    [DllImport("kernel32.dll")] static extern IntPtr OpenProcess(int access, bool inherit, int pid);
    [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);
    [DllImport("kernel32.dll")] static extern bool GetExitCodeProcess(IntPtr h, out int code);
    [DllImport("kernel32.dll")] static extern bool TerminateProcess(IntPtr h, int code);
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode)] static extern bool QueryFullProcessImageName(IntPtr h, int flags, StringBuilder name, ref int size);
    [DllImport("ntdll.dll")] static extern int NtQueryInformationProcess(IntPtr h, int cls, IntPtr buf, int len, out int ret);
    [DllImport("ntdll.dll")] static extern int NtQueryInformationProcess(IntPtr h, int cls, ref BASIC_INFORMATION info, int len, out int ret);

    const int CreateSuspended = 0x4, CreateUnicodeEnvironment = 0x400, StillActive = 259;
    const int Access = 0x1000 | 0x0001 | 0x100000;   // query (limited), terminate, synchronize
    const int MsgActiveZero = 4, MsgNewProcess = 6;

    public sealed class Proc {
        public int Id, Parent; public string Path = "", CommandLine = ""; public DateTime Seen; public bool Ended;
        internal IntPtr Handle;
        public string Name { get { return System.IO.Path.GetFileName(Path); } }
        public int Exit { get { int c; return Handle != IntPtr.Zero && GetExitCodeProcess(Handle, out c) ? c : -1; } }
        public bool Alive { get { return Exit == StillActive; } }
    }

    readonly IntPtr job, port, root;
    readonly string prefix;
    public readonly List<Proc> Procs = new List<Proc>();
    public readonly List<string> Lines = new List<string>();
    public int RootId;

    public WatchedRun(string exe, string args, string dir) {
        prefix = dir.TrimEnd('\\') + "\\";
        job = CreateJobObject(IntPtr.Zero, null);
        port = CreateIoCompletionPort(new IntPtr(-1), IntPtr.Zero, IntPtr.Zero, 1);
        var cp = new COMPLETION_PORT { CompletionKey = job, CompletionPort = port };
        if (job == IntPtr.Zero || port == IntPtr.Zero || !SetInformationJobObject(job, 7, ref cp, Marshal.SizeOf(cp)))
            throw new Exception("cannot make the job object: " + Marshal.GetLastWin32Error());
        var thread = new Thread(Listen); thread.IsBackground = true; thread.Start();
        var si = new STARTUPINFO(); si.cb = Marshal.SizeOf(si);
        PROCESS_INFORMATION pi;
        var cmd = new StringBuilder("\"" + exe + "\"" + (args.Length > 0 ? " " + args : ""));
        if (!CreateProcess(exe, cmd, IntPtr.Zero, IntPtr.Zero, false, CreateSuspended | CreateUnicodeEnvironment, IntPtr.Zero, null, ref si, out pi))
            throw new Exception("cannot start " + exe + ": " + Marshal.GetLastWin32Error());
        RootId = pi.dwProcessId;
        root = pi.hProcess;
        if (!AssignProcessToJobObject(job, pi.hProcess)) {
            TerminateProcess(pi.hProcess, 1);
            throw new Exception("cannot put " + exe + " in the job: " + Marshal.GetLastWin32Error());
        }
        ResumeThread(pi.hThread);
        CloseHandle(pi.hThread);
    }

    // the job's messages: each new process, read at once
    void Listen() {
        while (true) {
            int msg; IntPtr key, data;
            if (!GetQueuedCompletionStatus(port, out msg, out key, out data, -1)) return;
            if (msg != MsgNewProcess) continue;
            var p = new Proc { Id = data.ToInt32(), Seen = DateTime.UtcNow };
            p.Handle = OpenProcess(Access, false, p.Id);
            if (p.Handle != IntPtr.Zero) Read(p);
            lock (Procs) Procs.Add(p);
        }
    }

    static void Read(Proc p) {
        var name = new StringBuilder(32768); int size = name.Capacity;
        if (QueryFullProcessImageName(p.Handle, 0, name, ref size)) p.Path = name.ToString(0, size);
        var bi = new BASIC_INFORMATION(); int got;
        if (NtQueryInformationProcess(p.Handle, 0, ref bi, Marshal.SizeOf(bi), out got) == 0) p.Parent = bi.InheritedFromUniqueProcessId.ToInt32();
        int len;
        NtQueryInformationProcess(p.Handle, 60, IntPtr.Zero, 0, out len);   // ProcessCommandLineInformation
        if (len <= 0) return;
        IntPtr buf = Marshal.AllocHGlobal(len);
        try {
            // a UNICODE_STRING (Length, MaximumLength, Buffer) and the text it points at
            if (NtQueryInformationProcess(p.Handle, 60, buf, len, out len) == 0)
                p.CommandLine = Marshal.PtrToStringUni(Marshal.ReadIntPtr(buf, IntPtr.Size), Marshal.ReadInt16(buf) / 2);
        } finally { Marshal.FreeHGlobal(buf); }
    }

    ACCOUNTING Accounting() {
        ACCOUNTING a;
        QueryInformationJobObject(job, 1, out a, Marshal.SizeOf(typeof(ACCOUNTING)), IntPtr.Zero);
        return a;
    }
    public bool Done { get { return Accounting().ActiveProcesses == 0; } }
    public int Missed { get { lock (Procs) return Math.Max(0, Accounting().TotalProcesses - Procs.Count); } }

    bool InDir(Proc p) { return p.Path.StartsWith(prefix, StringComparison.OrdinalIgnoreCase); }

    // what runs from the install folder for longer than the grace is ended, with all it started
    public void Sweep(double grace) {
        lock (Procs) {
            foreach (var p in Procs.ToArray()) {
                if (p.Ended || !InDir(p) || !p.Alive || (DateTime.UtcNow - p.Seen).TotalSeconds < grace) continue;
                Lines.Add(string.Format("ended {0} after {1:F0} s: {2}", p.Name, (DateTime.UtcNow - p.Seen).TotalSeconds, p.CommandLine));
                End(p);
            }
        }
    }
    void End(Proc p) {
        foreach (var c in Procs) if (c.Parent == p.Id && c.Seen >= p.Seen && !c.Ended) End(c);
        p.Ended = true;
        TerminateProcess(p.Handle, 1);
    }
    public void EndAll() { TerminateJobObject(job, 1); }

    public string[] Report() {
        var r = new List<string>();
        lock (Procs) {
            foreach (var p in Procs) {
                if (p.Id == RootId) continue;
                if (InDir(p)) r.Add("ran " + p.CommandLine);
                else if (p.Parent == RootId)
                    r.Add(string.Format("child {0} exit {1}", System.IO.Path.GetFileNameWithoutExtension(p.Name), p.Exit));
            }
        }
        r.AddRange(Lines);
        if (Missed > 0) r.Add("missed " + Missed);
        int code; GetExitCodeProcess(root, out code);
        r.Add("exit " + code);
        return r.ToArray();
    }
}
'@

$run = New-Object WatchedRun $Exe, $Arguments, $Dir
$start = Get-Date
while (-not $run.Done) {
    $run.Sweep($Grace)
    if (((Get-Date) - $start).TotalSeconds -gt $Timeout) {
        Write-Output ("timeout after {0} s" -f $Timeout)
        $run.EndAll()
        break
    }
    Start-Sleep -Milliseconds 200
}
# Nothing from the install folder may outlive the run, even outside the job (a sign-in task
# the hooks started would run the agent there): ended, and reported.
$prefix = $Dir.TrimEnd('\') + '\'
foreach ($q in Get-CimInstance Win32_Process) {
    if ($q.ExecutablePath -and $q.ExecutablePath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        Write-Output ("ended {0} (outside the run): {1}" -f $q.Name, $q.CommandLine)
        Stop-Process -Id $q.ProcessId -Force -ErrorAction SilentlyContinue
    }
}
# the job's last messages (a process it held that the listener has not read yet)
for ($i = 0; $i -lt 100 -and $run.Missed -gt 0; $i++) { Start-Sleep -Milliseconds 20 }
foreach ($l in $run.Report()) { Write-Output $l }
