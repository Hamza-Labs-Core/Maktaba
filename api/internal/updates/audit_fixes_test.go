package updates

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// runsOf counts version's runs of a trigger.
func runsOf(t *testing.T, s *Store, version, trigger string) []Run {
	t.Helper()
	all, err := s.Runs(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	var out []Run
	for _, r := range all {
		if r.Version == version && r.Trigger == trigger {
			out = append(out, r)
		}
	}
	return out
}

// minutes runs the scheduler and the driver for n minutes of the clock.
func (r *driverRig) minutes(t *testing.T, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		_, _ = r.f.svc.Tick(ctx)
		for k := 0; k < 3; k++ {
			_ = r.d.Step(ctx)
		}
		r.f.now = r.f.now.Add(time.Minute)
	}
}

// H1 (audit): a failed automatic run is not started again every minute. A transient
// failure is tried again at once, then after retryBackoff, then in the next window, and
// a window counts one attempt (three windows block the release, not three minutes).
func TestFailedRunRetriedWithBackoff(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.prepFail = "download_failed"
	r.minutes(t, 25) // 03:10-03:35: at once, then after the pause, then nothing more
	if n := len(runsOf(t, r.f.store, "1.5.0", TriggerAuto)); n != triesPerWindow {
		t.Fatalf("runs in one window: %d, want %d", n, triesPerWindow)
	}
	rel, _ := r.f.store.Get(ctx, "1.5.0")
	if rel.Attempts != 1 || rel.BlockedReason != "" {
		t.Fatalf("one window counts one attempt: %+v", rel)
	}
	if lt := r.f.svc.LastTick(); lt == nil || lt.Reason != WaitRetryWindow {
		t.Fatalf("last tick: %+v", lt)
	}
	// the second try came at once, the third after the pause
	runs := runsOf(t, r.f.store, "1.5.0", TriggerAuto)
	if gap := runs[0].StartedAt.Sub(*runs[1].FinishedAt); gap < retryBackoff {
		t.Fatalf("the third try came %s after the second", gap)
	}
	// the next two nights: one attempt each, then blocked
	for night := 0; night < 2; night++ {
		r.f.now = time.Date(2026, 10, 23+night, 3, 10, 0, 0, r.f.loc)
		r.minutes(t, 5)
	}
	if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.Attempts != MaxAttempts || rel.BlockedReason != "attempts" {
		t.Fatalf("after three windows: %+v", rel)
	}
}

// H1 (audit): a failure a person must mend (the release's app missing for this platform,
// an app built locally) starts no new run in its window and counts nothing; its notice
// is one per release and reason; the next window tries once more.
func TestNeedsPersonRunsOncePerWindow(t *testing.T) {
	for _, code := range []string{"no_app_update", "local_build", "app_management", "translocated", "not_writable"} {
		t.Run(code, func(t *testing.T) {
			r := newDriverRig(t)
			ctx := context.Background()
			r.home.prepFail = code
			r.minutes(t, 10)
			if n := len(runsOf(t, r.f.store, "1.5.0", TriggerAuto)); n != 1 {
				t.Fatalf("runs in one window: %d", n)
			}
			if lt := r.f.svc.LastTick(); lt == nil || lt.Reason != WaitNeedsPerson {
				t.Fatalf("last tick: %+v", lt)
			}
			n := r.f.notes.find(NoticeNeedsPerson)
			if n == nil || n.p.Subject != "release:1.5.0:home:"+code || n.p.Every != notifyOnce {
				t.Fatalf("notice: %+v", n)
			}
			r.f.now = time.Date(2026, 10, 23, 3, 10, 0, 0, r.f.loc)
			r.minutes(t, 5)
			if n := len(runsOf(t, r.f.store, "1.5.0", TriggerAuto)); n != 2 {
				t.Fatalf("runs after the next window: %d", n)
			}
			if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.Attempts != 0 || rel.BlockedReason != "" {
				t.Fatalf("a person's failure counted: %+v", rel)
			}
		})
	}
}

// M5 (audit): only a refused signature or identity and migrations that fail on the copy
// block a release; a rehearsal that could not run is an attempt, an expired trust root a
// person's.
func TestWhatBlocksARelease(t *testing.T) {
	for code, want := range map[string]struct {
		blocked  string
		attempts int
	}{
		"rehearsal_failed":          {"rehearsal", 0},
		"verify_signature":          {"verify", 0},
		"rehearsal_dump":            {"", 1},
		"rehearsal_scratch":         {"", 1},
		"rehearsal_restore":         {"", 1},
		"verify_trust_root_expired": {"", 0},
		// a download that failed is an attempt, never a verification block
		"download_failed": {"", 1},
		// an older build than the home's release (an rc tagged after its release): set
		// aside quietly, no attempt
		"older_build": {"older_build", 0},
	} {
		t.Run(code, func(t *testing.T) {
			r := newDriverRig(t)
			r.home.prepFail = code
			r.minutes(t, 3)
			rel, _ := r.f.store.Get(context.Background(), "1.5.0")
			if rel.BlockedReason != want.blocked || rel.Attempts != want.attempts {
				t.Fatalf("%s: blocked %q attempts %d", code, rel.BlockedReason, rel.Attempts)
			}
		})
	}
}

