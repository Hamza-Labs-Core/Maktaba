//go:build windows

package hostinfo

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow: a console program started from the app gets no window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
