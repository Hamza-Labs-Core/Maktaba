package updates

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// The owner's decision (2026-10-28): when only the TV change's check fails, the update
// installs, the TV stays on the streaming software it ran, the release is not blocked,
// and the admin retries or drops the change from the Updates page.

// fakeTVChanges is the TV change controller as the run's held TV needs it.
type fakeTVChanges struct {
	mu       sync.Mutex
	st       tvchange.AutoUpdateStatus
	err      error
	retried  []string
	dropped  []string
	actors   []string
	replaced []string
	begun    []string
}

func (f *fakeTVChanges) AutoUpdate(_ context.Context, runID string) (tvchange.AutoUpdateStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.st
	st.RunID = runID
	return st, nil
}

func (f *fakeTVChanges) act(runID string, actor *string, to string, list *[]string) (tvchange.AutoUpdateStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return tvchange.AutoUpdateStatus{}, f.err
	}
	if f.st.Outcome != tvchange.AutoOutcomeHeld {
		return tvchange.AutoUpdateStatus{}, tvchange.ErrNotHeld
	}
	*list = append(*list, runID)
	if actor != nil {
		f.actors = append(f.actors, *actor)
	}
	f.st.Outcome = to
	if to == tvchange.AutoOutcomeCancelled {
		f.st.EndReason = tvchange.EndDropped
	}
	return f.st, nil
}

func (f *fakeTVChanges) RetryAutoUpdate(_ context.Context, runID string, actor *string) (tvchange.AutoUpdateStatus, error) {
	return f.act(runID, actor, tvchange.AutoOutcomeQueued, &f.retried)
}

func (f *fakeTVChanges) DropAutoUpdate(_ context.Context, runID string, actor *string) (tvchange.AutoUpdateStatus, error) {
	return f.act(runID, actor, tvchange.AutoOutcomeCancelled, &f.dropped)
}

func (f *fakeTVChanges) BeginAutoUpdate(_ context.Context, runID, version, sha256 string) (tvchange.AutoUpdateStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.begun = append(f.begun, runID+" "+version+" "+sha256)
	f.st = tvchange.AutoUpdateStatus{RunID: runID, Outcome: tvchange.AutoOutcomeQueued, ChangeID: "ch-9", Number: 9}
	return f.st, nil
}

func (f *fakeTVChanges) ReplaceOlderAutoUpdates(_ context.Context, runID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaced = append(f.replaced, runID)
	return nil
}

func heldStatus() tvchange.AutoUpdateStatus {
	return tvchange.AutoUpdateStatus{Outcome: tvchange.AutoOutcomeHeld, ChangeID: "ch-7", Number: 7, State: "draft",
		Reason: "verify_failed", Failed: []string{"stream_plays"}, LiveStreaming: "v1.4.0",
		FailedChecks: []tvchange.Check{{Check: "stream_plays", ChannelID: "c1", Result: tvchange.ResultFail, Value: "no video"}}}
}

// heldRig is a run whose home committed with its TV held.
func heldRig(t *testing.T) (*driverRig, *fakeTVChanges, Run) {
	t.Helper()
	r := newDriverRig(t)
	tv := &fakeTVChanges{st: heldStatus()}
	r.d.TV = tv
	r.home.tv = &MachineTV{ChangeID: "ch-7", Number: 7, Outcome: TVHeld, LiveStreaming: "v1.4.0",
		Failed: []string{"stream_plays"}}
	_, _ = r.f.svc.Tick(context.Background())
	return r, tv, r.drive(t, 12)
}

