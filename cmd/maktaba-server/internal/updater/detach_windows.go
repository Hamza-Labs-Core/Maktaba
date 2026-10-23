//go:build windows

package updater

import (
	"os/exec"
	"syscall"
)

// createBreakawayFromJob is CREATE_BREAKAWAY_FROM_JOB: the child leaves the launcher's
// kill-on-close job (which allows breakaway), so closing the job at the server's exit
// does not kill the app it opened.
const createBreakawayFromJob = 0x01000000

// detach starts cmd outside the launcher's job, in a process group of its own.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createBreakawayFromJob | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}
