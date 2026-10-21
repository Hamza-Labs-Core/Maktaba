package main

import (
	"os/exec"
	"testing"
)

// A step's program is asked on cancel (ctrlbreak's Stopper, whose own tests
// send the real CTRL_BREAK and SIGTERM; TestSetupVenvStopsGently a whole step)
// and is killed only after stepStopWait.
func TestStopGently(t *testing.T) {
	cmd := exec.Command("go", "version")
	if stop := stopGently(cmd); stop == nil {
		t.Fatal("no Stopper")
	}
	if cmd.Cancel == nil || cmd.WaitDelay != stepStopWait {
		t.Fatalf("Cancel set %v, WaitDelay %s", cmd.Cancel != nil, cmd.WaitDelay)
	}
}
