//go:build windows

package main

import (
	"os/exec"
	"time"
)

// On Windows the streaming process holds its children in a Job Object that ends
// them with it (procattr.ContainSelf): nothing more to do here.
func ownGroup(*exec.Cmd) {}

func endGroup(int, time.Duration) bool { return true }
