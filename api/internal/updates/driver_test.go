package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMachine plays a machine's supervisor: prepare -> prepared (or prepare_failed),
// apply -> applying, then the outcome on the next status (committed, rolled_back,
// apply_failed, or nothing: it never answers again).
type fakeMachine struct {
	mu        sync.Mutex
	st        *MachineStatus
	version   string
	prepFail  string // a failure code at prepare
	outcome   string // after apply: MCommitted (default), MRolledBack, MApplyFailed, "silent"
	applies   int
	onApply   func()
	installer string
	// asks: a desktop app (owner decision D1): apply orders, a click, then apply again
	asks bool
	// pendingFor: status reports a start step (watching, pending) this many times after
	// the apply before the outcome
	pendingFor int
	// backup: the dump its commit reports
	backup *RunBackup
}

// click is someone choosing to install on the machine's desktop app.
func (m *fakeMachine) click() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st != nil && m.st.State == MAwaitingClick {
		m.st.State, m.st.App = MClicked, "clicked"
	}
}

func (m *fakeMachine) state() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st == nil {
		return ""
	}
	return m.st.State
}

func (m *fakeMachine) do(op string, body any) (*MachineStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch op {
	case "prepare":
		b, _ := json.Marshal(body)
		var j MachineJob
		_ = json.Unmarshal(b, &j)
		if m.st != nil && m.st.RunID() == j.RunID {
			return m.copy(), nil
		}
		m.st = &MachineStatus{Job: &j, State: MPreparing, From: m.version, Installer: m.installer, Asks: m.asks,
			Steps: []MachineStep{{Step: "preflight", State: "done"}}}
	case "apply":
		if m.st != nil && m.st.State == MAwaitingClick {
			return m.copy(), nil // asked again: waits for its click
		}
		if m.st != nil && m.st.State == MPrepared && m.asks {
			m.st.State, m.st.App = MAwaitingClick, "ordered"
			return m.copy(), nil
		}
		if m.st == nil || (m.st.State != MPrepared && m.st.State != MClicked) {
			return m.copy(), errors.New("update_not_ready")
		}
		m.st.State = MApplying
		m.applies++
		if m.onApply != nil {
			m.onApply()
		}
	case "abort":
		if m.st != nil {
			switch m.st.State {
			case MPreparing, MPrepared, MAwaitingClick, MClicked:
				m.st.State = MAborted
			case MAborted, MPrepareFailed:
			default:
				return m.copy(), errors.New("past_switch")
			}
		}
	case "rollback":
		if m.st != nil {
			m.st.State = MRolledBack
		}
	case "status":
		if m.st == nil {
			return nil, nil
		}
		switch m.st.State {
		case MPreparing:
			if m.prepFail != "" {
				m.st.State, m.st.Error = MPrepareFailed, &MachineError{Step: "stage", Code: m.prepFail}
			} else {
				m.st.State = MPrepared
			}
		case MApplying, MWatching:
			if m.pendingFor > 0 {
				m.pendingFor--
				m.st.State, m.st.Pending = MWatching, "pipeline: Building the Python environment"
				return m.copy(), nil
			}
			m.st.Pending = ""
			switch m.outcome {
			case "silent":
				return nil, errors.New("restarting")
			case MRolledBack:
				m.st.State, m.st.Reason = MRolledBack, "api /readyz 503"
			case MApplyFailed:
				m.st.State, m.st.Error = MApplyFailed, &MachineError{Step: "restart", Code: "app_install_failed"}
			default:
				m.st.State = MCommitted
				m.st.Backup = m.backup
				m.version = m.st.Job.Version
			}
		}
	}
	return m.copy(), nil
}

func (m *fakeMachine) copy() *MachineStatus {
	if m.st == nil {
		return nil
	}
	c := *m.st
	return &c
}

// homeFake is HomeSupervisor over a fakeMachine.
type homeFake struct{ m *fakeMachine }

