package layout

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// busyTries bounds the retries of a program Linux says is busy.
const busyTries = 20

// CombinedOutputFresh runs the command build makes and returns its combined output,
// making and running it again while the system says the program's file is busy
// (ETXTBSY). A binary written a moment ago can be held open for writing by a process
// this one started at that moment (its fork holds the descriptor until it execs,
// golang/go#22315): only on Linux, and only for a few milliseconds.
func CombinedOutputFresh(build func() *exec.Cmd) ([]byte, error) {
	for i := 0; ; i++ {
		out, err := build().CombinedOutput()
		if err == nil || i == busyTries-1 || !errors.Is(err, syscall.ETXTBSY) {
			return out, err
		}
		time.Sleep(time.Duration(i+1) * 10 * time.Millisecond)
	}
}