// One "never go back" rule: a node on a later build of the release's line (an rc of it
// tagged after it, as the catalog knows) is skipped at the plan, as one ahead is; one the
// catalog does not know that refuses the release as an older build is skipped too, with no
// notice and no hold on the others.
func TestANodeOnALaterBuildIsSkipped(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	seed(t, r.f.store, rel("1.5.0-rc.7", func(rr *Release) {
		rr.Manifest.Published = rr.Manifest.Published.Add(time.Hour) // tagged after 1.5.0
	}))
	r.node(t, "n1", "alpha", "linux-amd64", "1.5.0-rc.7")
	r.node(t, "n2", "bravo", "linux-amd64", "1.5.0-rc.9").prepFail = KindOlderBuild
	r.node(t, "n3", "charlie", "linux-amd64", "1.4.0")
	if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" {
		t.Fatalf("tick: %+v %v", d, err)
	}
	run := r.drive(t, 60)
	if run.State != RunDone {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	ts := targetsByName(t, r.f.store, run.ID)
	why := func(x Target) string {
		var d struct {
			Why string `json:"why"`
		}
		_ = json.Unmarshal(x.Detail, &d)
		return d.Why
	}
	if ts["alpha"].State != TargetSkipped || why(ts["alpha"]) != KindOlderBuild {
		t.Fatalf("alpha (rc.7, a later build): %+v", ts["alpha"])
	}
	if ts["bravo"].State != TargetSkipped || why(ts["bravo"]) != KindOlderBuild {
		t.Fatalf("bravo (refused it as an older build): %+v", ts["bravo"])
	}
	if ts["charlie"].State != TargetHealthy {
		t.Fatalf("charlie: %+v", ts["charlie"])
	}
	if n := r.f.notes.find(NoticeNodeFailed); n != nil {
		t.Fatalf("a notice for an older build: %+v", n)
	}
}

