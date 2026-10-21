//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The real thing: /dev/full refuses every write with ENOSPC, as a full disk
// does. The server's output still goes through the pipe, never blocking it.
func TestLogPipeOnDevFull(t *testing.T) {
	withSink(t, func(string) (logSink, error) { return os.OpenFile("/dev/full", os.O_WRONLY, 0) })
	w, done, err := logPipe(filepath.Join(t.TempDir(), "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	wrote := make(chan error, 1)
	go func() {
		_, err := w.Write(bytes.Repeat([]byte("server output\n"), 80_000)) // > 1 MB, far over a pipe's 64 KiB
		wrote <- err
	}()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server's output blocked on a full disk")
	}
}
