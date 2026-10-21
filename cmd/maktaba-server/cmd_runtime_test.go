//go:build !windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
)

// onePayload is a payload of one file, as payload.sh writes it.
func onePayload(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\n"
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "maktaba-launcher"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	manifest := fmt.Sprintf("f\t0755\t%d\t%s\tbin/maktaba-launcher\n", len(body), hex.EncodeToString(sum[:]))
	if err := os.WriteFile(filepath.Join(dir, appruntime.Manifest), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	msum := sha256.Sum256([]byte(manifest))
	pj, _ := json.Marshal(map[string]any{"schema": 1, "version": version, "platform": appruntime.Platform(),
		"files": map[string]any{"manifest": appruntime.Manifest, "count": 1, "bytes": len(body), "sha256": hex.EncodeToString(msum[:])}})
	if err := os.WriteFile(filepath.Join(dir, appruntime.PayloadJSON), pj, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type recordingService struct{ log []string }

func (s *recordingService) Active(context.Context) bool { return true }
func (s *recordingService) Stop(context.Context) error  { s.log = append(s.log, "stop"); return nil }
func (s *recordingService) Start(context.Context) error { s.log = append(s.log, "start"); return nil }

func runRuntime(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRuntimeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestRuntimeInstallActivateStatus(t *testing.T) {
	svc := &recordingService{}
	rt := appruntime.New(filepath.Join(t.TempDir(), "runtime"))
	rt.Sync = func() {}
	old := newRuntimeDeps
	newRuntimeDeps = func() runtimeDeps { return runtimeDeps{rt: rt, service: svc} }
	t.Cleanup(func() { newRuntimeDeps = old })

	out, err := runRuntime(t, "install", "--json", "--from", onePayload(t, "v0.3.0"), "--activate", "--owner", "app")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var progress struct {
		Type  string `json:"type"`
		Stage string `json:"stage"`
		Done  int64  `json:"done"`
		Total int64  `json:"total"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &progress); err != nil || progress.Type != "progress" ||
		progress.Stage != "copy" || progress.Done != progress.Total {
		t.Errorf("progress %q", lines[0])
	}
	var res struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil || res.Type != "result" ||
		res.Data["activated"] != true || res.Data["version"] != "v0.3.0" {
		t.Errorf("result %q", lines[len(lines)-1])
	}
	if strings.Join(svc.log, ",") != "stop,start" {
		t.Errorf("service %v", svc.log)
	}

	out, err = runRuntime(t, "status")
	if err != nil || !strings.Contains(out, "(v0.3.0), owner app, 1 installed") {
		t.Errorf("status %q %v", out, err)
	}
}

func TestRuntimeErrorCodes(t *testing.T) {
	for err, want := range map[error]string{
		fmt.Errorf("x: %w", appruntime.ErrInvalid):  "payload_invalid",
		fmt.Errorf("x: %w", appruntime.ErrDiskFull): "disk_full",
		fmt.Errorf("x: %w", appruntime.ErrLocked):   "locked",
		&codedError{"service", os.ErrClosed}:        "service",
		os.ErrPermission:                            "runtime",
	} {
		if got := runtimeCode(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
}
