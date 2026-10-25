//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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
	child := attachChild(cmd, nil)
	defer child.release(nil)
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
	child.release(nil)
	if child.running() {
		t.Fatal("a released handle shows as running")
	}
}

// envExitWhen: TestHelperExitsWhenTheFileIs, run as a child, writes <file>.ready
// and exits once <file> exists.
const envExitWhen = "LAUNCHER_TEST_EXIT_WHEN"

func TestHelperExitsWhenTheFileIs(t *testing.T) {
	file := os.Getenv(envExitWhen)
	if file == "" {
		t.Skip("the helper TestAskStopHoldsTheServerWhileAsking starts")
	}
	if err := os.WriteFile(file+".ready", nil, 0o644); err != nil {
		os.Exit(2)
	}
	for {
		if _, err := os.Stat(file); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAskStopHoldsTheServerWhileAsking: from the check to the send the server
// is held, so it cannot exit in between (the CTRL_BREAK would then reach the
// launcher too), and it runs on once asked.
func TestAskStopHoldsTheServerWhileAsking(t *testing.T) {
	processTest(t)
	exitFile := filepath.Join(t.TempDir(), "exit")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperExitsWhenTheFileIs$")
	cmd.Env = append(os.Environ(), envExitWhen+"="+exitFile)
	prepareChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child := attachChild(cmd, nil)
	defer child.release(nil)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		if child.running() {
			child.kill()
			<-done
		}
	}()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(exitFile + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper did not start")
		}
	}
	sent, early := 0, false
	err := child.ask(func() error {
		sent++
		// told to exit while held: it cannot until it is resumed
		if err := os.WriteFile(exitFile, nil, 0o644); err != nil {
			return err
		}
		select {
		case <-done:
			early = true
		case <-time.After(300 * time.Millisecond):
		}
		return nil
	})
	if err != nil || sent != 1 {
		t.Fatalf("a running server: ask = %v, sent %d", err, sent)
	}
	if early {
		t.Fatal("the server exited while held")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the server, resumed: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the server was not resumed after it was asked")
	}
}
