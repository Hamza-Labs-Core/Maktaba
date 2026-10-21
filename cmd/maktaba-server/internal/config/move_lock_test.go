package config

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// moveChild is the test binary as another maktaba-server start (TestMain runs it
// with MAKTABA_TEST_MOVE_CHILD set):
//
//	hold - take the move lock in MAKTABA_TEST_MOVE_ROOT, write <root>/held, and keep
//	       the lock until <root>/release appears
//	move - write <base>/ready-<pid>; once <base>/go appears, copy the Windows install
//	       under MAKTABA_TEST_MOVE_BASE (winLayoutAt) into its new place, every
//	       rename between its folders failing as between volumes, and print the
//	       lines
func moveChild(mode string) int {
	switch mode {
	case "hold":
		root := os.Getenv("MAKTABA_TEST_MOVE_ROOT")
		release, err := lockMove(root, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		defer release()
		if err := os.WriteFile(filepath.Join(root, "held"), nil, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
		for deadline := time.Now().Add(20 * time.Second); !exists(filepath.Join(root, "release")); {
			if time.Now().After(deadline) {
				return 4
			}
			time.Sleep(20 * time.Millisecond)
		}
		return 0
	case "move":
		base := os.Getenv("MAKTABA_TEST_MOVE_BASE")
		if err := os.WriteFile(filepath.Join(base, fmt.Sprintf("ready-%d", os.Getpid())), nil, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
		for deadline := time.Now().Add(20 * time.Second); !exists(filepath.Join(base, "go")); { // both start together
			if time.Now().After(deadline) {
				return 4
			}
			time.Sleep(5 * time.Millisecond)
		}
		old, cur, env := winLayoutAt(base)
		lines, err := migrateWindows(volumes(old.Config, cur.Config), old, cur,
			func(k string) string { return env[k] }, 20*time.Second)
		for _, l := range lines {
			fmt.Println(l)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	return 9
}

// startChild runs the test binary as moveChild.
func startChild(t *testing.T, mode string, env ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts other processes; runs without -short")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(append(os.Environ(), "MAKTABA_TEST_MOVE_CHILD="+mode), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd, &stdout, &stderr
}

// The move lock between two processes: while one start holds it another waits, a
// bounded time, and goes on as soon as it is released.
func TestMoveLockAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	holder, _, stderr := startChild(t, "hold", "MAKTABA_TEST_MOVE_ROOT="+root)
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(root, "release"), nil, 0o600)
		_ = holder.Wait()
	})
	for deadline := time.Now().Add(10 * time.Second); !exists(filepath.Join(root, "held")); {
		if time.Now().After(deadline) {
			t.Fatalf("the other process never took the lock: %s", stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	if _, err := lockMove(root, 300*time.Millisecond); err == nil || !strings.Contains(err.Error(), "another maktaba-server") {
		t.Fatalf("held by another process: %v", err)
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Fatal("a second start waits before it gives up")
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(root, "release"), nil, 0o600)
	}()
	release, err := lockMove(root, 10*time.Second)
	if err != nil {
		t.Fatalf("released while waiting: %v", err)
	}
	release()
	if err := holder.Wait(); err != nil {
		t.Fatalf("holder: %v: %s", err, stderr)
	}
}

// Two starts at once (a serve and a setup step after an update) copy an install
// once between them: every piece is copied by one of them, whole, the originals
// stay, and neither fails.
func TestTwoStartsCopyOnce(t *testing.T) {
	base := t.TempDir()
	old, cur, _ := winLayoutAt(base)
	originals := map[string]string{
		"server.toml":        "[server]\n" + strings.Repeat("# a long comment\n", 64<<10), // a copy that takes a while
		"secrets.key":        "S",
		"stream-signing.key": "K",
		"node.json":          "{}",
	}
	plant(t, old.Config, originals)

	type run struct {
		cmd            *exec.Cmd
		stdout, stderr *bytes.Buffer
	}
	var runs []run
	for range 2 {
		cmd, stdout, stderr := startChild(t, "move", "MAKTABA_TEST_MOVE_BASE="+base)
		runs = append(runs, run{cmd, stdout, stderr})
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if ready, _ := filepath.Glob(filepath.Join(base, "ready-*")); len(ready) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the two starts never got ready")
		}
	}
	writeAt(t, filepath.Join(base, "go"), "") // both at once
	var lines []string
	for _, r := range runs {
		if err := r.cmd.Wait(); err != nil {
			t.Fatalf("a start failed: %v: %s", err, r.stderr)
		}
		for _, l := range strings.Split(r.stdout.String(), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
	}
	copied := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "Copied ") {
			copied++
		}
	}
	if copied != 3 || len(lines) != 3 {
		t.Fatalf("want each of the 3 pieces copied once:\n%s", strings.Join(lines, "\n"))
	}
	holds(t, cur.Keys, map[string]string{"secrets.key": "S", "stream-signing.key": "K"})
	if readAt(t, filepath.Join(cur.Config, "server.toml")) != originals["server.toml"] || exists(filepath.Join(cur.Config, "node.json")) {
		t.Fatal("server.toml must be whole in the new place, and node.json never copied")
	}
	holds(t, old.Config, originals)
}
