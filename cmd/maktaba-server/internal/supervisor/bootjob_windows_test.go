//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// bootJobPossible reports whether a child may leave the job this test runs in (the boot
// job's way needs it: a CI runner may allow it where this machine's shell does not).
func bootJobPossible() bool {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
	return cmd.Run() == nil
}