func (h homeFake) Do(_ context.Context, op string, body any) (*MachineStatus, error) {
	return h.m.do(op, body)
}
func (h homeFake) Log(context.Context, string) ([]byte, error) { return []byte("log"), nil }

// clusterFake is Cluster over fake machines, by node id (session "s-<id>").
type clusterFake struct {
	mu       sync.Mutex
	machines map[string]*fakeMachine
	offline  map[string]bool
	homeCmds []string
	order    []string // apply order
	applying map[string]bool
	overlap  bool // two applies at once seen
}

func newClusterFake() *clusterFake {
	return &clusterFake{machines: map[string]*fakeMachine{}, offline: map[string]bool{}, applying: map[string]bool{}}
}

func (c *clusterFake) add(id, version string) *fakeMachine {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := &fakeMachine{version: version, installer: "layout"}
	c.machines[id] = m
	return m
}

func (c *clusterFake) MachineAgents() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]string{}
	for id := range c.machines {
		if !c.offline[id] {
			out[id] = "s-" + id
		}
	}
	return out
}

func (c *clusterFake) MachineStates() map[string]json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]json.RawMessage{}
	for id, m := range c.machines {
		if c.offline[id] {
			continue
		}
		m.mu.Lock()
		b, _ := json.Marshal(map[string]any{"version": m.version, "platform": "", "layout": m.installer})
		m.mu.Unlock()
		out[id] = b
	}
	return out
}

func (c *clusterFake) HomeProcesses() []string { return []string{"home-worker"} }

func (c *clusterFake) Command(_ context.Context, sid, name string, args any, _ time.Duration) (bool, json.RawMessage, error) {
	c.mu.Lock()
	if sid == "home-worker" {
		c.homeCmds = append(c.homeCmds, name)
		c.mu.Unlock()
		return true, nil, nil
	}
	id := strings.TrimPrefix(sid, "s-")
	m, ok := c.machines[id]
	if !ok || c.offline[id] {
		c.mu.Unlock()
		return false, nil, errors.New("no session")
	}
	if name == "update.apply" {
		for other, on := range c.applying {
			if on && other != id {
				c.overlap = true
			}
		}
		c.applying[id] = true
		c.order = append(c.order, id)
	}
	c.mu.Unlock()
	st, err := m.do(strings.TrimPrefix(name, "update."), args)
	if st != nil && (st.State == MCommitted || st.State == MRolledBack || st.State == MApplyFailed ||
		st.State == MAwaitingClick || st.State == MClicked || st.State == MAborted) {
		c.mu.Lock()
		c.applying[id] = false
		c.mu.Unlock()
	}
	b, _ := json.Marshal(map[string]any{"status": st})
	if err != nil {
		return false, b, nil
	}
	return true, b, nil
}

type driverRig struct {
	f       *fixture
	d       *Driver
	home    *fakeMachine
	cluster *clusterFake
	viewers int
}

func newDriverRig(t *testing.T) *driverRig {
	t.Helper()
	enableAutoInstall(t)
	f := newFixture(t, sqliteDB(t))
	seed(t, f.store, rel("1.5.0", func(r *Release) {
		r.Assets = []Asset{{Name: "release.json", URL: "https://github.test/release.json"}}
	}))
	r := &driverRig{f: f, home: &fakeMachine{version: "1.4.0", installer: "appbundle"}, cluster: newClusterFake()}
	r.d = &Driver{Store: f.store, Svc: f.svc, Home: homeFake{r.home}, Cluster: r.cluster,
		Now: func() time.Time { return f.now }, Viewers: func(context.Context) int { return r.viewers },
		Getenv: func(string) string { return "" }}
	f.svc.Starter = r.d
	return r
}

func (r *driverRig) node(t *testing.T, id, name, platform, version string) *fakeMachine {
	t.Helper()
	if _, err := r.f.db.Exec(`INSERT INTO cluster_nodes (id, name, state, platform, version, full_node) VALUES ($1, $2, 'approved', $3, $4, 1)`,
		id, name, platform, version); err != nil {
		t.Fatal(err)
	}
	return r.cluster.add(id, version)
}

