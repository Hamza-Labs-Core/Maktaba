//go:build windows

package main

import (
	"os"
	"os/exec"
	"testing"
)

// TestAskStopAfterTheServerExited: an exited server is not asked (its pid may
// be another process's by then, and a CTRL_BREAK naming a pid that leads no
// group goes astray), and that is no error; while it ran, its handle kept the
// pid its own.
func TestAskStopAfterTheServerExited(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$") // the test binary, running no test: it exits at once
	prepareChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := attachChild(cmd)
	defer child.release()
	if child.proc == 0 {
		t.Fatal("no handle on the server")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if child.running() {
		t.Fatal("an exited server shows as running")
	}
	if err := askStop(cmd, child); err != nil {
		t.Fatalf("asking an exited server: %v", err)
	}
	child.release()
	if child.running() {
		t.Fatal("a released handle shows as running")
	}
}
