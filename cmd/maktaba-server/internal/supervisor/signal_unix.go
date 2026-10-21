//go:build !windows

package supervisor

import (
	"errors"
	"os"
	"syscall"
)

// terminationSignal is the graceful-stop signal sent to children on
// shutdown. On Unix that's SIGTERM, which every service's
// signal.NotifyContext already traps to begin its drain.
func terminationSignal() os.Signal { return syscall.SIGTERM }

// askChild sends the child its graceful-stop signal (the child only: it stops
// its own children as it drains). One already gone needs no asking.
func askChild(proc *os.Process, _ *procTree, sig os.Signal) error {
	if err := proc.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

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
