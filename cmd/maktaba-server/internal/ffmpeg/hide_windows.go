//go:build windows

package ffmpeg

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow: a probe of a server started without a console (the app, a
// detached serve) opens no console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
