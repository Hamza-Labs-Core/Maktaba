package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.04: the reconciler against a fake process table, a fake store and a fake clock.

// testCatalog is a catalog of the embedded one's phases and reasons plus comps (TOML
// [[component]] tables).
func testCatalog(t *testing.T, comps string) *components.Catalog {
	t.Helper()
	def := components.Default()
	q := func(l []string) string {
		out := make([]string, len(l))
		for i, s := range l {
			out[i] = fmt.Sprintf("%q", s)
		}
		return "[" + strings.Join(out, ", ") + "]"
	}
	src := fmt.Sprintf("schema = 1\nphases = %s\nreasons = %s\nfeatures = %s\n%s", q(def.Phases), q(def.Reasons),
		q(def.Features), comps)
	c, err := components.Load([]byte(src))
	if err != nil {
		t.Fatalf("test catalog: %v\n%s", err, src)
	}
	return c
}

// procComp is a [[component]] of kind process.
func procComp(id, process, requires, extra string) string {
	return fmt.Sprintf(`
[[component]]
id = %q
kind = "process"
group = "processing"
title_key = "components.%s.title"
desc_key = "components.%s.desc"
processes = [%q]
requires = [%s]
platforms = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
since = "0.4.0"
%s
`, id, id, id, process, requires, extra)
}

// chainCatalog: a <- b <- c, each one process (fake/a, fake/b, fake/c).
func chainCatalog(t *testing.T) *components.Catalog {
	return testCatalog(t, procComp("a", "fake/a", "", "")+procComp("b", "fake/b", `"a"`, "")+
		procComp("c", "fake/c", `"b"`, ""))
}

// fakeTable is a gateTable whose processes come up at once when allowed (or, with slow,
// stay starting until up is called) and stop at once.
type fakeTable struct {
	mu    sync.Mutex
	procs []procView
	calls []string
	slow  map[string]bool
}

func (f *fakeTable) add(comp, inst string, dormant bool) {
	f.procs = append(f.procs, procView{Role: "fake", Instance: inst, Component: comp, Dormant: dormant,
		Allowed: !dormant, Wanted: true, State: StateRunning, Running: !dormant})
	if dormant {
		f.procs[len(f.procs)-1].State = StateStopped
	}
}

func (f *fakeTable) procViews() []procView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.procs)
}

func (f *fakeTable) Allow(role Role, inst string, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.procs {
		p := &f.procs[i]
		if p.Role != role || p.Instance != inst {
			continue
		}
		f.calls = append(f.calls, fmt.Sprintf("%s=%v", inst, on))
		p.Allowed = on
		switch {
		case on && f.slow[inst]:
			p.State, p.Running = StateStarting, true
		case on:
			p.State, p.Running = StateRunning, true
		default:
			p.State, p.Running = StateStopped, false
		}
		return nil
	}
	return fmt.Errorf("no %s/%s", role, inst)
}

func (f *fakeTable) up(inst string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.procs {
		if f.procs[i].Instance == inst {
			f.procs[i].State = StateRunning
		}
	}
}

func (f *fakeTable) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

// fakeStore is the desired state and the status rows, counting its queries.
type fakeStore struct {
	mu      sync.Mutex
	desired map[string]store.Desired
	rev     int64
	err     error
	queries int
	reports int
	rows    []store.Status
}

func (s *fakeStore) Revision(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++
	return s.rev, s.err
}

func (s *fakeStore) Desired(context.Context, string) (map[string]store.Desired, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++
	if s.err != nil {
		return nil, 0, s.err
	}
	out := make(map[string]store.Desired, len(s.desired))
	for k, v := range s.desired {
		out[k] = v
	}
	return out, s.rev, nil
}

func (s *fakeStore) ReportStatus(_ context.Context, _, _ string, rows []store.Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports++
	s.rows = slices.Clone(rows)
	return nil
}

func (s *fakeStore) row(id string) (store.Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.ID == id {
			return r, true
		}
	}
	return store.Status{}, false
}

func newTestReconciler(cat *components.Catalog, tab gateTable, st *fakeStore) (*Reconciler, *syncWriter) {
	log := &syncWriter{w: &bytes.Buffer{}}
	r := &Reconciler{table: tab, Catalog: cat, Machine: store.Home, Bundle: "full", Log: log,
		Platform: "darwin-arm64"}
	if st != nil {
		r.Status = st
	}
	r.defaults()
	return r, log
}