// drive steps the driver until no run is active (or n steps), moving the clock a
// minute a step.
func (r *driverRig) drive(t *testing.T, n int) Run {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := r.d.Step(ctx); err != nil {
			t.Logf("step: %v", err)
		}
		r.f.now = r.f.now.Add(time.Minute)
		if a, _ := r.f.store.ActiveRun(ctx); a == nil {
			break
		}
	}
	runs, err := r.f.store.Runs(ctx, 10)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs: %v %v", runs, err)
	}
	return runs[0]
}

func targetsByName(t *testing.T, s *Store, runID string) map[string]Target {
	t.Helper()
	ts, err := s.Targets(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Target{}
	for _, x := range ts {
		out[x.Name] = x
	}
	return out
}

// The home updates first, then every machine, one at a time, each checked before the
// next; the workers are drained while the home applies and put back after.
func TestDriverHomeThenMachinesOneAtATime(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.node(t, "n2", "bravo", "windows-amd64", "1.4.0")
	if _, err := r.f.db.Exec(`UPDATE cluster_nodes SET drain = 1 WHERE id = 'n2'`); err != nil {
		t.Fatal(err)
	}
	drainedAtApply := false
	r.home.onApply = func() {
		var n int
		_ = r.f.db.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE drain = 1`).Scan(&n)
		drainedAtApply = n == 2
	}
	if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" {
		t.Fatalf("tick: %+v %v", d, err)
	}
	run := r.drive(t, 60)
	if run.State != RunDone {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	if wm, _ := r.f.store.Watermark(ctx); wm != "1.5.0" {
		t.Fatalf("watermark %q", wm)
	}
	if !drainedAtApply || r.home.applies != 1 {
		t.Fatalf("drained at the home's apply %v, applies %d", drainedAtApply, r.home.applies)
	}
	var drained int
	_ = r.f.db.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE drain = 1`).Scan(&drained)
	if drained != 1 { // n2 was drained before the run: it stays so
		t.Fatalf("drain left on %d machines", drained)
	}
	if got := strings.Join(r.cluster.homeCmds, ","); got != "drain,undrain" {
		t.Fatalf("home worker commands %s", got)
	}
	if r.cluster.overlap || strings.Join(r.cluster.order, ",") != "n1,n2" {
		t.Fatalf("apply order %v overlap %v", r.cluster.order, r.cluster.overlap)
	}
	ts := targetsByName(t, r.f.store, run.ID)
	if ts["alpha"].State != TargetHealthy || ts["bravo"].State != TargetHealthy || ts["alpha"].HealthyAt == nil {
		t.Fatalf("targets %+v", ts)
	}
	kind, _, _ := r.f.store.RunDetailOf(ctx, run.ID)
	if kind != "appbundle" {
		t.Fatalf("install kind %q", kind)
	}
	steps, _ := r.f.store.Steps(ctx, run.ID)
	hosts := map[string]bool{}
	for _, s := range steps {
		hosts[s.Host] = true
	}
	if !hosts["home"] || !hosts["n1"] || !hosts["n2"] {
		t.Fatalf("steps by host %v", hosts)
	}
	if r.f.notes.find(NoticeInstalledHome) == nil || r.f.notes.find(NoticeRolloutComplete) == nil {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
}

// A home that rolls back leaves the machines alone and blocks the release.
func TestDriverHomeRollbackTouchesNoMachine(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	n1 := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.home.outcome = MRolledBack
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 30)
	if run.State != RunRolledBack {
		t.Fatalf("run %s", run.State)
	}
	if n1.st != nil {
		t.Fatalf("a machine was asked: %+v", n1.st)
	}
	if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.BlockedReason != "rolled_back" {
		t.Fatalf("blocked %q", rel.BlockedReason)
	}
	if wm, _ := r.f.store.Watermark(ctx); wm != "" {
		t.Fatalf("watermark %q", wm)
	}
	if r.f.notes.find(NoticeRolledBack) == nil {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
}

// node_failure_policy over three machines on two platforms.
func TestDriverFailurePolicies(t *testing.T) {
	for _, tc := range []struct {
		policy, run    string
		bravo, charlie string
		applies        int
	}{
		{FailureHoldPlatform, RunHeld, TargetHeld, TargetHealthy, 2},
		{FailureHalt, RunHeld, TargetHeld, TargetHeld, 1},
		{FailureContinue, RunDoneFailures, TargetHealthy, TargetHealthy, 3},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			r := newDriverRig(t)
			ctx := context.Background()
			a := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
			a.outcome = MRolledBack
			r.node(t, "n2", "bravo", "linux-amd64", "1.4.0")
			r.node(t, "n3", "charlie", "windows-amd64", "1.4.0")
			raw, _ := json.Marshal(tc.policy)
			if _, err := r.f.db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2)`, SettingsKey,
				`{"node_failure_policy": `+string(raw)+`}`); err != nil {
				t.Fatal(err)
			}
			_, _ = r.f.svc.Tick(ctx)
			run := r.drive(t, 80)
			ts := targetsByName(t, r.f.store, run.ID)
			if run.State != tc.run || ts["alpha"].State != TargetRolledBack || ts["bravo"].State != tc.bravo ||
				ts["charlie"].State != tc.charlie || len(r.cluster.order) != tc.applies {
				t.Fatalf("run %s targets alpha %s bravo %s charlie %s applies %v", run.State, ts["alpha"].State,
					ts["bravo"].State, ts["charlie"].State, r.cluster.order)
			}
		})
	}
}

// A machine that never reports back is failed after node_health_timeout_minutes.
func TestDriverNodeHealthTimeout(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0").outcome = "silent"
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 60)
	ts := targetsByName(t, r.f.store, run.ID)
	if run.State != RunDoneFailures || ts["alpha"].State != TargetFailed || !strings.Contains(string(ts["alpha"].Error), "node_health_timeout") {
		t.Fatalf("run %s alpha %+v", run.State, ts["alpha"])
	}
}

// An offline machine is left offline by the rollout and caught up when it comes back.
func TestDriverCatchUp(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	n1 := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.cluster.offline["n1"] = true
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 30)
	if ts := targetsByName(t, r.f.store, run.ID); run.State != RunDone || ts["alpha"].State != TargetOffline {
		t.Fatalf("run %s targets %+v", run.State, ts)
	}
	r.cluster.offline["n1"] = false
	if err := r.d.Step(ctx); err != nil { // plans the catch-up
		t.Fatal(err)
	}
	cu := r.drive(t, 30)
	if cu.Trigger != TriggerCatchUp || cu.State != RunDone || n1.version != "1.5.0" || r.home.applies != 1 {
		t.Fatalf("catch-up %+v machine %s home applies %d", cu, n1.version, r.home.applies)
	}
}

// Cancel is accepted before the home switches only.
func TestDriverCancel(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	_, _ = r.f.svc.Tick(ctx)
	_ = r.d.Step(ctx) // home_preparing
	run, _ := r.f.store.ActiveRun(ctx)
	if err := r.d.Cancel(ctx, run.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := r.f.store.GetRun(ctx, run.ID)
	if got.State != RunCancelled || r.home.st.State != MAborted {
		t.Fatalf("run %s home %s", got.State, r.home.st.State)
	}

	r2 := newDriverRig(t)
	r2.home.outcome = "silent"
	_, _ = r2.f.svc.Tick(ctx)
	for i := 0; i < 6; i++ {
		_ = r2.d.Step(ctx)
	}
	run2, _ := r2.f.store.ActiveRun(ctx)
	if run2 == nil || run2.State != RunHomeApplying {
		t.Fatalf("run %+v", run2)
	}
	if err := r2.d.Cancel(ctx, run2.ID, ""); !errors.Is(err, ErrPastSwitch) {
		t.Fatalf("cancel after the switch: %v", err)
	}
}

// An automatic run restarts the home only with nobody watching; at the window's end
// it gives up (tv_busy) and is tried in the next window.
func TestDriverWaitsForTheTV(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.viewers = 2
	_, _ = r.f.svc.Tick(ctx)
	for i := 0; i < 5; i++ {
		_ = r.d.Step(ctx)
	}
	run, _ := r.f.store.ActiveRun(ctx)
	if run == nil || run.State != RunHomePrepared || run.Step != "waiting_tv" || r.home.applies != 0 {
		t.Fatalf("run %+v applies %d", run, r.home.applies)
	}
	r.f.now = r.f.now.Add(4 * time.Hour) // past 05:00 + 120 minutes
	_ = r.d.Step(ctx)
	got, _ := r.f.store.GetRun(ctx, run.ID)
	if got.State != RunFailed || !strings.Contains(string(got.Error), "tv_busy") || r.home.st.State != MAborted {
		t.Fatalf("run %+v home %s", got, r.home.st.State)
	}
	if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.BlockedReason != "" || rel.Attempts != 0 {
		t.Fatalf("a busy TV counts against the release: %+v", rel)
	}
}

// The API restarts mid-run (an update restarts it): a new driver takes the run up from
// the database.
func TestDriverResumesAcrossAnAPIRestart(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	_, _ = r.f.svc.Tick(ctx)
	for i := 0; i < 4; i++ {
		_ = r.d.Step(ctx)
		r.f.now = r.f.now.Add(time.Minute)
	}
	r.d = &Driver{Store: r.f.store, Svc: r.f.svc, Home: homeFake{r.home}, Cluster: r.cluster,
		Now: func() time.Time { return r.f.now }, Viewers: func(context.Context) int { return 0 },
		Getenv: func(string) string { return "" }}
	if run := r.drive(t, 40); run.State != RunDone || r.home.applies != 1 {
		t.Fatalf("run %s applies %d", run.State, r.home.applies)
	}
}

// A failed prepare counts against the release (blocked at three); a verification or a
// rehearsal failure blocks it at once.
func TestDriverPrepareFailures(t *testing.T) {
	for _, tc := range []struct{ code, blocked string }{{"verify_identity", "verify"}, {"rehearsal_failed", "rehearsal"},
		{"download_failed", ""}} {
		t.Run(tc.code, func(t *testing.T) {
			r := newDriverRig(t)
			ctx := context.Background()
			r.home.prepFail = tc.code
			_, _ = r.f.svc.Tick(ctx)
			run := r.drive(t, 10)
			rel, _ := r.f.store.Get(ctx, "1.5.0")
			if run.State != RunFailed || rel.BlockedReason != tc.blocked {
				t.Fatalf("run %s blocked %q attempts %d", run.State, rel.BlockedReason, rel.Attempts)
			}
			if tc.blocked == "" && rel.Attempts != 1 {
				t.Fatalf("attempts %d", rel.Attempts)
			}
		})
	}
}

// A machine ahead of the home is skipped, never downgraded; a worker-only one is
// reported.
func TestDriverSkewRules(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	ahead := r.node(t, "n1", "alpha", "linux-amd64", "1.6.0")
	if _, err := r.f.db.Exec(`INSERT INTO cluster_nodes (id, name, state, platform, version, full_node) VALUES
		('n9', 'worker', 'approved', 'linux-amd64', '1.4.0', 0)`); err != nil {
		t.Fatal(err)
	}
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 30)
	ts := targetsByName(t, r.f.store, run.ID)
	if run.State != RunDone || ts["alpha"].State != TargetSkipped || ts["worker"].State != TargetWorkerOnly || ahead.st != nil {
		t.Fatalf("run %s targets %+v", run.State, ts)
	}
	ms := r.d.Machines(ctx, Machine{ID: "home", Role: "home", Version: "1.5.0"})
	states := map[string]string{}
	for _, m := range ms {
		states[m.Name] = m.State
	}
	if states["alpha"] != "ahead_of_home" || states["worker"] != "worker_only" {
		t.Fatalf("machines %v", fmt.Sprint(states))
	}
}
