//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// stopGently makes the context's cancellation ask cmd to stop: CTRL_BREAK to
// the process group it starts in (the bootstrap and the pipeline take it as
// SIGBREAK and stop pip themselves), then the kill after stepStopWait.
// Without a console to send it through (a step run detached), the old kill.
// The ask names cmd's pid while exec still holds its handle, so it is cmd's.
func stopGently(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	cmd.Cancel = func() error {
		if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid)); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = stepStopWait
}
