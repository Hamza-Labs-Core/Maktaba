//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A step that dies without a result says why: the end of its stderr.
func TestStepWithoutAResultSaysItsStderr(t *testing.T) {
	server := filepath.Join(t.TempDir(), "maktaba-server")
	script := "#!/bin/sh\necho 'starting' >&2\necho 'panic: runtime error: index out of range' >&2\nexit 2\n"
	if err := os.WriteFile(server, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := execStepRunner(server)(context.Background(), "venv", map[string]any{}, nil)
	var f *stepFailure
	if !errors.As(err, &f) || f.Code != "no_result" || !strings.Contains(f.Message, "panic: runtime error: index out of range") {
		t.Fatalf("%#v", err)
	}
	if w := (&stderrTail{max: 4}); func() bool { _, _ = w.Write([]byte("abcdef")); return string(w.b) != "cdef" }() {
		t.Error("the tail keeps more than its last bytes")
	}
}

// The wait's Ctrl+C skips it, once: the wait gives the signal back at once.
func TestInterruptSkipsTheWait(t *testing.T) {
	skipped, stop := interruptSkips()
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-skipped:
	case <-time.After(5 * time.Second):
		t.Fatal("the Ctrl+C did not skip the wait")
	}
}
