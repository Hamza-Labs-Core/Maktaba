//go:build !windows

package hostinfo

import "os/exec"

// hideWindow: only Windows opens a window for a console program.
func hideWindow(*exec.Cmd) {}
