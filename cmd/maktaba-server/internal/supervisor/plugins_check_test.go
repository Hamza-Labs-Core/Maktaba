//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Epic 59 story 59.07: CheckPluginsSandbox runs the plugin host with a start's flags and
// --sandbox-check, and reads its answer; an answer it cannot read, or no host, is an error.
func TestCheckPluginsSandbox(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join(dir, "data")
	argsFile := filepath.Join(dir, "args")
	host := func(body string) Options {
		bin := filepath.Join(dir, "maktaba-plugind")
		script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\n" + body + "\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MAKTABA_PLUGIND_BIN", bin)
		return Options{Config: cfg}
	}
	ctx := context.Background()

	c, err := CheckPluginsSandbox(ctx, host(`echo '{"sandbox":"partial","missing":["namespaces"]}'`))
	if err != nil || c.Sandbox != "partial" || !slices.Equal(c.Missing, []string{"namespaces"}) {
		t.Fatalf("partial: %+v %v", c, err)
	}
	b, _ := os.ReadFile(argsFile)
	args := strings.Fields(string(b))
	p := PluginsPaths(cfg)
	for _, want := range []string{"--sandbox-check", "--data", "--plugins-dir", p.Root, "--run-dir", p.Run, "--dispatch-socket", p.Dispatch} {
		if !slices.Contains(args, want) {
			t.Errorf("args %v: no %s", args, want)
		}
	}
	if _, err := os.Stat(p.Run); err != nil {
		t.Errorf("the run folder is not made as a start makes it: %v", err)
	}

	c, err = CheckPluginsSandbox(ctx, host(`echo '{"sandbox":"unavailable","step":"seccomp"}'; exit 78`))
	if err != nil || c.Sandbox != "unavailable" || c.Step != "seccomp" {
		t.Fatalf("unavailable: %+v %v", c, err)
	}
	if _, err := CheckPluginsSandbox(ctx, host(`echo 'unknown flag --sandbox-check' >&2; exit 2`)); err == nil {
		t.Fatal("an older host's refusal is no answer")
	}

	t.Setenv("MAKTABA_PLUGIND_BIN", filepath.Join(dir, "none"))
	if _, err := CheckPluginsSandbox(ctx, Options{Config: cfg}); !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("no host: %v", err)
	}
}
