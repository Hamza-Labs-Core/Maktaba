package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideConsole: a console program the server starts in the background gets no window.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
