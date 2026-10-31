//go:build darwin

package pgbundle

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// processName is a running process's command name (alive false: no such process; ""
// with alive: one it cannot tell, left to Postgres). macOS has no /proc: the kernel's
// process table says.
func processName(pid int) (name string, alive bool) {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return "", false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", true
	}
	if kp.Proc.P_pid == 0 {
		return "", false
	}
	comm := kp.Proc.P_comm[:]
	for i, c := range comm {
		if c == 0 {
			comm = comm[:i]
			break
		}
	}
	return string(comm), true
}

// parentPID is a running process's parent, from the kernel's process table.
func parentPID(pid int) (int, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp.Proc.P_pid == 0 {
		return 0, false
	}
	return int(kp.Eproc.Ppid), true
}

func signalProcess(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }
