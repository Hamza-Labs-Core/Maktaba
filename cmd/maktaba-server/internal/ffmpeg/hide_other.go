//go:build !windows

package ffmpeg

import "os/exec"

// hideWindow: only Windows opens a window for a console program.
func hideWindow(*exec.Cmd) {}
