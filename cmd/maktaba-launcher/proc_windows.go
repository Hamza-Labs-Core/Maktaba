//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// runJob is the kill-on-close Job Object the launcher runs every server in, held
// for the launcher's whole life: when the launcher dies (the logon task is ended,
// the service is killed) the kernel closes the handle and whatever runs in it
// goes too, so a restarted launcher never finds an orphaned server on its ports.
// Between servers the launcher ends what a server left in it, but for the TV
// processes a server handed over to the next one (story 35.7), which stay in it.
// BREAKAWAY_OK lets the supervisor's own per-child jobs, and the TV processes of
// a server under a launcher from before the handover, leave it.
type runJob struct {
	job windows.Handle // 0: none could be made
}

func newRunJob() *runJob {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &runJob{}
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return &runJob{}
	}
	return &runJob{job: job}
}

// close closes the job when the launcher ends: kill-on-close ends what is left.
func (j *runJob) close() {
	if j != nil && j.job != 0 {
		_ = windows.CloseHandle(j.job)
		j.job = 0
	}
}

// end ends every process in the job.
func (j *runJob) end() {
	if j != nil && j.job != 0 {
		_ = windows.TerminateJobObject(j.job, 1)
	}
}

// childHandle ties a server to the launcher's job.
type childHandle struct {
	mu  sync.Mutex
	run *runJob
	// proc keeps the server's pid its own until it is reaped: a CTRL_BREAK
	// naming a pid that leads no process group goes astray (it can reach the
	// launcher itself), so askStop asks only while this shows it running, and
	// holds the server suspended from that check to the send (ask).
	proc windows.Handle
}

// prepareChild: a new process group (CTRL_BREAK reaches only the server), and
// created suspended so it is in the job before it can start anything.
func prepareChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED
}

var (
	ntdll            = windows.NewLazySystemDLL("ntdll.dll")
	procResume       = ntdll.NewProc("NtResumeProcess")
	procSuspend      = ntdll.NewProc("NtSuspendProcess")
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procAllocConsole = kernel32.NewProc("AllocConsole")
	procGetConsoleWn = kernel32.NewProc("GetConsoleWindow")
	user32           = windows.NewLazySystemDLL("user32.dll")
	procShowWindow   = user32.NewProc("ShowWindow")
)

func attachChild(cmd *exec.Cmd, rj *runJob) *childHandle {
	h := &childHandle{run: rj}
	if p, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_SUSPEND_RESUME,
		false, uint32(cmd.Process.Pid)); err == nil {
		h.proc = p
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		return h // cannot resume it either; Wait reports what happens
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	if rj != nil && rj.job != 0 {
		_ = windows.AssignProcessToJobObject(rj.job, ph)
	}
	if r, _, _ := procResume.Call(uintptr(ph)); r != 0 {
		_ = cmd.Process.Kill()
	}
	return h
}

func (h *childHandle) kill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.run.end()
}

// release runs once the server has been reaped: what it left in the launcher's job
// is ended, unless it handed the TV processes over (keep not nil, story 35.7): they
// run on in the job for the next server to adopt.
func (h *childHandle) release(keep map[int]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if keep == nil {
		h.run.end()
	}
	if h.proc != 0 {
		_ = windows.CloseHandle(h.proc)
		h.proc = 0
	}
}

// running: the server has not exited (its handle is not signalled). Without
// a handle it cannot tell, and says so.
func (h *childHandle) running() bool {
	if h.proc == 0 {
		return false
	}
	ev, err := windows.WaitForSingleObject(h.proc, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

// ask runs send while the server still runs, holding it suspended from the
// check to the send so it cannot exit in between: the event names the group
// the server leads, and once it has exited nobody leads that group and the
// event goes to every process of the console, the launcher included (which
// would then stop, not roll back a release that timed out). The server's
// handler runs once it is resumed. An exited server needs no asking; one that
// cannot be held is not asked (the stop file and stop's grace remain).
func (h *childHandle) ask(send func() error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running() {
		return nil
	}
	if r, _, _ := procSuspend.Call(uintptr(h.proc)); r != 0 {
		if !h.running() {
			return nil
		}
		return fmt.Errorf("hold the server to ask it: NTSTATUS 0x%x", r)
	}
	defer func() { _, _, _ = procResume.Call(uintptr(h.proc)) }()
	if !h.running() {
		return nil
	}
	return send()
}

// askStop sends CTRL_BREAK to the server's process group (Go maps it to
// os.Interrupt, which `serve` drains on), while the server still runs (ask).
// Without a shared console this fails; the stop file the launcher wrote is
// the fallback.
func askStop(cmd *exec.Cmd, child *childHandle) error {
	pid := uint32(cmd.Process.Pid)
	return child.ask(func() error { return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pid) })
}

// setupConsole gives a service a console (hidden in session 0) so CTRL_BREAK
// can reach the server, and hides the window of a logon-task launcher.
func setupConsole(hide, service bool) {
	if service {
		_, _, _ = procAllocConsole.Call()
		hide = true
	}
	if hide {
		if w, _, _ := procGetConsoleWn.Call(); w != 0 {
			_, _, _ = procShowWindow.Call(w, 0) // SW_HIDE
		}
	}
}

// becomeSubreaper and reap are Linux's (proc_linux.go); the job object ties
// the tree to the server here.
func becomeSubreaper()     {}
func (*childHandle) reap() {}
