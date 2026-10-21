//go:build windows

package main

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// childHandle holds the kill-on-close Job Object the server runs in: when
// the launcher dies (the logon task is ended, the service is killed) the
// kernel closes the handle and the server goes with it, so a restarted
// launcher never finds an orphaned server on its ports. BREAKAWAY_OK lets the
// supervisor's own per-child jobs and the 35.7 handover leave it.
type childHandle struct {
	mu  sync.Mutex
	job windows.Handle
	// proc keeps the server's pid its own until it is reaped: a CTRL_BREAK
	// naming a pid that leads no process group goes astray (it can reach the
	// launcher itself), so askStop asks only while this shows it running.
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
	kernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procAllocConsole = kernel32.NewProc("AllocConsole")
	procGetConsoleWn = kernel32.NewProc("GetConsoleWindow")
	user32           = windows.NewLazySystemDLL("user32.dll")
	procShowWindow   = user32.NewProc("ShowWindow")
)

func attachChild(cmd *exec.Cmd) *childHandle {
	h := &childHandle{}
	if p, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false, uint32(cmd.Process.Pid)); err == nil {
		h.proc = p
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		return h // cannot resume it either; Wait reports what happens
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	if job, err := windows.CreateJobObject(nil, nil); err == nil {
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err == nil &&
			windows.AssignProcessToJobObject(job, ph) == nil {
			h.job = job
		} else {
			_ = windows.CloseHandle(job)
		}
	}
	if r, _, _ := procResume.Call(uintptr(ph)); r != 0 {
		_ = cmd.Process.Kill()
	}
	return h
}

func (h *childHandle) kill() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.job != 0 {
		_ = windows.TerminateJobObject(h.job, 1)
	}
}

func (h *childHandle) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.job != 0 {
		_ = windows.CloseHandle(h.job)
		h.job = 0
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

// askStop sends CTRL_BREAK to the server's process group (Go maps it to
// os.Interrupt, which `serve` drains on), while the server still runs (an
// exited one needs no asking). Without a shared console this fails; the stop
// file the launcher wrote is the fallback.
func askStop(cmd *exec.Cmd, child *childHandle) error {
	child.mu.Lock()
	defer child.mu.Unlock()
	if !child.running() {
		return nil
	}
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
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
