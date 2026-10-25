//go:build windows

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// tvJob is the boot's named job the processes that may outlive the supervisor run in
// (story 35.7 AC4, design §8.2): kill-on-close, with its handle duplicated into the
// launcher, so it outlives this supervisor and dies with the launcher (a service stop,
// a killed launcher). The next supervisor opens it by name. Each such process also has
// a named job of its own nested in it (not kill-on-close): what ends that process with
// its descendants (ffmpeg), opened by name after a handover.
type tvJob struct {
	name string
	job  windows.Handle
	dup  bool // the launcher holds a handle to the job
}

var (
	kernel32Job        = windows.NewLazySystemDLL("kernel32.dll")
	procOpenJobObjectW = kernel32Job.NewProc("OpenJobObjectW")
	procIsProcessInJob = kernel32Job.NewProc("IsProcessInJob")
)

// Job object access rights (winnt.h).
const (
	jobObjectQuery     = 0x0004
	jobObjectTerminate = 0x0008
)

// jobObjectBasicProcessIDList is JOBOBJECTINFOCLASS JobObjectBasicProcessIdList.
const jobObjectBasicProcessIDList = 3

// openTVJob creates (or opens, after a handover) the boot's job of the install whose
// updates directory is dir, and duplicates its handle into the launcher (launcherPID;
// 0: none). An error with a job returned: the job is usable but the launcher holds no
// handle to it, so nothing in it can outlive this supervisor (held is false).
func openTVJob(dir string, launcherPID int) (*tvJob, error) {
	name := layout.TVJobName(dir)
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	job, err := windows.CreateJobObject(nil, n)
	if err != nil {
		return nil, fmt.Errorf("create the job %s: %w", name, err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("set the job's limits: %w", err)
	}
	j := &tvJob{name: name, job: job}
	if launcherPID <= 0 {
		return j, nil // nobody holds it but this supervisor
	}
	lp, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(launcherPID))
	if err != nil {
		return j, fmt.Errorf("open the launcher (pid %d): %w", launcherPID, err)
	}
	defer func() { _ = windows.CloseHandle(lp) }()
	var out windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), job, lp, &out, 0, false,
		windows.DUPLICATE_SAME_ACCESS); err != nil {
		return j, fmt.Errorf("duplicate the job's handle into the launcher (pid %d): %w", launcherPID, err)
	}
	j.dup = true
	return j, nil
}

// held reports whether the launcher holds the job (its processes outlive this one).
func (j *tvJob) held() bool { return j != nil && j.job != 0 && j.dup }

// sweep ends every process in the boot's job that keep does not keep: what a supervisor
// that ended without a handover left there (the launcher's handle keeps the job, and so
// them, alive).
func (j *tvJob) sweep(keep func(pid int) bool) {
	if j == nil || j.job == 0 {
		return
	}
	for _, pid := range jobPIDs(j.job) {
		if pid == os.Getpid() || keep(pid) {
			continue
		}
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
			_ = windows.TerminateProcess(h, 1)
			_ = windows.CloseHandle(h)
		}
	}
}

// jobPIDs lists the processes in a job.
func jobPIDs(job windows.Handle) []int {
	const maxPIDs = 1024
	buf := make([]byte, 8+maxPIDs*unsafe.Sizeof(uintptr(0)))
	if err := windows.QueryInformationJobObject(job, jobObjectBasicProcessIDList,
		uintptr(unsafe.Pointer(&buf[0])), uint32(len(buf)), nil); err != nil {
		return nil
	}
	n := *(*uint32)(unsafe.Pointer(&buf[4]))
	out := make([]int, 0, n)
	for i := uint32(0); i < n && i < maxPIDs; i++ {
		out = append(out, int(*(*uintptr)(unsafe.Pointer(&buf[8+uintptr(i)*unsafe.Sizeof(uintptr(0))]))))
	}
	return out
}

