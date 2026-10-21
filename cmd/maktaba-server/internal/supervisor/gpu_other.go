//go:build !windows

package supervisor

import "os/exec"

// nvidiaSMIDirs: the Linux driver installs nvidia-smi on PATH (/usr/bin); this is
// where it is when a service's PATH is short.
func nvidiaSMIDirs() []string { return []string{"/usr/bin/nvidia-smi", "/usr/local/bin/nvidia-smi"} }

// hideWindow: only Windows opens console windows.
func hideWindow(*exec.Cmd) {}
