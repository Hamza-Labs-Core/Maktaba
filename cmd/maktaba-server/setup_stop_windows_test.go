//go:build windows

package main

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// The bootstrap starts in a process group of its own (CTRL_BREAK names it
// alone), is asked on cancel, and is killed only after stepStopWait.
func TestStopGentlyWindows(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit")
	stopGently(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("SysProcAttr = %+v: no process group of its own", cmd.SysProcAttr)
	}
	if cmd.Cancel == nil || cmd.WaitDelay != stepStopWait {
		t.Fatalf("Cancel set %v, WaitDelay %s", cmd.Cancel != nil, cmd.WaitDelay)
	}
}
