//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// childHandle: on POSIX the service manager reaps the tree (systemd
// KillMode=mixed, launchd); nothing extra to hold.
type childHandle struct{}

func prepareChild(*exec.Cmd)             {}
func attachChild(*exec.Cmd) *childHandle { return &childHandle{} }
func (*childHandle) kill()               {}
func (*childHandle) release()            {}
func askStop(cmd *exec.Cmd) error        { return cmd.Process.Signal(syscall.SIGTERM) }
func setupConsole(_, _ bool)             {}
