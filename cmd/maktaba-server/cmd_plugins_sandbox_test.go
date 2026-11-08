package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// #710: the supervisor's own command for the plugin host on Windows is registered, hidden
// from a person's help, and hands every argument to the launcher unparsed (the host's
// flags are not maktaba-server's).
func TestThePluginsSandboxCommand(t *testing.T) {
	root := newRootCmd()
	cmd, rest, err := root.Find([]string{"plugins-sandbox-run", "-read", "x", "--", "maktaba-plugind", "--pool", "1"})
	if err != nil || cmd.Name() != "plugins-sandbox-run" {
		t.Fatalf("not found: %v %v", cmd, err)
	}
	if !cmd.Hidden || !cmd.DisableFlagParsing {
		t.Fatalf("hidden %v, flags unparsed %v", cmd.Hidden, cmd.DisableFlagParsing)
	}
	if !slices.Equal(rest, []string{"-read", "x", "--", "maktaba-plugind", "--pool", "1"}) {
		t.Fatalf("arguments %v", rest)
	}
}

// Story 54.23: the supervisor's own command for a first-party runner on Windows is
// registered, hidden, and hands every argument to the launcher unparsed.
func TestTheRunnerSandboxCommand(t *testing.T) {
	root := newRootCmd()
	cmd, rest, err := root.Find([]string{"runner-sandbox-run", "-group", "light", "--", "python", "-m", "maktaba_plugin_runner"})
	if err != nil || cmd.Name() != "runner-sandbox-run" {
		t.Fatalf("not found: %v %v", cmd, err)
	}
	if !cmd.Hidden || !cmd.DisableFlagParsing {
		t.Fatalf("hidden %v, flags unparsed %v", cmd.Hidden, cmd.DisableFlagParsing)
	}
	if !slices.Equal(rest, []string{"-group", "light", "--", "python", "-m", "maktaba_plugin_runner"}) {
		t.Fatalf("arguments %v", rest)
	}
}

// #710: the install's check of a package runs the host binary itself on macOS and Linux,
// and the AppContainer launcher around it on Windows (the host refuses to run outside it).
func TestThePreloadCommandFollowsTheOS(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "maktaba-plugind")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_PLUGIND_BIN", bin)
	t.Setenv("MAKTABA_PLUGINS_SANDBOX_RUN", filepath.Join(dir, "launcher"))
	cfg := config.Config{}
	cfg.Server.DataDir = dir
	po := pluginsOpts(cfg, supervisor.Options{Config: cfg}, &supervisor.PluginsHealth{})
	if po == nil {
		t.Fatal("no plugin options")
	}
	args := po.preload(filepath.Join(dir, "pkg"), "dev.example.x", "1.0.0")
	if runtime.GOOS == "windows" {
		if po.plugind != filepath.Join(dir, "launcher") || args[0] != "plugins-sandbox-run" || !slices.Contains(args, bin) {
			t.Fatalf("Windows: %s %v", po.plugind, args)
		}
		return
	}
	if po.plugind != bin || slices.Contains(args, "plugins-sandbox-run") || !slices.Contains(args, "--preload") {
		t.Fatalf("%s: %s %v", runtime.GOOS, po.plugind, args)
	}
}
