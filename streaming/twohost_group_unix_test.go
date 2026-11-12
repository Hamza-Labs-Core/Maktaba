//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// ownGroup starts cmd in a process group of its own, so the test can end and wait
// for everything the process started (its ffmpeg children), not the process alone.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// groupGone reports whether no process is left in the group pgid leads.
func groupGone(pgid int) bool {
	return errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

// endGroup kills every process in the group pgid leads and waits until none is
// left (at most wait). Killing a streaming process does not end its ffmpeg
// children at the same instant: on Linux the kernel signals them as the parent
// exits (Pdeathsig) and they die a moment later, elsewhere they live on. One of
// them writing a segment while the test's temp dir is removed failed the removal
// with "directory not empty". It reports whether the group emptied in time.
func endGroup(pgid int, wait time.Duration) bool {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	deadline := time.Now().Add(wait)
	for !groupGone(pgid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}
