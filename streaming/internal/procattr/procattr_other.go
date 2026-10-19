//go:build !linux && !windows

package procattr

import "os/exec"

// Tie is a no-op here (no parent-death signal); the supervisor's process
// group still covers restarts and stops.
func Tie(*exec.Cmd) {}

// ContainSelf is a no-op here.
func ContainSelf() error { return nil }

// Contained is false here: only the supervisor's process group covers
// the children.
func Contained() bool { return false }
