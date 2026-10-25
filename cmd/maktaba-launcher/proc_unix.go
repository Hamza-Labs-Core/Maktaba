//go:build !windows && !linux

package main

import (
	"os/exec"
	"syscall"
)

// childHandle: on POSIX the service manager reaps the tree (systemd
// KillMode=mixed, launchd); nothing extra to hold.
type childHandle struct{}

// runJob is Windows' job; POSIX ties nothing to it.
type runJob struct{}

func newRunJob() *runJob { return nil }
func (*runJob) close()   {}

func prepareChild(*exec.Cmd)                      {}
func attachChild(*exec.Cmd, *runJob) *childHandle { return &childHandle{} }
func (*childHandle) kill()                        {}
func (*childHandle) release(map[int]bool)         {}
func askStop(cmd *exec.Cmd, _ *childHandle) error { return cmd.Process.Signal(syscall.SIGTERM) }
func setupConsole(_, _ bool)                      {}

func becomeSubreaper()     {}
func (*childHandle) reap() {}
