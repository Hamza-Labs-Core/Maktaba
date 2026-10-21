package main

import (
	"os/exec"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ctrlbreak"
)

// stepStopWait is how long a cancelled step's program may take to stop after
// it is asked (stopGently) before it is killed: the venv bootstrap gives pip
// 10 s, then writes its status (interrupted, where it stopped); a download
// closes its partial file. 45 s leaves room on a slow, busy disk.
const stepStopWait = 45 * time.Second

// stopGently makes the context's cancellation ask cmd to stop (ctrlbreak:
// SIGTERM, or CTRL_BREAK to a process group of its own on Windows, through a
// handle opened as it starts, so the event never reaches setup itself, which
// no longer listens after the first stop), then the kill after stepStopWait.
// The bootstrap and the pipeline stop pip themselves. The caller calls the
// Stopper's Started after Start and Close after Wait.
func stopGently(cmd *exec.Cmd) *ctrlbreak.Stopper {
	stop := ctrlbreak.NewStopper(cmd)
	cmd.WaitDelay = stepStopWait
	return stop
}