// openJob opens a named job (0: none by that name).
func openJob(name string, access uint32) windows.Handle {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	h, _, _ := procOpenJobObjectW.Call(uintptr(access), 0, uintptr(unsafe.Pointer(n)))
	return windows.Handle(h)
}

// holdIn duplicates a job's handle into the launcher (holder): the job, and its name,
// then outlive this supervisor. 0 when it cannot.
func holdIn(holder int, job windows.Handle) windows.Handle {
	if holder <= 0 || job == 0 {
		return 0
	}
	lp, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(holder))
	if err != nil {
		return 0
	}
	defer func() { _ = windows.CloseHandle(lp) }()
	var out windows.Handle
	if windows.DuplicateHandle(windows.CurrentProcess(), job, lp, &out, 0, false, windows.DUPLICATE_SAME_ACCESS) != nil {
		return 0
	}
	return out
}

// closeIn closes a handle the launcher (holder) holds (holdIn's), once the job it keeps
// is done with.
func closeIn(holder int, h windows.Handle) {
	lp, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(holder))
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(lp) }()
	_ = windows.DuplicateHandle(lp, h, 0, nil, 0, false, windows.DUPLICATE_CLOSE_SOURCE)
}

// descendants are the processes below roots (by their parents, from a snapshot of every
// process): what an adopted TV process started (ffmpeg).
func descendants(roots []int) map[int]bool {
	out := map[int]bool{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	parent := map[int]int{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		parent[int(e.ProcessID)] = int(e.ParentProcessID)
	}
	in := map[int]bool{}
	for _, r := range roots {
		in[r] = true
	}
	for pid := range parent {
		seen := map[int]bool{}
		for p := parent[pid]; p != 0 && !seen[p]; p = parent[p] {
			seen[p] = true
			if in[p] {
				out[pid] = true
				break
			}
		}
	}
	return out
}

// rejob puts a running process and its descendants in a new named job (not
// kill-on-close): an adopted TV process whose own job is gone.
func rejob(pid int, name string) (windows.Handle, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	job, err := windows.CreateJobObject(nil, n)
	if err != nil {
		return 0, err
	}
	for _, p := range append([]int{pid}, keys(descendants([]int{pid}))...) {
		h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p))
		if err != nil {
			continue
		}
		_ = windows.AssignProcessToJobObject(job, h)
		_ = windows.CloseHandle(h)
	}
	return job, nil
}

// heldOf is the launcher's handle to a child's job (0: none).
func heldOf(t *procTree) uint64 {
	if t == nil {
		return 0
	}
	return uint64(t.held)
}

func keys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// inChildJob reports whether pid is in the named job of an adopted child (it or one of
// its descendants).
func inChildJob(jobName string, pid int) bool {
	cj := openJob(jobName, jobObjectQuery)
	if cj == 0 {
		return false
	}
	defer func() { _ = windows.CloseHandle(cj) }()
	ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	var in int32
	r, _, _ := procIsProcessInJob.Call(uintptr(ph), uintptr(cj), uintptr(unsafe.Pointer(&in)))
	return r != 0 && in != 0
}

// prepareSurvivor: the child leaves the launcher's job (kill-on-close, closed when this
// supervisor exits; BREAKAWAY_OK lets it), for the boot's job attachSurvivor puts it in.
func prepareSurvivor(cmd *exec.Cmd) {
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_BREAKAWAY_FROM_JOB
}

// errBreakaway: the job this supervisor runs in does not let a child leave it.
func errBreakaway(err error) bool { return errors.Is(err, windows.ERROR_ACCESS_DENIED) }

// survivorJob is the named job of a child that may outlive the supervisor.
func survivorJob(dir string, pid int) string { return layout.TVChildJobName(dir, pid) }

