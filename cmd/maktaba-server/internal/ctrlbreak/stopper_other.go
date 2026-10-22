//go:build !windows

package ctrlbreak

import (
	"os/exec"
	"syscall"
)

// Stopper makes an exec.Cmd's Cancel send the child SIGTERM. The child stays
// in this process's group (a Ctrl-C in the terminal reaches it too, which it
// takes as a stop). os.Process.Signal goes through exec's own handle on the
// child, so Started and Close have nothing to hold.
type Stopper struct{}

// NewStopper sets cmd's Cancel before Start.
func NewStopper(cmd *exec.Cmd) *Stopper {
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	return &Stopper{}
}

// Started does nothing here.
func (*Stopper) Started() {}

// Close does nothing here.
func (*Stopper) Close() {}

// CanAsk is always true here: a signal needs no console.
func CanAsk() bool { return true }
