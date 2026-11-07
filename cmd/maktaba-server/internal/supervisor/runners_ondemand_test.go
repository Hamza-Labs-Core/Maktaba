package supervisor

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// An idle home runs no first-party runner (the Core budget: four processes, Epic 54): each
// shared group's runner is on demand, stops itself when idle (exit 0) and has its idle
// timeout; ml and index, their own components' processes, are neither.
func TestAnIdleHomeRunsNoSharedRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no runner on Windows")
	}
	cfg := config.Config{}
	cfg.Database.URL = "postgres://h/db"
	cfg.Server.DataDir = t.TempDir()
	ps, err := BuildPluginRunners(Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	shared := 0
	for _, p := range ps {
		idle := ""
		for _, kv := range p.Cmd.Env {
			if v, ok := strings.CutPrefix(kv, "MAKTABA_RUNNER_IDLE_SEC="); ok {
				idle = v
			}
		}
		if containsString(RunnerGroups, p.Instance) {
			shared++
			if !p.OnDemand || !p.IdleOnClean || idle != strconv.Itoa(RunnerIdleSec) {
				t.Fatalf("%s: on demand %v, idle on clean %v, idle timeout %q", p.Instance, p.OnDemand,
					p.IdleOnClean, idle)
			}
			continue
		}
		if p.OnDemand || p.IdleOnClean || idle != "" {
			t.Fatalf("%s (its component's own) starts on demand or idles: %+v", p.Instance, p)
		}
	}
	if shared == 0 {
		t.Fatal("no shared runner group was built")
	}
}

// A shared runner is not started at boot; the worker's ask starts it; its idle exit leaves
// it stopped (not restarted, not wanted) until the next ask.
func TestARunnerStartsWhenAskedAndStaysDownWhenIdle(t *testing.T) {
	p, addr := fakeProc(t, RolePluginRunner, "serve")
	p.Instance, p.OnDemand, p.IdleOnClean = "light", true, true
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	light := func(sn Snapshot) ChildStatus {
		for _, c := range sn.Processes {
			if c.Instance == "light" {
				return c
			}
		}
		return ChildStatus{}
	}
	time.Sleep(300 * time.Millisecond)
	if c := light(s.Snapshot()); c.PID != 0 || c.Wanted || c.State != StateStopped {
		t.Fatalf("an idle home started the runner: %+v", c)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	ask := func() {
		t.Helper()
		resp, err := http.Post(srv.URL+"/runners/light/start", "application/json", nil)
		if err != nil || resp.StatusCode != http.StatusAccepted {
			t.Fatalf("ask: %v %v", resp, err)
		}
		resp.Body.Close()
	}
	ask()
	first := waitFor(t, s, "runner up", func(sn Snapshot) bool { return light(sn).State == StateRunning })
	pid := light(first).PID
	// idle: it exits 0 by itself
	if resp, err := http.Get("http://" + addr + "/exit"); err == nil {
		resp.Body.Close()
	}
	waitFor(t, s, "runner stopped", func(sn Snapshot) bool {
		c := light(sn)
		return c.PID == 0 && c.LastExit != nil && c.LastExit.Reason == ReasonClean
	})
	time.Sleep(400 * time.Millisecond) // several backoff periods: no restart
	if c := light(s.Snapshot()); c.PID != 0 || c.Wanted || c.State != StateStopped {
		t.Fatalf("an idle runner was started again: %+v", c)
	}
	ask()
	waitFor(t, s, "runner again", func(sn Snapshot) bool {
		c := light(sn)
		return c.State == StateRunning && c.PID != 0 && c.PID != pid
	})
	if resp, err := http.Post(srv.URL+"/runners/nope/start", "application/json", nil); err != nil ||
		resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown group: %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
}