// passWith runs one pass on desired.
func passWith(r *Reconciler, desired map[string]store.Desired) int {
	r.mu.Lock()
	r.desired, r.fromDB = desired, true
	r.mu.Unlock()
	return r.pass(context.Background())
}

func want(id, w string, gen int64) store.Desired {
	return store.Desired{ID: id, Want: w, Generation: gen}
}

// T04: gates close dependents first and open requirements first, one step a pass as each
// requirement comes up.
func TestReconcileOrder(t *testing.T) {
	cat := chainCatalog(t)
	if got := cat.Order(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("order %v", got)
	}
	tab := &fakeTable{}
	tab.add("a", "a", false)
	tab.add("b", "b", false)
	tab.add("c", "c", false)
	r, _ := newTestReconciler(cat, tab, &fakeStore{})
	off := map[string]store.Desired{"a": want("a", "installed", 2), "b": want("b", "installed", 2), "c": want("c", "installed", 2)}
	passWith(r, off)
	if got := tab.takeCalls(); !slices.Equal(got, []string{"c=false", "b=false", "a=false"}) {
		t.Fatalf("stops %v, want dependents first", got)
	}
	on := map[string]store.Desired{"a": want("a", "enabled", 3), "b": want("b", "enabled", 3), "c": want("c", "enabled", 3)}
	var opens []string
	for range 4 {
		passWith(r, on)
		opens = append(opens, tab.takeCalls()...)
	}
	if !slices.Equal(opens, []string{"a=true", "b=true", "c=true"}) {
		t.Fatalf("starts %v, want requirements first", opens)
	}
}

// T05: a requirement enabled but still starting leaves its dependent Waiting with
// RequirementsMet unknown; the first pass after it runs opens the dependent.
func TestReconcileWaitsForStartingRequirement(t *testing.T) {
	cat := chainCatalog(t)
	tab := &fakeTable{slow: map[string]bool{"a": true}}
	tab.add("a", "a", true)
	tab.add("b", "b", true)
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	on := map[string]store.Desired{"a": want("a", "enabled", 1), "b": want("b", "enabled", 1)}
	passWith(r, on)
	passWith(r, on)
	if got := tab.takeCalls(); !slices.Equal(got, []string{"a=true"}) {
		t.Fatalf("calls %v: only a opens while it starts", got)
	}
	row, _ := st.row("b")
	if row.Phase != components.PhaseWaiting {
		t.Fatalf("b is %q, want waiting", row.Phase)
	}
	var rm store.Condition
	for _, c := range row.Conditions {
		if c.Type == components.CondRequirementsMet {
			rm = c
		}
	}
	if rm.Status != "unknown" || rm.Params["component"] != "a" {
		t.Fatalf("b's RequirementsMet %+v, want unknown on a", rm)
	}
	if a, _ := st.row("a"); a.Phase != components.PhaseStarting {
		t.Fatalf("a is %q, want starting", a.Phase)
	}
	tab.up("a")
	passWith(r, on)
	if got := tab.takeCalls(); !slices.Equal(got, []string{"b=true"}) {
		t.Fatalf("calls %v: b opens the pass after a runs", got)
	}
}

// A requirement turned off: the dependent waits with dependency_off and keeps its wish.
func TestReconcileDependencyOff(t *testing.T) {
	cat := chainCatalog(t)
	tab := &fakeTable{}
	tab.add("a", "a", false)
	tab.add("b", "b", false)
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	passWith(r, map[string]store.Desired{"a": want("a", "installed", 2), "b": want("b", "enabled", 1)})
	if got := tab.takeCalls(); !slices.Equal(got, []string{"b=false", "a=false"}) {
		t.Fatalf("calls %v", got)
	}
	row, _ := st.row("b")
	if row.Phase != components.PhaseWaiting || row.Reason() != "dependency_off" ||
		row.Conditions[0].Params["component"] != "a" {
		t.Fatalf("b %+v, want waiting on dependency_off:a", row)
	}
	if a, _ := st.row("a"); a.Phase != components.PhaseOff || a.ObservedGeneration != 2 {
		t.Fatalf("a %+v, want off at generation 2", a)
	}
}

type fakeAssets map[string]bool

func (f fakeAssets) Installed(id string) bool { return f[id] }

// failingAssets are assets whose install failed (AssetFailures).
type failingAssets struct {
	fakeAssets
	failed map[string]string
}

func (f failingAssets) Failed(id string) (string, map[string]any, bool) {
	why, ok := f.failed[id]
	return why, map[string]any{"name": "m.bin"}, ok && !f.fakeAssets[id]
}

