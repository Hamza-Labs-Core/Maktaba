//go:build windows

package appvenv

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ctrlbreak"
	"golang.org/x/sys/windows"
)

// ownGroup starts the bootstrap in a process group of its own
// (CREATE_NEW_PROCESS_GROUP), on the server's console: a CTRL_BREAK can then be
// sent to it alone, and the server's own CTRL_BREAK (the launcher asking the
// server to stop) does not reach it before the server asks.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// askStop sends the bootstrap's process group CTRL_BREAK (SIGBREAK to Python):
// it stops pip, says interrupted and exits 130. The bootstrap is held from the
// check that it still runs to the send (ctrlbreak.Ask), so a bootstrap that has
// just exited is not asked: the event would go to the server's whole console,
// every role and the server too. Without a console to send it through (a
// server started detached, which the app's launcher never does) it is killed
// instead, and the next run resumes the build.
func askStop(cmd *exec.Cmd) error {
	err := ctrlbreak.Ask(cmd.Process.Pid)
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return cmd.Process.Kill()
}