// Preview fails: the run succeeds, the TV held, the version not blocked (nor counted),
// the admin told, no rollback; the run's detail keeps the held TV, and the release that
// committed replaces any older held change.
func TestDriverTVHeldCommitsTheRelease(t *testing.T) {
	t.Parallel()
	r, tv, run := heldRig(t)
	ctx := context.Background()
	if run.State != RunDone || strings.Contains(string(run.Error), "code") {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.BlockedReason != "" || rel.Attempts != 0 {
		t.Fatalf("a held TV counts against the release: %+v", rel)
	}
	if wm, _ := r.f.store.Watermark(ctx); wm != "1.5.0" {
		t.Fatalf("watermark %q", wm)
	}
	if r.f.notes.find(NoticeRolledBack) != nil {
		t.Fatalf("a rolled-back notice for a held TV: %v", r.f.notes.kinds())
	}
	n := r.f.notes.find(NoticeTVHeld)
	if n == nil || n.p.Params["held_on"] != "v1.4.0" || n.p.Params["checks"] != "stream_plays" ||
		n.p.Params["version"] != "1.5.0" || n.p.Link != "/admin/updates" {
		t.Fatalf("notice %+v (kinds %v)", n, r.f.notes.kinds())
	}
	_, det, err := r.f.store.RunDetailOf(ctx, run.ID)
	if err != nil || det.TV == nil || det.TV.Outcome != TVHeld || det.TV.HeldOn != "v1.4.0" || det.TV.ChangeID != "ch-7" {
		t.Fatalf("detail tv %+v (%v)", det.TV, err)
	}
	if len(tv.replaced) != 1 || tv.replaced[0] != run.ID {
		t.Fatalf("replaced %v", tv.replaced)
	}
	if !contains(auditActions(t, r.f), "tv_held") {
		t.Fatalf("audit %v", auditActions(t, r.f))
	}
	// the overview says the TV is held, with the report's summary
	held := r.d.TVHeld(ctx)
	if held == nil || held.RunID != run.ID || held.State != TVStateHeld || held.HeldOn != "v1.4.0" ||
		len(held.Checks) != 1 || held.Checks[0].Value != "no video" {
		t.Fatalf("tv held %+v", held)
	}
}

// A release that commits with its TV live says nothing held, and is not noticed as held.
func TestDriverTVLiveIsNotHeld(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	tv := &fakeTVChanges{st: tvchange.AutoUpdateStatus{Outcome: tvchange.AutoOutcomeLive}}
	r.d.TV = tv
	r.home.tv = &MachineTV{ChangeID: "ch-7", Outcome: "live"}
	_, _ = r.f.svc.Tick(context.Background())
	run := r.drive(t, 12)
	_, det, _ := r.f.store.RunDetailOf(context.Background(), run.ID)
	if run.State != RunDone || det.TV != nil || r.f.notes.find(NoticeTVHeld) != nil || r.d.TVHeld(context.Background()) != nil {
		t.Fatalf("run %s tv %+v notices %v", run.State, det.TV, r.f.notes.kinds())
	}
}

// The outage of 2026-10-28: a home whose release passed its watch while its TV change
// waited for an approval commits with the TV pending. The run is done, nothing blocked
// or counted, nobody warned of a held TV; its detail says the TV was pending, and the
// overview shows no held TV.
func TestDriverTVPendingCommitsTheRelease(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	tv := &fakeTVChanges{st: tvchange.AutoUpdateStatus{Outcome: tvchange.AutoOutcomeInProgress, ChangeID: "ch-57",
		Number: 57, State: "ready"}}
	r.d.TV = tv
	r.home.tv = &MachineTV{ChangeID: "ch-57", Number: 57, Outcome: TVPending, LiveStreaming: "v1.4.0"}
	_, _ = r.f.svc.Tick(context.Background())
	run := r.drive(t, 12)
	ctx := context.Background()
	if run.State != RunDone {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.BlockedReason != "" || rel.Attempts != 0 {
		t.Fatalf("a pending TV counts against the release: %+v", rel)
	}
	if r.f.notes.find(NoticeTVHeld) != nil || r.f.notes.find(NoticeRolledBack) != nil {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
	_, det, err := r.f.store.RunDetailOf(ctx, run.ID)
	if err != nil || det.TV == nil || det.TV.Outcome != TVPending || det.TV.Number != 57 {
		t.Fatalf("detail tv %+v (%v)", det.TV, err)
	}
	if !contains(auditActions(t, r.f), "tv_pending") {
		t.Fatalf("audit %v", auditActions(t, r.f))
	}
	if held := r.d.TVHeld(ctx); held != nil {
		t.Fatalf("a pending TV shown held: %+v", held)
	}
}

// The review of #433: a home whose TV change could not be had at all commits with the TV
// held (no change behind it, check no_change). The overview shows it held; Retry makes the
// change with the binary the home said (then it goes on as any), and Drop leaves the TV as
// it is with nothing to cancel.
func TestDriverTVHeldWithNoChange(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"retry", "drop"} {
		t.Run(action, func(t *testing.T) {
			r := newDriverRig(t)
			tv := &fakeTVChanges{st: tvchange.AutoUpdateStatus{Outcome: tvchange.AutoOutcomeNone}}
			r.d.TV = tv
			r.home.tv = &MachineTV{Outcome: TVHeld, Failed: []string{TVNoChange}, LiveStreaming: "v1.4.0",
				StreamingVersion: "v1.5.0", StreamingSHA256: "ab12"}
			_, _ = r.f.svc.Tick(context.Background())
			run := r.drive(t, 12)
			ctx := context.Background()
			if run.State != RunDone {
				t.Fatalf("run %s %s", run.State, run.Error)
			}
			held := r.d.TVHeld(ctx)
			if held == nil || held.RunID != run.ID || held.State != TVStateHeld || held.ChangeID != "" {
				t.Fatalf("tv held %+v", held)
			}
			got, err := r.d.TVAction(ctx, run.ID, action, "admin-1")
			if err != nil {
				t.Fatalf("%s: %v", action, err)
			}
			_, det, _ := r.f.store.RunDetailOf(ctx, run.ID)
			switch action {
			case "retry":
				if len(tv.begun) != 1 || tv.begun[0] != run.ID+" v1.5.0 ab12" || got.State != TVStateRetrying ||
					det.TV.ChangeID != "ch-9" {
					t.Fatalf("retry: begun %v state %q detail %+v", tv.begun, got.State, det.TV)
				}
			case "drop":
				if len(tv.begun) != 0 || got.State != TVStateDropped || !det.TV.Dropped || r.d.TVHeld(ctx) != nil {
					t.Fatalf("drop: begun %v state %q detail %+v", tv.begun, got.State, det.TV)
				}
			}
		})
	}
}

// Retry and Drop: admins only (403 for anyone else, the controller untouched); a retry
// while another run is under way is refused; a run whose TV is not held answers 409.
func TestTVActionRetryAndDrop(t *testing.T) {
	t.Parallel()
	r, tv, run := heldRig(t)
	ctx := context.Background()
	h := &Handler{Svc: r.f.svc, Driver: r.d}
	mux := chi.NewRouter()
	h.Mount(mux)
	base := "/api/cluster/updates/runs/" + run.ID + "/tv/"

	for _, action := range []string{"retry", "drop"} {
		if code, _ := do(t, mux, http.MethodPost, base+action, nil, false); code != http.StatusForbidden {
			t.Fatalf("%s by a non-admin: %d", action, code)
		}
	}
	if len(tv.retried)+len(tv.dropped) != 0 {
		t.Fatalf("a non-admin reached the controller: %v %v", tv.retried, tv.dropped)
	}
	if code, _ := do(t, mux, http.MethodPost, base+"nope", nil, true); code != http.StatusNotFound {
		t.Fatalf("unknown action: %d", code)
	}
	if code, _ := do(t, mux, http.MethodPost, "/api/cluster/updates/runs/no-such-run/tv/retry", nil, true); code != http.StatusNotFound {
		t.Fatalf("unknown run: %d", code)
	}

	code, out := do(t, mux, http.MethodPost, base+"retry", nil, true)
	if code != http.StatusOK || out["state"] != TVStateRetrying || len(tv.retried) != 1 || tv.actors[0] != adminID {
		t.Fatalf("retry: %d %v (retried %v actors %v)", code, out, tv.retried, tv.actors)
	}
	// no longer held: a second retry, and a drop, are refused
	if code, out := do(t, mux, http.MethodPost, base+"drop", nil, true); code != http.StatusConflict || out["code"] != "tv_not_held" {
		t.Fatalf("drop after a retry: %d %v", code, out)
	}
	// the view says what it is now
	_, vout := do(t, mux, http.MethodGet, "/api/cluster/updates/runs/"+run.ID, nil, true)
	if tvd, _ := vout["detail"].(map[string]any)["tv"].(map[string]any); tvd["state"] != TVStateRetrying || tvd["outcome"] != TVHeld {
		t.Fatalf("view tv %v", vout["detail"])
	}

	// held again (its check failed again): dropped
	tv.mu.Lock()
	tv.st = heldStatus()
	tv.mu.Unlock()
	code, out = do(t, mux, http.MethodPost, base+"drop", nil, true)
	if code != http.StatusOK || out["state"] != TVStateDropped || len(tv.dropped) != 1 {
		t.Fatalf("drop: %d %v", code, out)
	}
	if acts := strings.Join(auditActions(t, r.f), ","); !strings.Contains(acts, "tv_retry") || !strings.Contains(acts, "tv_drop") {
		t.Fatalf("audit %s", acts)
	}
	if r.d.TVHeld(ctx) != nil {
		t.Fatal("a dropped TV still reads held")
	}

	// a run under way moves the TV itself: no retry meanwhile
	tv.mu.Lock()
	tv.st = heldStatus()
	tv.mu.Unlock()
	if _, err := r.f.store.InsertRun(ctx, "1.5.0", "1.5.0", TriggerManual, adminID); err != nil {
		t.Fatal(err)
	}
	if code, out := do(t, mux, http.MethodPost, base+"retry", nil, true); code != http.StatusConflict || out["code"] != BlockActiveRun {
		t.Fatalf("retry during a run: %d %v", code, out)
	}
}

// A run whose TV was not held has nothing to retry or drop; an API without the
// controller answers 503.
func TestTVActionNotHeld(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	r.d.TV = &fakeTVChanges{st: tvchange.AutoUpdateStatus{Outcome: tvchange.AutoOutcomeLive}}
	_, _ = r.f.svc.Tick(context.Background())
	run := r.drive(t, 12)
	h := &Handler{Svc: r.f.svc, Driver: r.d}
	mux := chi.NewRouter()
	h.Mount(mux)
	if code, out := do(t, mux, http.MethodPost, "/api/cluster/updates/runs/"+run.ID+"/tv/retry", nil, true); code != http.StatusConflict ||
		out["code"] != "tv_not_held" {
		t.Fatalf("not held: %d %v", code, out)
	}
	r.d.TV = nil
	if code, _ := do(t, mux, http.MethodPost, "/api/cluster/updates/runs/"+run.ID+"/tv/drop", nil, true); code != http.StatusServiceUnavailable {
		t.Fatalf("no controller: %d", code)
	}
	// and the overview of an API without one says nothing held
	_, ov := do(t, mux, http.MethodGet, "/api/cluster/updates", nil, true)
	if _, ok := ov["tv_held"]; ok {
		raw, _ := json.Marshal(ov["tv_held"])
		t.Fatalf("tv_held %s", raw)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
