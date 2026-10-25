//go:build !windows

package main

import "os/exec"

// hideConsole: nothing to hide off Windows.
func hideConsole(*exec.Cmd) {}
