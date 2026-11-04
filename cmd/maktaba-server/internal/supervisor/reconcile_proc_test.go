package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.04 with real (fake) child processes: they run without -short.

// T01, T02: Allow(false) asks a running child to stop, kills it after the catalog's grace
// (not Want's 10 s), and never starts it again until the gate opens.
func TestAllowStopsWithCatalogGraceAndNoRestart(t *testing.T) {
	p, _ := fakeProc(t, RolePipeline, "serve", "FAKE_ON_STOP=hold")
	p.Instance = "stt-1"
	cat := testCatalog(t, procComp("t.stt", "pipeline/stt-*", "", "drain = { grace_s = 1 }"))
	old := drainExtra
	drainExtra = 300 * time.Millisecond
	t.Cleanup(func() { drainExtra = old })
	s := New([]Process{p}, testPolicy(), WithLog(io.Discard), WithCatalog(cat))
	running(t, s)
	waitFor(t, s, "running", func(sn Snapshot) bool { return proc(sn, RolePipeline).State == StateRunning })
	if c := proc(s.Snapshot(), RolePipeline).Component; c != "t.stt" {
		t.Fatalf("component %q", c)
	}
	t0 := time.Now()
	if err := s.Allow(RolePipeline, "stt-1", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "killed", func(sn Snapshot) bool { return proc(sn, RolePipeline).PID == 0 })
	took := time.Since(t0)
	if took < 1200*time.Millisecond || took > 8*time.Second {
		t.Fatalf("killed after %s, want the catalog's 1.3 s", took)
	}
	st := proc(s.Snapshot(), RolePipeline)
	if st.LastExit == nil || st.LastExit.Reason != ReasonStopped || !st.Blocked {
		t.Fatalf("exit %+v blocked %v", st.LastExit, st.Blocked)
	}
	time.Sleep(1500 * time.Millisecond) // 30 backoffs of the test policy
	st = proc(s.Snapshot(), RolePipeline)
	if st.PID != 0 || st.Restarts != 0 || st.State != StateHeld {
		t.Fatalf("restarted after Allow(false): %+v", st)
	}
	if err := s.Allow(RolePipeline, "stt-1", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "running again", func(sn Snapshot) bool { return proc(sn, RolePipeline).State == StateRunning })
}

// A dormant process built before its program was on this machine (ollama before the
// runtime.ollama pack) reads Present and Path again: missing while the program is not
// there, and once it is, the gate opened starts that program.
func TestDormantProgramFoundAfterTheBoot(t *testing.T) {
	p, _ := fakeProc(t, RoleOllama, "serve")
	bin, env := p.Cmd.Path, p.Cmd.Env
	p.Cmd = exec.Command(filepath.Join(t.TempDir(), "not-yet", "ollama"), p.Cmd.Args[1:]...)
	p.Cmd.Env = env
	var found atomic.Bool
	p.Dormant = true
	p.Path = func() string {
		if found.Load() {
			return bin
		}
		return ""
	}
	p.Present = found.Load
	s := New([]Process{p}, testPolicy(), WithLog(io.Discard))
	running(t, s)
	if v := s.procViews(); len(v) != 1 || !v[0].Missing {
		t.Fatalf("views %+v: the program is not here yet", v)
	}
	found.Store(true)
	if v := s.procViews(); v[0].Missing {
		t.Fatalf("views %+v: the program is here now", v)
	}
	if err := s.Allow(RoleOllama, "", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "the program found after the boot running", func(sn Snapshot) bool {
		return proc(sn, RoleOllama).State == StateRunning
	})
}

// T03: a slot whose gate is closed does not start when wanted, and the front door's
// /slots/{i}/start is answered 409 component_off.
func TestSlotGateClosed(t *testing.T) {
	p, _ := fakeProc(t, RoleTVSlot, "serve")
	p.Instance, p.OnDemand = "amber", true
	s := New([]Process{p}, testPolicy(), WithLog(io.Discard))
	if err := s.Allow(RoleTVSlot, "amber", false); err != nil {
		t.Fatal(err)
	}
	running(t, s)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/slots/amber/start", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || body["code"] != "component_off" || body["component"] != "livetv.slots" {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	if err := s.Want(RoleTVSlot, "amber", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if st := proc(s.Snapshot(), RoleTVSlot); st.PID != 0 || !st.Wanted {
		t.Fatalf("slot started with its gate closed: %+v", st)
	}
	_ = s.Allow(RoleTVSlot, "amber", true)
	waitFor(t, s, "slot running", func(sn Snapshot) bool { return proc(sn, RoleTVSlot).State == StateRunning })
}

// T06: the cache says transcription is held and the database is down: no stt worker
// starts during the boot.
func TestHeldInCacheNeverStarts(t *testing.T) {
	p, _ := fakeProc(t, RolePipeline, "serve")
	p.Instance = "stt-1"
	path := filepath.Join(t.TempDir(), CompCacheFile)
	d := store.Desired{ID: "transcription", Want: components.WantEnabled, Generation: 9,
		Hold: &store.Hold{By: "ana", At: time.Now()}}
	if err := writeCompCache(path, store.Home, "full", map[string]store.Desired{"transcription": d}, 1842, time.Now()); err != nil {
		t.Fatal(err)
	}
	s := New([]Process{p}, testPolicy(), WithLog(io.Discard))
	r := NewReconciler(s)
	r.Log, r.CachePath, r.Tick, r.Retry = io.Discard, path, 10*time.Millisecond, 10*time.Millisecond
	r.Source = &fakeStore{err: errors.New("connection refused")}
	r.Prime()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	running(t, s)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		sn := s.Snapshot()
		if st := proc(sn, RolePipeline); st.PID != 0 {
			t.Fatalf("stt-1 started while held: %+v", st)
		}
		for _, e := range sn.Events {
			if e.Kind == EventStarted {
				t.Fatalf("started: %+v", e)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}
