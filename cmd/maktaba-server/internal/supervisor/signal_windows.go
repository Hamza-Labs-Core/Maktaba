//go:build windows

package supervisor

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// terminationSignal: Windows has no signal to choose; it names what a Go
// role sees of askChild's CTRL_BREAK.
func terminationSignal() os.Signal { return os.Interrupt }

// askChild sends CTRL_BREAK to the child's process group (prepareTree makes
// every child one, group id = its pid), the one console event a group can be
// sent on its own (CTRL_C is disabled in a new group). Go roles see it as
// os.Interrupt and the pipeline as SIGBREAK; both drain and exit. The child's
// descendants in its group (ffmpeg, yt-dlp) get it too; the supervisor and
// the other roles, each in a group of its own, do not. The signal is POSIX's:
// Windows has this one ask.
//
// The event must only ever name a running group leader: one naming a pid that
// leads no group goes astray (it can reach the supervisor itself), so it is
// sent while the tree's handle keeps the child's pid its own, and not at all
// once the child has exited. It travels through the console the supervisor
// shares with its children, so without one it fails with errNoConsole.
func askChild(proc *os.Process, tree *procTree, _ os.Signal) error {
	return tree.ask(func() error {
		err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(proc.Pid))
		if err == nil {
			return nil
		}
		if _, cerr := windows.GetConsoleCP(); cerr != nil {
			return fmt.Errorf("%w: %v", errNoConsole, err)
		}
		return fmt.Errorf("CTRL_BREAK: %w", err)
	})
}

// signalName: Windows processes end with an exit code only (a kill by
// TerminateProcess shows as code 1), so there is no signal to name.
func signalName(*os.ProcessState) string { return "" }
