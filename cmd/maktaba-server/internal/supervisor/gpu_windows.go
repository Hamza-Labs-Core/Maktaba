//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// nvidiaSMIDirs: the driver puts nvidia-smi.exe in System32 (and older drivers
// under Program Files\NVIDIA Corporation\NVSMI).
func nvidiaSMIDirs() []string {
	var out []string
	if root := os.Getenv("SystemRoot"); root != "" {
		out = append(out, filepath.Join(root, "System32", "nvidia-smi.exe"))
	}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		out = append(out, filepath.Join(pf, "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"))
	}
	return out
}

// hideWindow: the server runs without a console window (story 38.3); a console
// program it starts must not open one.
func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
