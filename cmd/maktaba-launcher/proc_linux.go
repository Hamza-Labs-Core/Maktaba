//go:build linux

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// On Linux the launcher is a child subreaper (Run: becomeSubreaper). The
// supervisor puts every role in a process group of its own, so when the server
// ends without stopping them (a crash, an OOM kill) they live on, and they are
// reparented to the launcher instead of to systemd. The launcher stops them
// before it starts the server again, which would otherwise meet an api still
// holding its port. Only children in another process group than the
// launcher's are ever touched: the server itself runs in the launcher's.

// orphanGrace is how long what the server left behind may take to stop after
// SIGTERM before it is killed.
var orphanGrace = 10 * time.Second

type childHandle struct{}

func becomeSubreaper() { _ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }

func prepareChild(*exec.Cmd)             {}
func attachChild(*exec.Cmd) *childHandle { return &childHandle{} }
func askStop(cmd *exec.Cmd) error        { return cmd.Process.Signal(syscall.SIGTERM) }
func setupConsole(_, _ bool)             {}

// release runs once the server has been reaped: what it left behind is
// stopped (SIGTERM, then SIGKILL after orphanGrace) and reaped.
func (*childHandle) release() { stopOrphans(orphanGrace) }

// kill is the stop grace running out: the server is killed by the caller,
// and everything it left behind with it.
func (*childHandle) kill() { stopOrphans(0) }

// reap collects what ended while the server runs (a role's child that
// outlived the role): no zombie stays behind under the launcher.
func (*childHandle) reap() {
	for _, o := range orphans() {
		var ws syscall.WaitStatus
		_, _ = syscall.Wait4(o.pid, &ws, syscall.WNOHANG, nil)
	}
}

type orphan struct{ pid, pgid int }

// orphans are the launcher's children outside its process group.
func orphans() []orphan {
	self, own := os.Getpid(), syscall.Getpgrp()
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []orphan
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		ppid, pgid, ok := procStat(pid)
		if ok && ppid == self && pgid != own {
			out = append(out, orphan{pid, pgid})
		}
	}
	return out
}

// procStat reads a process's parent and process group from /proc/<pid>/stat
// ("pid (comm) state ppid pgrp …"; comm may hold spaces and parentheses).
func procStat(pid int) (ppid, pgid int, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 3 {
		return 0, 0, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	pgid, err2 := strconv.Atoi(f[2])
	return ppid, pgid, err1 == nil && err2 == nil
}

// signalOrphan signals an orphan's whole process group when it leads one.
func signalOrphan(o orphan, sig syscall.Signal) {
	if o.pgid == o.pid {
		_ = syscall.Kill(-o.pgid, sig)
		return
	}
	_ = syscall.Kill(o.pid, sig)
}

func stopOrphans(grace time.Duration) {
	left := orphans()
	if len(left) == 0 {
		return
	}
	sig := syscall.SIGTERM
	if grace <= 0 {
		sig = syscall.SIGKILL
	}
	for _, o := range left {
		signalOrphan(o, sig)
	}
	deadline := time.Now().Add(grace)
	for {
		for _, o := range left {
			var ws syscall.WaitStatus
			_, _ = syscall.Wait4(o.pid, &ws, syscall.WNOHANG, nil)
		}
		if left = orphans(); len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			for _, o := range left {
				signalOrphan(o, syscall.SIGKILL)
			}
			for _, o := range left {
				var ws syscall.WaitStatus
				_, _ = syscall.Wait4(o.pid, &ws, 0, nil)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