// Story 51.16: a pack asset a component requires keeps it waiting until it is installed;
// a model never does; with no desired row for the asset (before the seed) nothing waits.
func TestReconcileWaitsForAPackAsset(t *testing.T) {
	asset := func(id, src string) string {
		files := ""
		if strings.HasPrefix(src, "hf:") {
			files = `files = [{ path = "m.bin", sha256 = "` + strings.Repeat("ab", 32) + `", size = 1 }]`
		}
		return fmt.Sprintf(`
[[component]]
id = %q
kind = "asset"
group = "runtimes"
title_key = "components.%s.title"
desc_key = "components.%s.desc"
source = %q
%s
platforms = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
since = "0.4.0"
`, id, id, id, src, files)
	}
	cat := testCatalog(t, asset("runtime.x", "pack:runtime.x")+
		asset("model.x", "hf:org/name@0a363e9161cbc7ed1431c9597a8ceaf0c4f78fcf")+
		procComp("stt", "fake/stt", `"runtime.x", "model.x"`, ""))
	tab := &fakeTable{}
	tab.add("stt", "stt", true)
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	assets := fakeAssets{}
	r.Assets = assets
	d := map[string]store.Desired{"stt": want("stt", "enabled", 1), "runtime.x": want("runtime.x", "installed", 1),
		"model.x": want("model.x", "absent", 1)}
	passWith(r, d)
	if got := tab.takeCalls(); len(got) != 0 {
		t.Fatalf("calls %v: stt opened before its pack was installed", got)
	}
	row, _ := st.row("stt")
	var rm store.Condition
	for _, c := range row.Conditions {
		if c.Type == components.CondRequirementsMet {
			rm = c
		}
	}
	if row.Phase != components.PhaseWaiting || rm.Status != "unknown" || rm.Params["component"] != "runtime.x" {
		t.Fatalf("stt %+v, want waiting on runtime.x", row)
	}
	assets["runtime.x"] = true
	passWith(r, d)
	if got := tab.takeCalls(); !slices.Equal(got, []string{"stt=true"}) {
		t.Fatalf("calls %v: stt opens once the pack is in (the model never blocks)", got)
	}
	// open, but its model is not here: it says it waits on it, never running
	reqOf := func() (string, store.Condition) {
		row, _ := st.row("stt")
		for _, c := range row.Conditions {
			if c.Type == components.CondRequirementsMet {
				return row.Phase, c
			}
		}
		return row.Phase, store.Condition{}
	}
	if ph, c := reqOf(); ph != components.PhaseWaiting || c.Status != "unknown" || c.Params["component"] != "model.x" {
		t.Fatalf("stt %s %+v, want waiting on model.x", ph, c)
	}
	// its install failed: that reason, naming the model
	failing := failingAssets{fakeAssets: assets, failed: map[string]string{"model.x": "download_failed"}}
	r.Assets = failing
	passWith(r, d)
	if ph, c := reqOf(); ph != components.PhaseWaiting || c.Status != "false" || c.Reason != "download_failed" ||
		c.Params["component"] != "model.x" {
		t.Fatalf("stt %s %+v, want waiting on model.x's failed install", ph, c)
	}
	// the model in: running
	assets["model.x"] = true
	passWith(r, d)
	if ph, c := reqOf(); ph != components.PhaseRunning || c.Status != "true" {
		t.Fatalf("stt %s %+v, want running", ph, c)
	}
	// no desired row for the pack: today's behaviour
	tab2 := &fakeTable{}
	tab2.add("stt", "stt", true)
	r2, _ := newTestReconciler(cat, tab2, &fakeStore{})
	r2.Assets = fakeAssets{}
	passWith(r2, map[string]store.Desired{"stt": want("stt", "enabled", 1)})
	if got := tab2.takeCalls(); !slices.Equal(got, []string{"stt=true"}) {
		t.Fatalf("calls %v with no row for the pack", got)
	}
}

// A component with no desired row runs as server.toml built it: a built process stays
// open, a dormant one closed.
func TestReconcileNoRowIsToday(t *testing.T) {
	cat := chainCatalog(t)
	tab := &fakeTable{}
	tab.add("a", "a", false)
	tab.add("c", "c", true)
	r, _ := newTestReconciler(cat, tab, &fakeStore{})
	passWith(r, map[string]store.Desired{})
	if got := tab.takeCalls(); len(got) != 0 {
		t.Fatalf("calls %v, want none", got)
	}
	passWith(r, map[string]store.Desired{"c": want("c", "enabled", 1)})
	if got := tab.takeCalls(); !slices.Equal(got, []string{"c=true"}) {
		t.Fatalf("calls %v: a dormant process starts once enabled (its requirement b has no row)", got)
	}
}

