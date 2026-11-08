package supervisor

import (
	"net/http"
	"net/http/httptest"
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

// A held runner group (none of its components on) is refused by the start endpoint, and the
// ask leaves it held: an on-demand start never bypasses a hold.
func TestTheStartEndpointRefusesAHeldRunner(t *testing.T) {
	p, _ := fakeProc(t, RolePluginRunner, "serve")
	p.Instance, p.OnDemand, p.IdleOnClean = "service", true, true
	p.Component = PluginRunnersComponent
	s := New([]Process{p}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	if err := s.Allow(RolePluginRunner, "service", false); err != nil {
		t.Fatal(err)
	}
	service := func() ChildStatus {
		for _, c := range s.Snapshot().Processes {
			if c.Instance == "service" {
				return c
			}
		}
		return ChildStatus{}
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/runners/service/start", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a held group's start: %d, want 409", resp.StatusCode)
	}
	time.Sleep(300 * time.Millisecond)
	c := service()
	if c.PID != 0 || c.Wanted || c.State != StateHeld || c.StartMode != StartOnDemand {
		t.Fatalf("the ask touched a held runner: %+v", c)
	}
	if _, open, _ := s.Gate(RolePluginRunner, "service"); open {
		t.Fatal("the ask opened the gate")
	}
}

// Core's footprint is measured after 120 s idle (tools/release/bundle-size/core-home.sh): a
// runner the home's last job woke must have exited by then, with room for that job to end.
func TestRunnerIdleExitFitsCoresIdleWindow(t *testing.T) {
	const coreIdleWindow = 120
	if RunnerIdleSec <= 0 || RunnerIdleSec > coreIdleWindow/2 {
		t.Fatalf("RunnerIdleSec = %d: a runner must exit within half of Core's %d s idle window", RunnerIdleSec, coreIdleWindow)
	}
}
