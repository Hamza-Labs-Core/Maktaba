//go:build windows

package ctrlbreak

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	ntdll                     = windows.NewLazySystemDLL("ntdll.dll")
	procNtSuspendProcess      = ntdll.NewProc("NtSuspendProcess")
	procNtResumeProcess       = ntdll.NewProc("NtResumeProcess")
)

// Access is the access a handle given to Hold needs.
const Access = windows.SYNCHRONIZE | windows.PROCESS_SUSPEND_RESUME

// Hold runs send while the process h names still runs, holding it suspended
// from that check to the send so it cannot exit in between, and resumes it
// after: its handler runs then. A process that has exited is not asked
// (os.ErrProcessDone). One that cannot be held is not asked either (the error
// says so); the caller then kills it. h needs Access, and while the caller
// holds it the pid is not another process's.
func Hold(h windows.Handle, send func() error) error {
	if exited(h) {
		return os.ErrProcessDone
	}
	if r, _, _ := procNtSuspendProcess.Call(uintptr(h)); r != 0 {
		if exited(h) {
			return os.ErrProcessDone
		}
		return fmt.Errorf("hold the process to ask it: NTSTATUS 0x%x", r)
	}
	defer func() { _, _, _ = procNtResumeProcess.Call(uintptr(h)) }()
	if exited(h) {
		return os.ErrProcessDone
	}
	return send()
}

func exited(h windows.Handle) bool {
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == windows.WAIT_OBJECT_0
}

// CanAsk reports whether this process has a console to send CTRL_BREAK
// through (a child asked without one is killed instead).
func CanAsk() bool {
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return n != 0
}

// Stopper makes an exec.Cmd's Cancel ask the child to stop: CTRL_BREAK to the
// process group it starts in (CREATE_NEW_PROCESS_GROUP), through Hold. The
// handle is opened by Started, right after Start, while exec still holds its
// own: exec reaps the child (and frees its pid) before it reads Cancel's
// result, so a pid opened from Cancel could be another process's by then. A
// child that cannot be asked (no console, or no handle) is killed instead,
// through exec's own handle.
type Stopper struct {
	cmd     *exec.Cmd
	mu      sync.Mutex
	h       windows.Handle
	started bool
	closed  bool
}

// NewStopper sets cmd up before Start: a process group of its own, and the
// Cancel. The caller calls Started after Start and Close after Wait.
func NewStopper(cmd *exec.Cmd) *Stopper {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	s := &Stopper{cmd: cmd}
	cmd.Cancel = s.cancel
	return s
}

// Started opens the child's handle. It runs right after a Start that
// succeeded, before Wait.
func (s *Stopper) Started() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	if h, err := windows.OpenProcess(Access, false, uint32(s.cmd.Process.Pid)); err == nil {
		s.h = h
	}
}

// Close closes the handle, after Wait (exec calls Cancel no more by then).
func (s *Stopper) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.h != 0 {
		_ = windows.CloseHandle(s.h)
		s.h = 0
	}
}

// cancel is cmd's Cancel. Before Started (a context that ended as the child
// started) the caller has not called Wait yet, so the pid is still the
// child's and is opened here. After Close the child is reaped: nothing to ask.
func (s *Stopper) cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrProcessDone
	}
	h := s.h
	if !s.started {
		o, err := windows.OpenProcess(Access, false, uint32(s.cmd.Process.Pid))
		if err != nil {
			return s.cmd.Process.Kill()
		}
		defer func() { _ = windows.CloseHandle(o) }()
		h = o
	}
	if h == 0 {
		return s.cmd.Process.Kill()
	}
	pid := uint32(s.cmd.Process.Pid)
	err := Hold(h, func() error { return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pid) })
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return s.cmd.Process.Kill()
}