// A hold closes the gate (Stopped, held) and is never cleared by the reconciler.
func TestReconcileHold(t *testing.T) {
	cat := chainCatalog(t)
	tab := &fakeTable{}
	tab.add("a", "a", false)
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	d := want("a", "enabled", 4)
	d.Hold = &store.Hold{By: "ana", At: time.Now()}
	for range 3 {
		passWith(r, map[string]store.Desired{"a": d})
	}
	if got := tab.takeCalls(); !slices.Equal(got, []string{"a=false"}) {
		t.Fatalf("calls %v", got)
	}
	if row, _ := st.row("a"); row.Phase != components.PhaseStopped || row.Reason() != "held" {
		t.Fatalf("a %+v, want stopped, held", row)
	}
}

// A platform the component does not list keeps it waiting.
func TestReconcilePlatform(t *testing.T) {
	cat := testCatalog(t, strings.Replace(procComp("a", "fake/a", "", ""),
		`platforms = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]`, `platforms = ["linux-amd64"]`, 1))
	tab := &fakeTable{}
	tab.add("a", "a", false)
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	passWith(r, map[string]store.Desired{"a": want("a", "enabled", 1)})
	if row, _ := st.row("a"); row.Phase != components.PhaseWaiting || row.Reason() != "platform_unsupported" {
		t.Fatalf("a %+v", row)
	}
}

// Memory is checked before a dormant process first starts, never to stop one that runs.
func TestReconcileMemoryAtStartOnly(t *testing.T) {
	cat := testCatalog(t, procComp("a", "fake/a", "", "resources = { ram_mb = 6000 }")+
		procComp("b", "fake/b", "", "resources = { ram_mb = 6000 }"))
	tab := &fakeTable{}
	tab.add("a", "a", true)
	tab.add("b", "b", false)
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	r.FreeMB = func() int64 { return 1000 }
	passWith(r, map[string]store.Desired{"a": want("a", "enabled", 1), "b": want("b", "enabled", 1)})
	if got := tab.takeCalls(); len(got) != 0 {
		t.Fatalf("calls %v: a stays closed, b keeps running", got)
	}
	if row, _ := st.row("a"); row.Reason() != "not_enough_memory" {
		t.Fatalf("a %+v", row)
	}
}

// Story 51.14: a process component's row carries its memory (rss_mb, read every 5 min, a
// few MB either way not rewritten) and its restarts of the last 24 h (restarts_24h).
func TestReconcileRowsCarryMemoryAndRestartsOfADay(t *testing.T) {
	cat := testCatalog(t, procComp("a", "fake/a*", "", ""))
	tab := &fakeTable{}
	tab.add("a", "a1", false)
	tab.add("a", "a2", false)
	tab.procs[0].PID, tab.procs[1].PID = 101, 102
	tab.procs[0].Restarts, tab.procs[0].Restarts24h = 9, 2
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	mem := map[int]uint64{101: 300 << 20, 102: 200 << 20}
	reads := 0
	r.RSS = func(pids []int) map[int]uint64 {
		reads++
		out := map[int]uint64{}
		for _, p := range pids {
			if b, ok := mem[p]; ok {
				out[p] = b
			}
		}
		return out
	}
	d := map[string]store.Desired{"a": want("a", "enabled", 1)}
	passWith(r, d)
	row, _ := st.row("a")
	if row.Detail["rss_mb"] != int64(500) || row.Detail["restarts_24h"] != 2 || row.Detail["restarts"] != 9 {
		t.Fatalf("detail %v", row.Detail)
	}
	// a minute later: not read again; five minutes later a few MB more: not rewritten
	mem[101] = 310 << 20
	now = now.Add(time.Minute)
	passWith(r, d)
	now = now.Add(5 * time.Minute)
	passWith(r, d)
	if row, _ := st.row("a"); reads != 2 || row.Detail["rss_mb"] != int64(500) {
		t.Fatalf("%d reads, rss %v", reads, row.Detail["rss_mb"])
	}
	// a real change is written at the next read
	mem[101] = 900 << 20
	now = now.Add(5 * time.Minute)
	passWith(r, d)
	if row, _ := st.row("a"); row.Detail["rss_mb"] != int64(1100) {
		t.Fatalf("rss %v, want 1100", row.Detail["rss_mb"])
	}
	// the restarts of a day: one older than 24 h is not counted
	at := now.Add(-25 * time.Hour)
	if got := recentRestarts([]time.Time{at, now.Add(-time.Hour), now}, now); len(got) != 2 {
		t.Fatalf("restarts of the last 24 h %v", got)
	}
}

