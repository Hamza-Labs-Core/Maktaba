package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGo(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFlagsConcatAndSprintf(t *testing.T) {
	d := t.TempDir()
	writeGo(t, d, "bad.go", `package p

import (
	"fmt"
	"log/slog"
)

func f(lg *slog.Logger, name string, err error) {
	lg.Info("user " + name + " logged in")
	lg.Error(fmt.Sprintf("failed for %s", name))
	lg.Warn("dropped", "err", err) // OK: constant msg, fielded data
}
`)
	vs, err := scanDir(d)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("want 2 violations, got %d: %+v", len(vs), vs)
	}
	kinds := map[string]bool{}
	for _, v := range vs {
		kinds[v.kind] = true
	}
	if !kinds["string concatenation"] || !kinds["fmt.Sprintf"] {
		t.Fatalf("unexpected violation kinds: %+v", kinds)
	}
}

func TestScanAllowsConstantAndNamedConstMsg(t *testing.T) {
	d := t.TempDir()
	writeGo(t, d, "good.go", `package p

import "log/slog"

const msgUp = "service up"

func f(lg *slog.Logger, n int) {
	lg.Info("service starting", "port", n)
	lg.Info(msgUp, "pid", n)
	lg.LogAttrs(nil, slog.LevelError, "boom")
}
`)
	vs, err := scanDir(d)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(vs) != 0 {
		t.Fatalf("want 0 violations, got %d: %+v", len(vs), vs)
	}
}

func TestScanSkipsTestFiles(t *testing.T) {
	d := t.TempDir()
	writeGo(t, d, "x_test.go", `package p

import "log/slog"

func f(lg *slog.Logger, s string) { lg.Info("dyn " + s) }
`)
	vs, err := scanDir(d)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(vs) != 0 {
		t.Fatalf("_test.go must be skipped, got %+v", vs)
	}
}

func TestScanFlagsLogAttrsMsgArg(t *testing.T) {
	d := t.TempDir()
	writeGo(t, d, "la.go", `package p

import "log/slog"

func f(lg *slog.Logger, who string) {
	lg.LogAttrs(nil, slog.LevelInfo, "hi "+who)
}
`)
	vs, err := scanDir(d)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(vs) != 1 || vs[0].kind != "string concatenation" {
		t.Fatalf("LogAttrs msg arg not checked: %+v", vs)
	}
}
