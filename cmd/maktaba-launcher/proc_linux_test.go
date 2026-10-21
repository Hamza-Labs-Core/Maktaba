//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

func orphanAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// A server that crashes leaving a role behind (its own process group, holding
// the --log-file pipe) is started again at once, and what it left is stopped:
// the next server must not meet an api still holding its port (review of PR
// #173: with the log writer the launcher waited for the orphan's pipe).
func TestCrashedServerIsRestartedAndItsOrphansStopped(t *testing.T) {
	processTest(t)
	bundle, root := mkBundle(t, "orphan-exit:3,run")
	w, done, err := logPipe(filepath.Join(t.TempDir(), "logs", "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	old := orphanGrace
	orphanGrace = time.Second
	t.Cleanup(func() { orphanGrace = old })
	cfg := testConfig(root)
	cfg.Bundle = bundle
	cfg.Stdout, cfg.Stderr = w, w
	start := time.Now()
	stop := startLauncher(t, cfg)
	l := layout.New(root)
	waitFor(t, "the server started again", func() bool { return countAct(readTrace(l), "bin", "run") == 1 })
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the restart took %s: the launcher waited for the orphan", took)
	}
	b, err := os.ReadFile(filepath.Join(root, "orphan.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("the orphan %d still runs after the restart", pid)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

func TestProcStat(t *testing.T) {
	ppid, pgid, ok := procStat(os.Getpid())
	if !ok || ppid != os.Getppid() || pgid != syscall.Getpgrp() {
		t.Errorf("procStat(self) = %d %d %v", ppid, pgid, ok)
	}
	if _, _, ok := procStat(1 << 30); ok {
		t.Error("a process that does not exist")
	}
}
