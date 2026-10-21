//go:build windows

package ctrlbreak

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

var (
	ntdll                = windows.NewLazySystemDLL("ntdll.dll")
	procNtSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	procNtResumeProcess  = ntdll.NewProc("NtResumeProcess")
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

// Ask sends CTRL_BREAK to the process group pid leads (a child started with
// CREATE_NEW_PROCESS_GROUP), through Hold. pid is a child the caller has not
// reaped: exec.Cmd's, from its Cancel. (exec reaps, and frees the pid, before
// it reads Cancel's result, so a Cancel that races the child's own exit can
// name a pid already freed; Windows does not hand a pid out again that fast,
// and this was so before Hold.) It returns os.ErrProcessDone when the child has
// exited, which exec takes as done, and GenerateConsoleCtrlEvent's error when
// there is no console to send it through.
func Ask(pid int) error {
	h, err := windows.OpenProcess(Access, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return os.ErrProcessDone // no such process any more
		}
		return fmt.Errorf("open the process to ask it: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return Hold(h, func() error {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
	})
}

func exited(h windows.Handle) bool {
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == windows.WAIT_OBJECT_0
}
