//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ctrlbreak"
	"golang.org/x/sys/windows"
)

// stopGently makes the context's cancellation ask cmd to stop: CTRL_BREAK to
// the process group it starts in (the bootstrap and the pipeline take it as
// SIGBREAK and stop pip themselves), then the kill after stepStopWait.
// Without a console to send it through (a step run detached), the old kill.
// cmd is held from the check that it still runs to the send (ctrlbreak.Ask):
// one that has just exited is not asked, since the event would then reach
// setup itself, which has stopped listening for it after the first stop.
func stopGently(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	cmd.Cancel = func() error {
		err := ctrlbreak.Ask(cmd.Process.Pid)
		if err == nil || errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = stepStopWait
}
