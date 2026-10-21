package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/service"
)

// fakeServiceDeps: a unit in a temp dir and a manager whose commands are recorded.
func fakeServiceDeps(t *testing.T) (*serviceDeps, *[]string) {
	t.Helper()
	dir := t.TempDir()
	launcher := filepath.Join(dir, "runtime", "current", "bin", "maktaba-launcher")
	ran := &[]string{}
	d := &serviceDeps{
		manager: service.UserManager{
			GOOS:   "linux",
			Booted: func() bool { return true },
			Run: func(_ context.Context, argv []string) (string, error) {
				*ran = append(*ran, strings.Join(argv, " "))
				if strings.HasPrefix(strings.Join(argv, " "), "systemctl --user show maktaba-server.service") {
					return "UnitFileState=enabled\nActiveState=active\nSubState=running\nMainPID=7\n", nil
				}
				return "", nil
			},
			LingerFile: filepath.Join(dir, "linger"),
		},
		unit: service.AppUnit{Launcher: launcher, LogFile: filepath.Join(dir, "logs", "server.log"),
			Dir: filepath.Join(dir, "systemd", "user")},
	}
	old := newServiceDeps
	newServiceDeps = func() serviceDeps { return *d }
	t.Cleanup(func() { newServiceDeps = old })
	return d, ran
}

func runService(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newServiceCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestServiceInstallNeedsTheRuntime(t *testing.T) {
	d, ran := fakeServiceDeps(t)
	if _, err := runService(t, "install"); err == nil || !strings.Contains(err.Error(), "runtime install") {
		t.Fatalf("install without a runtime: %v", err)
	}
	if len(*ran) != 0 {
		t.Errorf("ran %q", *ran)
	}
	if err := os.MkdirAll(filepath.Dir(d.unit.Launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.unit.Launcher, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runService(t, "install", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Type string            `json:"type"`
		Data service.AppStatus `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &ev); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if ev.Type != "result" || !ev.Data.Installed || ev.Data.Active != "active" || !ev.Data.Enabled {
		t.Errorf("result %+v", ev)
	}
	if got := strings.Join(*ran, "|"); !strings.Contains(got, "systemctl --user enable --now maktaba-server.service") {
		t.Errorf("ran %s", got)
	}
	b, err := os.ReadFile(d.unit.Path())
	if err != nil || !strings.Contains(string(b), "ExecStart="+d.unit.Launcher+" --log-file") {
		t.Errorf("unit: %v\n%s", err, b)
	}
}

func TestServiceControlNeedsTheUnit(t *testing.T) {
	_, ran := fakeServiceDeps(t)
	if _, err := runService(t, "stop"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("stop without a unit: %v", err)
	}
	for _, r := range *ran {
		if strings.Contains(r, " stop ") {
			t.Errorf("ran %q", r)
		}
	}
}

func TestServiceStatusText(t *testing.T) {
	fakeServiceDeps(t)
	out, err := runService(t, "status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "not installed" {
		t.Errorf("status %q", out)
	}
	if got := describeService(service.AppStatus{Unit: "/u", Installed: true, Active: "active", SubState: "running", Manager: "ok",
		Enabled: true}); got != "/u: active (running), enabled, runs only while you are logged in (no linger)" {
		t.Errorf("describe %q", got)
	}
}

func TestServiceCodes(t *testing.T) {
	for err, want := range map[error]string{
		&codedError{"runtime_missing", os.ErrNotExist}: "runtime_missing",
		service.ErrUnsupported:                         "unsupported",
		service.ErrServiceTaken:                        "service_taken",
		os.ErrPermission:                               "systemd",
	} {
		if got := serviceCode(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
}
