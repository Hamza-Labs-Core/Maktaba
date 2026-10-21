package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLogFileAppendsAndRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "server.log")
	l, err := openLogFile(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"first line\n", "second\n", "third line!\n"} {
		if _, err := l.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" { // the user's only: the log names the library's files
		if fi, _ := os.Stat(filepath.Dir(path)); fi == nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("logs dir mode %v", fi)
		}
		if fi, _ := os.Stat(path); fi == nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("log mode %v", fi)
		}
	}
	if b, _ := os.ReadFile(path + ".1"); string(b) != "first line\nsecond\n" {
		t.Errorf("rotated copy %q", b)
	}
	if b, _ := os.ReadFile(path); string(b) != "third line!\n" {
		t.Errorf("current %q", b)
	}

	// over the limit at start: moved aside before anything is written
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 30), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err = openLogFile(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = l.Write([]byte("new\n"))
	_ = l.Close()
	if b, _ := os.ReadFile(path); string(b) != "new\n" {
		t.Errorf("after a start over the limit: %q", b)
	}
	if b, _ := os.ReadFile(path + ".1"); len(b) != 30 {
		t.Errorf("the large log was not kept as .1 (%d bytes)", len(b))
	}
}

func TestServeLogFileFlag(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "server.log")
	var stdout, stderr bytes.Buffer
	// a serve that cannot start (no layout here) still writes its error to the log
	code := run([]string{"--root", dir, "--log-file", logPath, "serve"}, &stdout, &stderr)
	if code == exitOK {
		t.Fatal("serve of an empty root succeeded")
	}
	b, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(b), "not a self-managed install") {
		t.Errorf("log %q (%v); stderr %q", b, err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("with --log-file the output goes to the log, not stderr: %q", stderr.String())
	}
}

// diskSink is a log file on a disk that can be full.
type diskSink struct {
	mu   *sync.Mutex
	buf  *bytes.Buffer
	full *bool
}

func (d diskSink) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if *d.full {
		return 0, syscall.ENOSPC
	}
	return d.buf.Write(p)
}
func (diskSink) Close() error                 { return nil }
func (d diskSink) Stat() (os.FileInfo, error) { return sizeInfo(int64(d.buf.Len())), nil }

type sizeInfo int64

func (s sizeInfo) Size() int64      { return int64(s) }
func (sizeInfo) Name() string       { return "server.log" }
func (sizeInfo) Mode() fs.FileMode  { return 0o600 }
func (sizeInfo) ModTime() time.Time { return time.Time{} }
func (sizeInfo) IsDir() bool        { return false }
func (sizeInfo) Sys() any           { return nil }

func withSink(t *testing.T, open func(string) (logSink, error)) {
	t.Helper()
	prev := openLogSink
	openLogSink = open
	t.Cleanup(func() { openLogSink = prev })
}

// A full disk drops output (it never fails or blocks the writer), the file is
// tried again after logRetry, and a line then says how much was lost.
func TestLogFileDropsWhileTheDiskIsFull(t *testing.T) {
	var mu sync.Mutex
	buf, full := &bytes.Buffer{}, false
	withSink(t, func(string) (logSink, error) { return diskSink{&mu, buf, &full}, nil })
	l, err := openLogFile(filepath.Join(t.TempDir(), "server.log"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	write := func(s string) {
		t.Helper()
		if n, err := l.Write([]byte(s)); err != nil || n != len(s) {
			t.Fatalf("write %q: %d %v", s, n, err)
		}
	}
	write("before\n")
	full = true
	write("lost one\n")
	full = false
	now = now.Add(logRetry / 2)
	write("lost two\n") // within the wait: dropped without trying
	now = now.Add(logRetry)
	write("after\n")
	got := buf.String()
	want := "before\n[maktaba-launcher] 18 bytes of output were lost: the log could not be written (" +
		syscall.ENOSPC.Error() + ")\nafter\n"
	if got != want {
		t.Errorf("log %q, want %q", got, want)
	}
}

// A reopen that fails at rotation does not stop the log either.
func TestLogFileReopensAfterAFailedRotation(t *testing.T) {
	var mu sync.Mutex
	buf, full := &bytes.Buffer{}, false
	fail := false
	withSink(t, func(string) (logSink, error) {
		if fail {
			return nil, errors.New("no inodes left")
		}
		return diskSink{&mu, buf, &full}, nil
	})
	l, err := openLogFile(filepath.Join(t.TempDir(), "server.log"), 10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	_, _ = l.Write([]byte("0123456789"))
	fail = true
	if n, err := l.Write([]byte("rotate")); err != nil || n != 6 {
		t.Fatalf("a failed rotation: %d %v", n, err)
	}
	fail = false
	now = now.Add(logRetry)
	_, _ = l.Write([]byte("back\n"))
	if !strings.Contains(buf.String(), "6 bytes of output were lost: the log could not be written (no inodes left)") ||
		!strings.HasSuffix(buf.String(), "back\n") {
		t.Errorf("log %q", buf.String())
	}
}

// The pipe the server writes to is drained whatever the disk does: with every
// write refused, far more than a pipe holds still goes through.
func TestLogPipeNeverBlocksOnAFullDisk(t *testing.T) {
	var mu sync.Mutex
	buf, full := &bytes.Buffer{}, true
	withSink(t, func(string) (logSink, error) { return diskSink{&mu, buf, &full}, nil })
	w, done, err := logPipe(filepath.Join(t.TempDir(), "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	wrote := make(chan error, 1)
	go func() {
		chunk := bytes.Repeat([]byte("x"), 4096)
		for i := 0; i < 256; i++ { // 1 MiB: sixteen times what a pipe holds
			if _, err := w.Write(chunk); err != nil {
				wrote <- err
				return
			}
		}
		wrote <- nil
	}()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("writing to the log blocked while the disk was full")
	}
}

// A logs directory and a log another program made first (setup's steps) end
// up the user's only.
func TestLogFileTightensExistingModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "server.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := openLogFile(path, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if fi, _ := os.Stat(dir); fi == nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("logs dir mode %v", fi)
	}
	if fi, _ := os.Stat(path); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v", fi)
	}
}
