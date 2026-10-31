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
	m := envMap(childEnvOK(RoleStreaming, cfg))
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
		if SlotsEnabled(c) || envMap(childEnvOK(RoleStreaming, c))["MAKTABA_TV_SLOTS"] != "" {
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
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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

// A slot bound to a TV release runs that release's binary (checked against its
// SHA-256) with MAKTABA_TV_RELEASE; a wrong checksum refuses the start.
func TestSlotReleaseBinding(t *testing.T) {
	dir := fakeStreamingBin(t)
	base := filepath.Join(dir, filepath.Base(mustBin(t, dir)))
	rel := filepath.Join(dir, "releases", "1.2.3")
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	relBin := filepath.Join(rel, filepath.Base(base))
	if err := os.WriteFile(relBin, []byte("release binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, _ := fileSHA256(relBin)
	p, err := BuildSlot("green", Options{Config: slotCfg(), BinDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	cmd := cloneCmd(p.Cmd)
	if err := applyBinding(cmd, &SlotBinding{ReleaseID: "r-1", Version: "1.2.3", SHA256: sum}, p.Cmd.Path); err != nil {
		t.Fatal(err)
	}
	if cmd.Path != relBin || cmd.Args[0] != relBin || envMap(cmd.Env)["MAKTABA_TV_RELEASE"] != "r-1" {
		t.Fatalf("bound command: %s %v", cmd.Path, cmd.Args)
	}
	cmd = cloneCmd(p.Cmd)
	if err := applyBinding(cmd, &SlotBinding{ReleaseID: "r-1", Version: "1.2.3", SHA256: strings.Repeat("0", 64)}, p.Cmd.Path); err == nil ||
		!strings.Contains(err.Error(), "tv_slot_refused") {
		t.Fatalf("a wrong checksum started: %v", err)
	}
	if err := applyBinding(cmd, &SlotBinding{ReleaseID: "r-1", Version: "../x"}, p.Cmd.Path); err == nil {
		t.Fatal("a bad version resolved")
	}
	cmd.Path = relBin
	if err := applyBinding(cmd, nil, p.Cmd.Path); err != nil || cmd.Path != p.Cmd.Path {
		t.Fatalf("unbound: %s %v", cmd.Path, err)
	}
	s := New([]Process{p}, testPolicy())
	if s.Bind("green", &SlotBinding{ReleaseID: "r"}) != nil || s.Bind("purple", nil) == nil {
		t.Fatal("Bind")
	}
}

// A TV held on the release before (story 35.6, the owner's decision of 2026-10-28): a
// slot bound to it restarts on its binary where the install keeps it (a desktop app's
// kept payload), checked against its SHA-256, and that release counts as in use while the
// slot is bound, running or not, so no commit prunes it.
func TestSlotBindingFindsTheKeptBinary(t *testing.T) {
	dir := fakeStreamingBin(t)
	base := filepath.Join(dir, filepath.Base(mustBin(t, dir)))
	kept := filepath.Join(t.TempDir(), "payloads", "1.4.0", "bin", filepath.Base(base))
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("streaming 1.4.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, _ := fileSHA256(kept)
	finder := func(v string) []string {
		if bareVersion(v) == "1.4.0" {
			return []string{filepath.Join(t.TempDir(), "missing"), kept}
		}
		return nil
	}
	p, err := BuildSlot("blue", Options{Config: slotCfg(), BinDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	b := &SlotBinding{ReleaseID: "r-held", Version: "v1.4.0", SHA256: sum}
	cmd := cloneCmd(p.Cmd)
	if err := applyBinding(cmd, b, p.Cmd.Path); err == nil {
		t.Fatal("without the kept binaries the release resolved")
	}
	cmd = cloneCmd(p.Cmd)
	if err := applyBindingKept(cmd, b, p.Cmd.Path, finder); err != nil || cmd.Path != kept ||
		envMap(cmd.Env)["MAKTABA_TV_RELEASE"] != "r-held" {
		t.Fatalf("kept binary: %s %v", cmd.Path, err)
	}
	cmd = cloneCmd(p.Cmd)
	wrong := &SlotBinding{ReleaseID: "r-held", Version: "1.4.0", SHA256: strings.Repeat("0", 64)}
	if err := applyBindingKept(cmd, wrong, p.Cmd.Path, finder); err == nil {
		t.Fatal("a kept binary of another checksum started")
	}

	s := New([]Process{p}, testPolicy(), WithKeptStreaming(finder))
	if s.keptStreaming == nil {
		t.Fatal("WithKeptStreaming")
	}
	if err := s.Bind("blue", b); err != nil {
		t.Fatal(err)
	}
	// bound, not running (between two starts): its release is still in use
	if got := s.ReleasesInUse(); len(got) != 1 || got[0] != "1.4.0" {
		t.Fatalf("in use %v", got)
	}
}

func mustBin(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) == 0 {
		t.Fatal("no fake binary")
	}
	return ents[0].Name()
}

// A running slot asked to start on another release is started again on it (else it
// keeps answering for its old release); the same binding again leaves it running.
func TestSlotStartOnAnotherReleaseRestartsIt(t *testing.T) {
	green, _ := fakeProc(t, RoleTVSlot, "serve")
	green.Instance = "green"
	s := New([]Process{green}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	start := func(release string) {
		t.Helper()
		resp, err := http.Post(srv.URL+"/slots/green/start", "application/json",
			strings.NewReader(`{"release_id":"`+release+`"}`))
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("start green on %s: %v %v", release, resp, err)
		}
		resp.Body.Close()
	}
	pid := func(sn Snapshot) int {
		for _, p := range sn.Processes {
			if p.Instance == "green" && p.State == StateRunning {
				return p.PID
			}
		}
		return 0
	}
	start("r-old")
	first := pid(waitFor(t, s, "green running", func(sn Snapshot) bool { return pid(sn) != 0 }))
	start("r-old")
	time.Sleep(200 * time.Millisecond)
	if got := pid(s.Snapshot()); got != first {
		t.Fatalf("the same binding restarted green: pid %d, was %d", got, first)
	}
	start("r-new")
	waitFor(t, s, "green on the new release", func(sn Snapshot) bool { p := pid(sn); return p != 0 && p != first })
}

// Any bind, not only the front door's start, starts a running slot again on its new
// release; a slot that is not wanted is bound for its next start and left stopped.
func TestBindMatchesTheRunningRelease(t *testing.T) {
	blue, _ := fakeProc(t, RoleTVSlot, "serve")
	blue.Instance = "blue"
	amber, _ := fakeProc(t, RoleTVSlot, "serve")
	amber.Instance, amber.OnDemand = "amber", true
	s := New([]Process{blue, amber}, testPolicy(), WithLog(&syncBuffer{}))
	running(t, s)
	pid := func(sn Snapshot, name string) int {
		for _, p := range sn.Processes {
			if p.Instance == name && p.State == StateRunning {
				return p.PID
			}
		}
		return 0
	}
	first := pid(waitFor(t, s, "blue running", func(sn Snapshot) bool { return pid(sn, "blue") != 0 }), "blue")
	if err := s.Bind("blue", &SlotBinding{ReleaseID: "r-new"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "blue on r-new", func(sn Snapshot) bool { p := pid(sn, "blue"); return p != 0 && p != first })
	if err := s.Bind("amber", &SlotBinding{ReleaseID: "r-new"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if p := pid(s.Snapshot(), "amber"); p != 0 {
		t.Fatalf("a bind started amber, which nobody wants: pid %d", p)
	}
}
