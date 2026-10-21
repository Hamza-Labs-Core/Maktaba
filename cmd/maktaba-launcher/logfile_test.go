package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