// H2 (audit): an admin retries a blocked release: Update now naming it unblocks and
// installs it (a withdrawn one stays refused), and Unblock lets the next window take it.
func TestRetryABlockedRelease(t *testing.T) {
	enableAutoInstall(t)
	f := newFixture(t, sqliteDB(t))
	ctx := context.Background()
	seed(t, f.store, rel("1.5.0"))
	_ = f.store.Block(ctx, "1.5.0", "rolled_back")
	run, err := f.svc.RunNow(ctx, "00000000-0000-0000-0000-000000000001", "1.5.0")
	if err != nil || run.Version != "1.5.0" {
		t.Fatalf("Update now naming a blocked release: %+v %v", run, err)
	}
	if rel, _ := f.store.Get(ctx, "1.5.0"); rel.BlockedReason != "" {
		t.Fatalf("still blocked: %+v", rel)
	}
	if acts := strings.Join(auditActions(t, f), ","); !strings.Contains(acts, "release_unblocked") {
		t.Fatalf("audit: %s", acts)
	}

	f2 := newFixture(t, sqliteDB(t))
	seed(t, f2.store, rel("1.5.0"))
	_ = f2.store.Block(ctx, "1.5.0", "attempts")
	h := newRouter(t, f2)
	if code, body := do(t, h, http.MethodPost, "/api/cluster/updates/unblock", map[string]any{"version": "1.5.0"}, true); code != http.StatusOK {
		t.Fatalf("unblock: %d %v", code, body)
	}
	if code, body := do(t, h, http.MethodPost, "/api/cluster/updates/unblock", map[string]any{"version": "1.5.0"}, true); code != http.StatusConflict || body["code"] != "not_blocked" {
		t.Fatalf("unblock again: %d %v", code, body)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/cluster/updates/unblock", map[string]any{"version": "1.5.0"}, false); code != http.StatusForbidden {
		t.Fatalf("not an admin: %d", code)
	}
	// withdrawn: refused
	_ = f2.store.Block(ctx, "1.5.0", "attempts")
	if _, err := f2.db.Exec(`UPDATE release_catalog SET withdrawn_at = '2026-10-22T00:00:00Z' WHERE version = '1.5.0'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.svc.RunNow(ctx, "00000000-0000-0000-0000-000000000001", "1.5.0"); err == nil {
		t.Fatal("a withdrawn release installed")
	}
}

// M1 (audit): a machine whose part needed a person (or failed before its switch) is
// caught up once it changed (its install converted) or catchUpRetry later; one the
// release rolled back on stays (TestCatchUpRespectsRollbacksAndTheWindow).
func TestMendedMachineCaughtUp(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	n := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	n.prepFail = "not_self_managed"
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 40)
	if ts := targetsByName(t, r.f.store, run.ID); ts["alpha"].State != TargetNeedsPerson {
		t.Fatalf("alpha %s", ts["alpha"].State)
	}
	n.mu.Lock()
	n.st, n.prepFail = nil, ""
	n.mu.Unlock()
	_ = r.d.Step(ctx)
	if cu, _ := r.f.store.ActiveRun(ctx); cu != nil {
		t.Fatalf("caught up before anything changed: %+v", cu)
	}
	n.mu.Lock()
	n.installer = "runtime" // converted: a self-managed install now
	n.mu.Unlock()
	_ = r.d.Step(ctx)
	if cu, _ := r.f.store.ActiveRun(ctx); cu == nil || cu.Trigger != TriggerCatchUp {
		t.Fatalf("not caught up once mended: %+v", cu)
	}
}

// M2 (audit): with catch_up "immediate" (the default) a machine behind is caught up at
// once, outside the window, and a home installed by hand raises the cluster's release:
// its machines show behind and are caught up to it.
func TestCatchUpImmediateBehindAHandInstalledHome(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.f.svc.Current = "1.5.0" // the home installed 1.5.0 by hand: no run, no watermark
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.f.now = time.Date(2026, 10, 22, 14, 0, 0, 0, r.f.loc)
	ms := r.d.Machines(ctx, Machine{ID: "home", Role: "home", Version: "1.5.0"})
	if len(ms) != 2 || ms[1].State != "behind" {
		t.Fatalf("machines: %+v", ms)
	}
	_ = r.d.Step(ctx)
	cu, _ := r.f.store.ActiveRun(ctx)
	if cu == nil || cu.Trigger != TriggerCatchUp || cu.Version != "1.5.0" {
		t.Fatalf("no catch-up at 14:00 with catch_up immediate: %+v", cu)
	}
	if wm, _ := r.f.store.Watermark(ctx); wm != "1.5.0" {
		t.Fatalf("watermark %q", wm)
	}
}

// M3 (audit): a machine is drained for its own apply and undrained once healthy.
func TestNodeDrainedForItsApply(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	n := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	drained := func() bool {
		var v boolScan
		_ = r.f.db.QueryRow(`SELECT drain FROM cluster_nodes WHERE id = 'n1'`).Scan(&v)
		return v.v
	}
	var atApply *bool
	n.onApply = func() {
		d := drained()
		atApply = &d
	}
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 60)
	if run.State != RunDone || atApply == nil || !*atApply {
		t.Fatalf("run %s; drained at its apply: %v", run.State, atApply)
	}
	if drained() {
		t.Fatal("still drained once healthy")
	}
}

// M6 (audit): a release a gate (the TV watched) held for stuckWindows windows in a row is
// told once and shown, for an admin to install it now.
func TestStuckReleaseEscalates(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.viewers = 5
	for night := 0; night < stuckWindows; night++ {
		r.f.now = time.Date(2026, 10, 22+night, 3, 10, 0, 0, r.f.loc)
		_, _ = r.f.svc.Tick(ctx)
		for i := 0; i < 5; i++ {
			_ = r.d.Step(ctx)
		}
		r.f.now = r.f.now.Add(4 * time.Hour) // past the window and its overrun
		_ = r.d.Step(ctx)
	}
	n := r.f.notes.find(NoticeStuck)
	if n == nil || n.p.Params["code"] != "tv_busy" || n.p.Every != notifyOnce {
		t.Fatalf("stuck notice: %+v (%v)", n, r.f.notes.kinds())
	}
	o, err := r.f.svc.Overview(ctx)
	if err != nil || o.Stuck == nil || o.Stuck.Windows != stuckWindows || o.Stuck.Code != "tv_busy" {
		t.Fatalf("overview stuck: %+v %v", o.Stuck, err)
	}
}

// L5 (audit): the overview says why a due release does not start (the scheduler's last
// decision), and a machine the release's schema pauses is told once.
func TestLastTickAndPausedMachines(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	m15 := Manifest{V: 1, Version: "1.5.0", Tag: "v1.5.0", Channel: ChannelStable, SchemaRev: 150, MinNodeSchema: 150,
		Published: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	if err := r.f.store.SetVerified(ctx, "1.5.0", Verified{Manifest: m15, ChecksumsSHA256: "c"}); err != nil {
		t.Fatal(err)
	}
	seed(t, r.f.store, rel("1.4.0", func(x *Release) { x.Manifest.SchemaRev = 149 }))
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.f.svc.Blockers = append(r.f.svc.Blockers, BlockerFunc(func(context.Context) (string, error) { return BlockRecentCrash, nil }))
	_, _ = r.f.svc.Tick(ctx)
	o, _ := r.f.svc.Overview(ctx)
	if o.LastTick == nil || o.LastTick.Action != "blocked" || o.LastTick.Reason != BlockRecentCrash {
		t.Fatalf("last tick: %+v", o.LastTick)
	}
	r.f.svc.Blockers = r.f.svc.Blockers[:len(r.f.svc.Blockers)-1]
	_, _ = r.f.svc.Tick(ctx)
	r.drive(t, 60)
	n := r.f.notes.find(NoticeNodePausedSchema)
	if n == nil || n.p.Params["machine"] != "alpha" || n.p.Params["min_node_schema"] != 150 {
		t.Fatalf("paused notice: %+v (%v)", n, r.f.notes.kinds())
	}
}
