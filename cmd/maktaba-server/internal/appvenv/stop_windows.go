//go:build windows

package appvenv

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// ownGroup starts the bootstrap in a process group of its own
// (CREATE_NEW_PROCESS_GROUP), on the server's console: a CTRL_BREAK can then be
// sent to it alone, and the server's own CTRL_BREAK (the launcher asking the
// server to stop) does not reach it before the server asks.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// askStop sends the bootstrap's process group CTRL_BREAK (SIGBREAK to Python):
// it stops pip, says interrupted and exits 130. Without a console to send it
// through (a server started detached, which the app's launcher never does) it
// is killed instead, and the next run resumes the build.
func askStop(cmd *exec.Cmd) error {
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid)); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
