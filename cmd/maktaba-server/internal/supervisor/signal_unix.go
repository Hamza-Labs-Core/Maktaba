//go:build !windows

package supervisor

import (
	"os"
	"syscall"
)

// terminationSignal is the graceful-stop signal sent to children on
// shutdown. On Unix that's SIGTERM, which every service's
// signal.NotifyContext already traps to begin its drain.
func terminationSignal() os.Signal { return syscall.SIGTERM }

// signalName names the signal that killed a process ("SIGSEGV"), or ""
// when it exited on its own.
func signalName(ps *os.ProcessState) string {
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	sig := ws.Signal()
	if name := signalNames[sig]; name != "" {
		return name
	}
	return sig.String()
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGABRT: "SIGABRT",
	syscall.SIGBUS:  "SIGBUS",
	syscall.SIGFPE:  "SIGFPE",
	syscall.SIGHUP:  "SIGHUP",
	syscall.SIGILL:  "SIGILL",
	syscall.SIGINT:  "SIGINT",
	syscall.SIGKILL: "SIGKILL",
	syscall.SIGPIPE: "SIGPIPE",
	syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGTERM: "SIGTERM",
}