// A dormant process whose program is not on this machine (ollama before its pack) waits,
// enabled, with not_in_payload, and starts once the program is there.
func TestReconcileProgramNotHereWaits(t *testing.T) {
	cat := testCatalog(t, procComp("a", "fake/a", "", ""))
	tab := &fakeTable{}
	tab.add("a", "a", true)
	tab.procs[0].Missing = true
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	d := map[string]store.Desired{"a": want("a", "enabled", 1)}
	passWith(r, d)
	if got := tab.takeCalls(); len(got) != 0 {
		t.Fatalf("calls %v: a's program is not here, it stays closed", got)
	}
	if row, _ := st.row("a"); row.Phase != components.PhaseWaiting || row.Reason() != "not_in_payload" {
		t.Fatalf("a %+v", row)
	}
	tab.mu.Lock()
	tab.procs[0].Missing = false
	tab.mu.Unlock()
	passWith(r, d)
	if got := tab.takeCalls(); !slices.Equal(got, []string{"a=true"}) {
		t.Fatalf("calls %v: a's program came, it starts", got)
	}
}

// T06 (fake table): the cache says held: Prime closes the gate before anything starts.
func TestReconcilePrimeFromCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), CompCacheFile)
	d := want("transcription", "enabled", 9)
	d.Hold = &store.Hold{By: "ana", At: time.Date(2026, 11, 2, 21, 40, 0, 0, time.UTC)}
	if err := writeCompCache(path, store.Home, "full", map[string]store.Desired{"transcription": d}, 1842, time.Now()); err != nil {
		t.Fatal(err)
	}
	tab := &fakeTable{}
	tab.procs = []procView{{Role: RolePipeline, Instance: "stt-1", Component: "transcription", Allowed: true, Wanted: true,
		State: StateStarting}}
	r, _ := newTestReconciler(components.Default(), tab, nil)
	r.CachePath = path
	r.Prime()
	if got := tab.takeCalls(); !slices.Equal(got, []string{"stt-1=false"}) {
		t.Fatalf("calls %v", got)
	}
	// another machine's cache is not this one's
	fresh := &fakeTable{procs: []procView{{Role: RolePipeline, Instance: "stt-1", Component: "transcription",
		Allowed: true, Wanted: true}}}
	r2, _ := newTestReconciler(components.Default(), fresh, nil)
	r2.Machine = "5b0c3f5e-0000-4000-8000-000000000001"
	r2.CachePath = path
	r2.Prime()
	if got := r2.table.(*fakeTable).takeCalls(); len(got) != 0 {
		t.Fatalf("calls %v from another machine's cache", got)
	}
}