// attachSurvivor puts a started (suspended) child that may outlive the supervisor in
// the boot's job (j; nil: it stays in the jobs it is in, the launcher's, which a
// launcher that speaks the handover keeps across it) and in a named job of its own
// nested there (not kill-on-close: what ends it with its descendants), whose handle the
// launcher (holder) also holds, and resumes it. detached reports it is there; otherwise
// it gets the kill-on-close job of any child (it then ends with this supervisor) and err
// says why.
func attachSurvivor(cmd *exec.Cmd, j *tvJob, childJob string, holder int) (tree *procTree, detached bool, err error) {
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME|
		windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, false, fmt.Errorf("open child process: %w", err)
	}
	tree = &procTree{proc: ph}
	var why error
	if j != nil && j.job == 0 {
		why = errors.New("no job to outlive the supervisor in")
	} else if err := assignTo(j, ph); err != nil {
		why = err
	} else if n, err := windows.UTF16PtrFromString(childJob); err != nil {
		why = err
	} else if cj, err := windows.CreateJobObject(nil, n); err != nil {
		why = fmt.Errorf("create %s: %w", childJob, err)
	} else if err := windows.AssignProcessToJobObject(cj, ph); err != nil {
		_ = windows.CloseHandle(cj)
		why = fmt.Errorf("assign to %s: %w", childJob, err)
	} else {
		tree.job, tree.terminate, detached = cj, true, true
		tree.held, tree.holder = holdIn(holder, cj), holder
	}
	if !detached {
		if job, jerr := newKillOnCloseJob(); jerr == nil {
			if windows.AssignProcessToJobObject(job, ph) == nil {
				tree.job = job
			} else {
				_ = windows.CloseHandle(job)
			}
		}
	}
	if r, _, _ := procNtResumeProcess.Call(uintptr(ph)); r != 0 {
		tree.kill()
		_ = cmd.Process.Kill()
		tree.release()
		return nil, false, errors.Join(why, fmt.Errorf("resume child: NTSTATUS 0x%x", r))
	}
	return tree, detached, why
}

// assignTo puts a process in the boot's job (j nil: nothing to do).
func assignTo(j *tvJob, ph windows.Handle) error {
	if j == nil {
		return nil
	}
	if err := windows.AssignProcessToJobObject(j.job, ph); err != nil {
		return fmt.Errorf("assign to %s: %w", j.name, err)
	}
	return nil
}

// adoptTree opens an adopted child (its process and its named job): what asks it to
// stop, ends it with its descendants, and waits for it. The process must still be the
// one that started at c.Start. A job the launcher (holder) no longer keeps is made anew
// around the process and its descendants, and handed to the launcher.
func adoptTree(c layout.HandoverChild, dir string, holder int) (*procTree, *os.Process, error) {
	ph, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_SUSPEND_RESUME|windows.PROCESS_TERMINATE|
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(c.PID))
	if err != nil {
		return nil, nil, fmt.Errorf("open pid %d: %w", c.PID, err)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(ph, &created, &exited, &kernel, &user); err != nil ||
		uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != c.Start {
		_ = windows.CloseHandle(ph)
		return nil, nil, fmt.Errorf("pid %d is another process now", c.PID)
	}
	tree := &procTree{proc: ph, terminate: true, holder: holder}
	if c.Job != "" {
		tree.job = openJob(c.Job, jobObjectTerminate|jobObjectQuery)
	}
	switch {
	case tree.job != 0 && c.JobHeld != 0 && c.JobHolder == holder:
		tree.held = windows.Handle(c.JobHeld) // the launcher keeps it already
	case tree.job != 0:
		tree.held = holdIn(holder, tree.job)
	default:
		if job, err := rejob(c.PID, layout.TVChildJobName(dir, c.PID)); err == nil {
			tree.job, tree.held = job, holdIn(holder, job)
		}
	}
	p, err := os.FindProcess(c.PID)
	if err != nil {
		tree.release()
		return nil, nil, err
	}
	return tree, p, nil
}

// watchGone returns once the adopted process is gone (its handle signalled).
func watchGone(pid int, start uint64, tree *procTree) {
	if tree != nil && tree.proc != 0 {
		_, _ = windows.WaitForSingleObject(tree.proc, windows.INFINITE)
		return
	}
	for layout.ProcessIs(pid, start) {
		time.Sleep(adoptPoll)
	}
}
