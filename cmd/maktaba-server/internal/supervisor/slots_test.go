package supervisor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// fakeStreamingBin puts an executable named maktaba-streaming in a temp dir.
func fakeStreamingBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "maktaba-streaming"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func slotCfg() config.Config {
	var cfg config.Config
	cfg.Database.URL = "postgres://maktaba@127.0.0.1:5432/maktaba"
	cfg.Server.StreamAddr = ":8081"
	return cfg
}

// TC1 (story 33.1): the tv-slot role's instances, their addresses, probes and
// arguments; the third instance is on demand; the front door learns the slots.
func TestSupervisorStartsSlots(t *testing.T) {
	cfg := slotCfg()
	procs, err := BuildSlots(Options{Config: cfg, BinDir: fakeStreamingBin(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 3 {
		t.Fatalf("%d slot processes, want blue, green, amber", len(procs))
	}
	for i, want := range []struct {
		name, addr, admin string
		onDemand          bool
	}{
		{"blue", "127.0.0.1:8791", "127.0.0.1:9111", false},
		{"green", "127.0.0.1:8792", "127.0.0.1:9112", false},
		{"amber", "127.0.0.1:8793", "127.0.0.1:9113", true},
	} {
		p := procs[i]
		env := envMap(p.Cmd.Env)
		if p.Role != RoleTVSlot || p.Instance != want.name || p.OnDemand != want.onDemand ||
			p.Probe != "http://"+want.admin+"/healthz" || p.Name() != "tv-slot/"+want.name {
			t.Errorf("%s: %+v", want.name, p)
		}
		if got := strings.Join(p.Cmd.Args[1:], " "); got != "slot --slot "+want.name {
			t.Errorf("%s args %q", want.name, got)
		}
		if env["MAKTABA_TV_SLOT"] != want.name || env["MAKTABA_HTTP_ADDR"] != want.addr ||
			env["MAKTABA_ADMIN_ADDR"] != want.admin || env["DATABASE_URL"] != cfg.Database.URL {
			t.Errorf("%s env: %v", want.name, env)
		}
	}
	// the streaming role is the front door: where the slots are, how to ask for them
	m := envMap(ChildEnv(RoleStreaming, cfg))
	if m["MAKTABA_TV_SLOTS"] != "blue=127.0.0.1:8791,green=127.0.0.1:8792,amber=127.0.0.1:8793" ||
		m["MAKTABA_SUPERVISOR_URL"] != "http://127.0.0.1:9103" {
		t.Fatalf("front door env: %v", m)
	}
	// [tv] slots = off, a SQLite home or a joined node: no slots, no front door wiring
	off := cfg
	off.TV.Slots = "off"
	sqlite := cfg
	sqlite.Database.URL = "sqlite:///var/lib/maktaba/db.sqlite"
	for name, c := range map[string]config.Config{"off": off, "sqlite": sqlite} {
		if SlotsEnabled(c) || envMap(ChildEnv(RoleStreaming, c))["MAKTABA_TV_SLOTS"] != "" {
			t.Errorf("%s: slots enabled", name)
		}
	}
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	nodeConfig = func() config.NodeConfig {
		return config.NodeConfig{NodeID: "n1", DatabaseURL: "postgresql://node@home/maktaba"}
	}
	if SlotsEnabled(cfg) {
		t.Error("a joined node runs slots")
	}
	if _, err := BuildSlot("purple", Options{Config: cfg, BinDir: fakeStreamingBin(t)}); err == nil {
		t.Error("unknown instance accepted")
	}
}

// The on-demand instance waits until it is wanted; a stopped instance is not
// restarted until it is wanted again; the others are not disturbed; the exit
// hook hears every exit.
func TestSupervisorSlotsOnDemand(t *testing.T) {
	blue, _ := fakeProc(t, RoleTVSlot, "serve")
	blue.Instance = "blue"
	amber, _ := fakeProc(t, RoleTVSlot, "serve")
	amber.Instance, amber.OnDemand = "amber", true
	var mu sync.Mutex
	var exits []string
	s := New([]Process{blue, amber}, testPolicy(), WithLog(&syncBuffer{}), WithExitHook(func(p Process, info ExitInfo) {
		mu.Lock()
		exits = append(exits, p.Name()+":"+info.Reason)
		mu.Unlock()
	}))
	running(t, s)
	inst := func(snap Snapshot, name string) ChildStatus {
		for _, p := range snap.Processes {
			if p.Instance == name {
				return p
			}
		}
		return ChildStatus{}
	}
	waitFor(t, s, "blue running", func(sn Snapshot) bool { return inst(sn, "blue").State == StateRunning })
	time.Sleep(300 * time.Millisecond)
	if a := inst(s.Snapshot(), "amber"); a.PID != 0 || a.Wanted || a.State != StateStopped {
		t.Fatalf("amber started before it was asked for: %+v", a)
	}
	// the front door asks for it through the admin surface
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/slots/amber/start", "application/json", nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("start amber: %v %v", resp, err)
	}
	resp.Body.Close()
	snap := waitFor(t, s, "amber running", func(sn Snapshot) bool { return inst(sn, "amber").State == StateRunning })
	amberPID, bluePID := inst(snap, "amber").PID, inst(snap, "blue").PID
	if !inst(snap, "amber").Wanted {
		t.Error("amber not marked wanted")
	}
	// stop it: it exits and stays down; blue is untouched
	resp, err = http.Post(srv.URL+"/slots/amber/stop", "application/json", nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("stop amber: %v %v", resp, err)
	}
	resp.Body.Close()
	waitFor(t, s, "amber stopped", func(sn Snapshot) bool {
		a := inst(sn, "amber")
		return a.PID == 0 && a.LastExit != nil && a.LastExit.Reason == ReasonStopped
	})
	time.Sleep(400 * time.Millisecond) // several backoff periods: no restart
	snap = s.Snapshot()
	if a := inst(snap, "amber"); a.PID != 0 || a.Restarts != 0 {
		t.Fatalf("a stopped slot was restarted: %+v", a)
	}
	if b := inst(snap, "blue"); b.PID != bluePID || b.Restarts != 0 {
		t.Fatalf("blue was disturbed: %+v", b)
	}
	// wanted again: a new process
	if err := s.Want(RoleTVSlot, "amber", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "amber again", func(sn Snapshot) bool {
		a := inst(sn, "amber")
		return a.State == StateRunning && a.PID != 0 && a.PID != amberPID
	})
	if err := s.Want(RoleTVSlot, "purple", true); err == nil {
		t.Error("an unknown instance was accepted")
	}
	mu.Lock()
	got := strings.Join(exits, ",")
	mu.Unlock()
	if !strings.Contains(got, "tv-slot/amber:stopped") {
		t.Errorf("exit hook heard %q", got)
	}
	resp, _ = http.Post(srv.URL+"/slots/amber/explode", "application/json", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad action answered %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// The supervisor reports a slot's exit to the front door at once.
func TestSlotExitReporter(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Method + " " + r.URL.Path
	}))
	defer srv.Close()
	report := SlotExitReporter(strings.TrimPrefix(srv.URL, "http://127.0.0.1"))
	report(Process{Role: RoleAPI}, ExitInfo{}) // not a slot: nothing
	report(Process{Role: RoleTVSlot, Instance: "blue"}, ExitInfo{Reason: ReasonCrash})
	select {
	case g := <-got:
		if g != "POST /internal/tv/slot-exited/blue" {
			t.Fatalf("reported %q", g)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no report")
	}
}