// T07: the cache (revision 50, held) loses to the database (revision 40, enabled).
func TestReconcileDatabaseWinsOverCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), CompCacheFile)
	d := want("transcription", "enabled", 9)
	d.Hold = &store.Hold{By: "ana", At: time.Now()}
	if err := writeCompCache(path, store.Home, "full", map[string]store.Desired{"transcription": d}, 50, time.Now()); err != nil {
		t.Fatal(err)
	}
	tab := &fakeTable{}
	tab.procs = []procView{{Role: RolePipeline, Instance: "stt-1", Component: "transcription", Allowed: true, Wanted: true}}
	st := &fakeStore{desired: map[string]store.Desired{"transcription": want("transcription", "enabled", 3)}, rev: 40}
	r, log := newTestReconciler(components.Default(), tab, st)
	r.Source, r.CachePath, r.Tick = st, path, 10*time.Millisecond
	r.Prime()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if v := tab.procViews(); v[0].Allowed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stt-1 not allowed; log:\n%s", log)
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitLog(t, log, "the database wins")
	// and the cache now holds the database's answer
	waitFor := time.Now().Add(5 * time.Second)
	for {
		got, rev, ok, _ := readCompCache(path, store.Home)
		if ok && rev == 40 && got["transcription"].Hold == nil {
			break
		}
		if time.Now().After(waitFor) {
			t.Fatalf("cache not rewritten: rev %d %+v", rev, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitLog(t, log, "components_reconciled changed=1 revision=40")
}

func waitLog(t *testing.T, log *syncWriter, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(log.String(), s) {
		if time.Now().After(deadline) {
			t.Fatalf("log has no %q:\n%s", s, log.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// T08 and AC4: reconcile = off opens and closes nothing, whatever the desired state, and
// every row says reconcile_off.
func TestReconcileOff(t *testing.T) {
	tab := &fakeTable{}
	tab.procs = []procView{{Role: RoleOllama, Component: "llm", Allowed: true, Wanted: true, State: StateRunning, Running: true}}
	st := &fakeStore{}
	r, _ := newTestReconciler(components.Default(), tab, st)
	r.Off = true
	r.Prime()
	passWith(r, map[string]store.Desired{"llm": want("llm", "installed", 2)})
	if got := tab.takeCalls(); len(got) != 0 {
		t.Fatalf("calls %v with reconcile off", got)
	}
	row, ok := st.row("llm")
	if !ok || row.Phase != components.PhaseRunning || row.Reason() != "reconcile_off" {
		t.Fatalf("llm %+v, want running with reconcile_off", row)
	}
}

// The kill switch reaches every child.
func TestReconcileOffEnv(t *testing.T) {
	cfg := config.Defaults()
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline, RoleTVSlot} {
		env, err := ChildEnv(role, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(env, func(s string) bool { return strings.HasPrefix(s, EnvComponentsReconcile+"=") }) {
			t.Fatalf("%s: %s set with reconcile on: %v", role, EnvComponentsReconcile, env)
		}
		cfg.Components.Reconcile = "off"
		env, err = ChildEnv(role, cfg)
		cfg.Components.Reconcile = ""
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(env, EnvComponentsReconcile+"=off") {
			t.Fatalf("%s: no %s=off: %v", role, EnvComponentsReconcile, env)
		}
	}
}

// T09 (N12): five idle minutes cost at most ten queries and write no status.
func TestReconcileIdleCost(t *testing.T) {
	tab := &fakeTable{}
	tab.procs = []procView{{Role: RoleOllama, Component: "llm", Allowed: true, Wanted: true, State: StateRunning, Running: true}}
	st := &fakeStore{desired: map[string]store.Desired{"llm": want("llm", "enabled", 1)}, rev: 7}
	r, _ := newTestReconciler(components.Default(), tab, st)
	ticks := make(chan time.Time)
	r.Source, r.Tick = st, 2*time.Millisecond
	r.After = func(time.Duration) <-chan time.Time { return ticks }
	r.Prime()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		st.mu.Lock()
		reports := st.reports
		st.mu.Unlock()
		if reports > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no first report")
		}
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // the first passes settle
	st.mu.Lock()
	q0, w0 := st.queries, st.reports
	st.mu.Unlock()
	for range 10 { // 5 minutes of the 30 s poll, with many 1 s passes in between
		ticks <- time.Now()
		time.Sleep(10 * time.Millisecond)
	}
	st.mu.Lock()
	q, w := st.queries-q0, st.reports-w0
	st.mu.Unlock()
	if q > 10 || w != 0 {
		t.Fatalf("idle: %d queries, %d status writes; want <= 10 and 0", q, w)
	}
}

// T11: each process state maps to the phase of §6.
func TestProcPhase(t *testing.T) {
	for _, c := range []struct {
		name  string
		p     procView
		phase string
		why   string
	}{
		{"gate closed", procView{Wanted: true}, components.PhaseOff, ""},
		{"asked to stop", procView{Allowed: false, Running: true, Draining: true, Wanted: true}, components.PhaseDraining, ""},
		{"starting", procView{Allowed: true, Wanted: true, State: StateStarting, Running: true}, components.PhaseStarting, ""},
		{"running", procView{Allowed: true, Wanted: true, State: StateRunning, Running: true}, components.PhaseRunning, ""},
		{"backoff", procView{Allowed: true, Wanted: true, State: StateBackoff, Restarts: 2}, components.PhaseStarting, ""},
		{"crash loop", procView{Allowed: true, Wanted: true, State: StateFailed}, components.PhaseFailed, "crash_loop"},
		{"unhealthy", procView{Allowed: true, Wanted: true, State: StateUnhealthy, Running: true}, components.PhaseDegraded, ""},
	} {
		ph, why, ok := procPhase(c.p)
		if !ok || ph != c.phase || why != c.why {
			t.Errorf("%s: %q %q %v, want %q %q", c.name, ph, why, ok, c.phase, c.why)
		}
	}
	if _, _, ok := procPhase(procView{Allowed: true, Wanted: false, State: StateStopped}); ok {
		t.Error("a process nobody wants (a standby slot) counts")
	}
	if _, _, ok := procPhase(procView{Allowed: true, Wanted: true, Once: true, State: StateExited}); ok {
		t.Error("a job that ended counts")
	}
	// the component's row: a restart shows in its detail; the closed gate's phase wins
	tab := &fakeTable{}
	tab.procs = []procView{{Role: RolePipeline, Instance: "stt-1", Component: "transcription", Allowed: true, Wanted: true,
		State: StateBackoff, Restarts: 3}}
	st := &fakeStore{}
	r, _ := newTestReconciler(components.Default(), tab, st)
	passWith(r, map[string]store.Desired{})
	if row, _ := st.row("transcription"); row.Phase != components.PhaseStarting || row.Detail["restarts"] != 3 {
		t.Fatalf("transcription %+v", row)
	}
}

// The supervisor's loops: started when on, cancelled when off (Draining until they
// return), and kept as a boot flag says.
func TestReconcileLoops(t *testing.T) {
	tab := &fakeTable{}
	st := &fakeStore{}
	r, _ := newTestReconciler(components.Default(), tab, st)
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	backups := &Loop{Name: "supervisor:backups", ConfigOn: true, Run: func(ctx context.Context) {
		started <- struct{}{}
		<-ctx.Done()
		<-release // a dump that ends after the stop
	}}
	mdns := &Loop{Name: "supervisor:mdns", ConfigOn: true, Forced: ReasonBootFlag,
		ForcedParams: map[string]any{"flag": "--no-mdns"}, Run: func(_ context.Context) {
			t.Error("mdns ran under --no-mdns")
		}}
	r.Loops = []*Loop{backups, mdns}
	r.defaults()
	ctx := context.Background()
	passWith(r, map[string]store.Desired{"discovery.mdns": want("discovery.mdns", "enabled", 1)})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("backups did not start")
	}
	if row, _ := st.row("discovery.mdns"); row.Phase != components.PhaseOff || row.Reason() != "boot_flag" {
		t.Fatalf("mdns %+v", row)
	}
	if row, _ := st.row("backups"); row.Phase != components.PhaseRunning {
		t.Fatalf("backups %+v", row)
	}
	off := map[string]store.Desired{"backups": want("backups", "installed", 2)}
	passWith(r, off)
	if row, _ := st.row("backups"); row.Phase != components.PhaseDraining {
		t.Fatalf("backups %+v, want draining while its dump runs", row)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.pass(ctx)
		if row, _ := st.row("backups"); row.Phase == components.PhaseOff {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backups never off")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// on again: a new run
	passWith(r, map[string]store.Desired{"backups": want("backups", "enabled", 3)})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("backups did not start again")
	}
}

// A failed read is said once, and the gates stay as the cache set them.
func TestReconcileDatabaseDown(t *testing.T) {
	tab := &fakeTable{}
	tab.procs = []procView{{Role: RolePipeline, Instance: "stt-1", Component: "transcription", Allowed: true, Wanted: true}}
	st := &fakeStore{err: errors.New("connection refused")}
	r, log := newTestReconciler(components.Default(), tab, nil)
	r.Source, r.Retry, r.Tick = st, 5*time.Millisecond, 5*time.Millisecond
	r.Prime()
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	if n := strings.Count(log.String(), "connection refused"); n != 1 {
		t.Fatalf("said %d times:\n%s", n, log.String())
	}
	if got := tab.takeCalls(); len(got) != 0 {
		t.Fatalf("calls %v with the database down and no cache", got)
	}
}

// The grace Allow(false) gives: the catalog's drain plus 10 s (transcription: 130 s), the
// process's own StopGrace when longer, 10 s for a process with no component.
func TestComponentGrace(t *testing.T) {
	cat := components.Default()
	if g := componentGrace(cat, Process{Component: "transcription"}); g != 130*time.Second {
		t.Fatalf("transcription %s", g)
	}
	if g := componentGrace(cat, Process{Component: "search.semantic", StopGrace: 5 * time.Minute}); g != 5*time.Minute {
		t.Fatalf("index %s", g)
	}
	if g := componentGrace(cat, Process{}); g != wantStopGrace {
		t.Fatalf("none %s", g)
	}
}

// The cache file round-trips.
func TestCompCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", CompCacheFile)
	at := time.Date(2026, 11, 2, 21, 40, 0, 0, time.UTC)
	in := map[string]store.Desired{"llm": want("llm", "enabled", 3),
		"transcription": {ID: "transcription", Want: "enabled", Hold: &store.Hold{By: "ana", At: at}, Generation: 9}}
	if err := writeCompCache(path, store.Home, "full", in, 1842, at); err != nil {
		t.Fatal(err)
	}
	got, rev, ok, err := readCompCache(path, store.Home)
	if err != nil || !ok || rev != 1842 || got["transcription"].Hold == nil || !got["transcription"].Hold.At.Equal(at) ||
		got["llm"].Generation != 3 {
		t.Fatalf("%v %v %d %+v", err, ok, rev, got)
	}
	if _, _, ok, err := readCompCache(filepath.Join(t.TempDir(), "none.json"), store.Home); ok || err != nil {
		t.Fatalf("missing file: %v %v", ok, err)
	}
}

// /processes names each process's component (the desktop app groups them by it).
func TestSnapshotComponent(t *testing.T) {
	s := New([]Process{{Role: RolePipeline, Instance: "stt-2"}, {Role: RoleAPI}, {Role: RoleOllama, Dormant: true}},
		testPolicy(), WithLog(io.Discard))
	got := map[Role]ChildStatus{}
	for _, p := range s.Snapshot().Processes {
		got[p.Role] = p
	}
	if got[RolePipeline].Component != "transcription" || got[RoleAPI].Component != "core.api" ||
		got[RoleOllama].Component != "llm" {
		t.Fatalf("%+v", got)
	}
	if !got[RoleOllama].Blocked || got[RoleOllama].State != StateStopped || got[RoleAPI].Blocked {
		t.Fatalf("a dormant process starts blocked: %+v", got)
	}
}

// What is built follows the cached desired state where it disagrees with server.toml
// (the database wins); with no cache, or reconcile = off, server.toml decides.
func TestBootConfig(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.DataDir = t.TempDir()
	cfg.TV.Slots, cfg.LLM.Enabled = "off", "off"
	if got, changed := BootConfig(cfg, store.Home); got.TV.SlotsOn() || len(changed) != 0 {
		t.Fatalf("no cache: %v %v", got.TV, changed)
	}
	desired := map[string]store.Desired{"livetv.slots": want("livetv.slots", "enabled", 1),
		"llm": want("llm", "enabled", 1), "cloud.link": want("cloud.link", "installed", 1)}
	if err := writeCompCache(CompCachePath(cfg.StateDir()), store.Home, "full", desired, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, changed := BootConfig(cfg, store.Home)
	if !got.TV.SlotsOn() || got.LLM.Enabled != "on" || got.Cloud.Enabled ||
		!slices.Equal(changed, []string{"tv.slots=on", "llm.enabled=on"}) {
		t.Fatalf("%+v %+v %v", got.TV, got.LLM, changed)
	}
	if back := FileKeys(got, cfg); back.TV.SlotsOn() || back.LLM.Enabled != "off" {
		t.Fatalf("FileKeys: %+v", back.TV)
	}
	// another machine's cache, the kill switch: as the file says
	if got, _ := BootConfig(cfg, "5b0c3f5e-0000-4000-8000-000000000001"); got.TV.SlotsOn() {
		t.Fatal("another machine's cache applied")
	}
	cfg.Components.Reconcile = "off"
	if got, _ := BootConfig(cfg, store.Home); got.TV.SlotsOn() {
		t.Fatal("applied with reconcile = off")
	}
	// slots off in the database and the in-process engine on: slots off
	cfg.Components.Reconcile, cfg.TV.Slots = "", "on"
	desired["livetv.slots"] = want("livetv.slots", "installed", 2)
	desired["livetv.inprocess"] = want("livetv.inprocess", "enabled", 2)
	if err := writeCompCache(CompCachePath(cfg.StateDir()), store.Home, "full", desired, 4, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := BootConfig(cfg, store.Home); got.TV.SlotsOn() {
		t.Fatal("slots still on")
	}
}

// Story 51.09: [components] capabilities_gate = "off" reaches the api as its env.
func TestCapabilitiesGateOffEnv(t *testing.T) {
	cfg := config.Defaults()
	env, err := ChildEnv(RoleAPI, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(env, func(s string) bool { return strings.HasPrefix(s, EnvComponentsCapabilitiesGate+"=") }) {
		t.Fatalf("%s set with the gate on: %v", EnvComponentsCapabilitiesGate, env)
	}
	cfg.Components.CapabilitiesGate = "off"
	if env, err = ChildEnv(RoleAPI, cfg); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, EnvComponentsCapabilitiesGate+"=off") {
		t.Fatalf("no %s=off: %v", EnvComponentsCapabilitiesGate, env)
	}
}
