package layout

import (
	"os/exec"
	"syscall"
	"testing"
)

// A program the system says is busy is tried again, with a new command each time; any
// other failure, and success, are answered at once.
func TestCombinedOutputFreshRetriesABusyProgram(t *testing.T) {
	n := 0
	busy := func() *exec.Cmd {
		n++
		if n < 3 {
			// a command whose start fails as a busy program's does
			return &exec.Cmd{Path: "/", Err: &exec.Error{Name: "busy", Err: syscall.ETXTBSY}}
		}
		return exec.Command("go", "version")
	}
	if out, err := CombinedOutputFresh(busy); err != nil || n != 3 || len(out) == 0 {
		t.Fatalf("busy twice, then ran: n=%d err=%v", n, err)
	}

	n = 0
	missing := func() *exec.Cmd { n++; return exec.Command("/no/such/program") }
	if _, err := CombinedOutputFresh(missing); err == nil || n != 1 {
		t.Fatalf("another failure: n=%d err=%v", n, err)
	}
}
