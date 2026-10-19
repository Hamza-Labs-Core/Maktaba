package tvchange

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// fakeStaging is the front door's staging as the controller sees it.
type fakeStaging struct {
	mu      sync.Mutex
	err     error
	view    func(plan StagingPlan) *StagingView
	plans   []StagingPlan
	stopped []string
	active  *StagingPlan
}

func (f *fakeStaging) Start(_ context.Context, p StagingPlan) (*StagingView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.plans = append(f.plans, p)
	f.active = &p
	return f.view(p), nil
}

func (f *fakeStaging) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	f.active = nil
	return nil
}

func (f *fakeStaging) View(context.Context) (*StagingView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.active == nil {
		return &StagingView{}, nil
	}
	return f.view(*f.active), nil
}

type svWorld struct {
	t   *testing.T
	db  *sql.DB
	ctl *Controller
	fs  *fakeStaging
	now time.Time
	ch  string
	// the sample the fake front door reports
	results []ProbeResult
	sampled time.Time
}

func (w *svWorld) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.db.Exec(q, args...); err != nil {
		w.t.Fatalf("%s: %v", q, err)
	}
}

func (w *svWorld) ts(t time.Time) any { return tsParam(w.db, t) }

func newSVWorld(t *testing.T, db *sql.DB) *svWorld {
	w := &svWorld{t: t, db: db, now: time.Date(2026, 6, 15, 20, 10, 0, 0, time.UTC), ch: uuid.NewString()}
	w.ctl = NewController(db, nil)
	w.ctl.Enqueue = func(context.Context, string, map[string]any, string) error { return nil }
	w.ctl.Now = func() time.Time { return w.now }
	w.ctl.Store.Now = w.ctl.Now
	w.fs = &fakeStaging{}
	w.ctl.Staging = w.fs
	w.sampled = w.now
	w.fs.view = func(p StagingPlan) *StagingView {
		at := w.sampled
		blockStart := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
		reached := at
		st := &StationInfo{ID: w.ch, Encoder: "software", Reached: &reached,
			Block: &OnAir{Seq: 1, Kind: "program", StartAt: blockStart, EndAt: blockStart.Add(time.Hour), Title: "Tafsir 1"}}
		return &StagingView{Active: true, Plan: &p, Slot: "amber", Healthy: true,
			Status: &SlotStatus{Slot: "amber", Role: "staging", Release: p.ReleaseID, Version: "v2", PID: 42,
				Stations: []string{w.ch}},
			Budget: 2, Capacity: 4, LiveOnAir: 1, StandbyWarm: 1,
			Warm: []WarmEntry{{Channel: w.ch, Why: "probe", Since: w.now.Add(-time.Minute)}},
			Samples: map[string]*ChannelSample{w.ch: {Channel: w.ch, At: at, Station: st, StationAt: at,
				Results: w.results, FirstSegmentAfter: 2.5}}}
	}
	w.exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv_staging', '{"verify_soak_seconds":20}')`)
	w.exec(`INSERT INTO channels (id, number, name, slug, mode, logo_path) VALUES ($1, 101, 'Quran', 'quran', 'shuffle', '/l.png')`, w.ch)
	at := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
	for gen := int64(0); gen <= 1; gen++ {
		for i := 0; i < 26; i++ {
			s := at.Add(time.Duration(i) * time.Hour)
			w.exec(`INSERT INTO channel_programs (id, channel_id, seq, kind, start_at, end_at, source_duration, title_snapshot, sched_gen)
				VALUES ($1, $2, $3, 'program', $4, $5, 3600000, $6, $7)`, uuid.NewString(), w.ch, i+1, w.ts(s),
				w.ts(s.Add(time.Hour)), fmt.Sprintf(`{"title": "Tafsir %d"}`, i+1), gen)
		}
	}
	return w
}

func (w *svWorld) staged(version string) *Change {
	w.t.Helper()
	rel := uuid.NewString()
	w.exec(`INSERT INTO tv_releases (id, digest, sched_gen, streaming_version) VALUES ($1, $2, $3, $4)`,
		rel, uuid.NewString(), 1, version)
	w.exec(`INSERT INTO tv_release_channels (release_id, channel_id, def) VALUES ($1, $2, $3)`, rel, w.ch,
		`{"number":101,"name":"Quran","slug":"quran","enabled":true,"logo_path":"/l.png"}`)
	c := &Change{State: StateStaged, Title: "t", Source: map[string]any{"kind": SourceManual}}
	if err := w.ctl.Store.Insert(context.Background(), w.db, c); err != nil {
		w.t.Fatal(err)
	}
	if err := w.ctl.Store.Set(context.Background(), w.db, c.ID, map[string]any{"release_id": rel,
		"kinds": []string{"lineup"}}); err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *svWorld) tick() {
	w.t.Helper()
	if err := w.ctl.Tick(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

func (w *svWorld) get(id string) *Change {
	w.t.Helper()
	c, err := w.ctl.Store.Get(context.Background(), w.db, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

func passing() []ProbeResult {
	return []ProbeResult{{Check: "channel.first_segment", Result: "pass", Value: "5 segments listed"},
		{Check: "ts.conformity", Result: "pass", Value: "ok"}, {Check: "splice.align", Result: "pass", Value: "ok"}}
}

func reportChecks(c *Change) map[string]string {
	out := map[string]string{}
	xs, _ := c.VerifyReport["checks"].([]any)
	for _, x := range xs {
		m, _ := x.(map[string]any)
		out[fmt.Sprint(m["check"])] = fmt.Sprint(m["result"])
	}
	return out
}

// The controller on a staging slot runtime: staged starts the slot and moves to
// verifying once it is healthy on the release; the samples soak; ready after
// verify_soak_seconds with the stream checks in the report.
func TestStreamVerificationSoaksToReady(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newSVWorld(t, be.DB)
			w.results = passing()
			c := w.staged("v2")
			w.tick()
			if got := w.get(c.ID); got.State != StateVerifying {
				t.Fatalf("after the slot came up: %s", got.State)
			}
			if p := w.fs.plans[0]; p.ReleaseID == "" || p.Version != "v2" || p.Probe != w.ch || p.ChangeID != c.ID {
				t.Fatalf("plan %+v", p)
			}
			for i := 0; i < 3; i++ {
				w.now = w.now.Add(10 * time.Second)
				w.sampled = w.now
				w.tick()
			}
			got := w.get(c.ID)
			if got.State != StateReady {
				t.Fatalf("after the soak: %s %v", got.State, got.VerifyReport)
			}
			ch := reportChecks(got)
			for _, k := range []string{"slot.health", "channel.first_segment", "ts.conformity", "splice.align",
				"channel.block", "channel.position", "guide.vs_stream", "encoders.budget", "guide.coverage",
				"schedule.boundary", "cloudflare.ingest"} {
				if ch[k] == "" {
					t.Errorf("no %s in %v", k, ch)
				}
			}
			if ch["slot.health"] != ResultPass || ch["channel.block"] != ResultPass || ch["guide.vs_stream"] != ResultPass {
				t.Errorf("checks %v", ch)
			}
			if sum, _ := got.VerifyReport["summary"].(map[string]any); sum == nil {
				t.Errorf("no summary: %v", got.VerifyReport)
			}
			// AC9: a ready change re-checks every minute; a slot gone → verifying
			w.fs.view = func(p StagingPlan) *StagingView {
				return &StagingView{Active: true, Plan: &p, Slot: "amber", Healthy: false}
			}
			w.now = w.now.Add(61 * time.Second)
			w.tick()
			if got := w.get(c.ID); got.State != StateVerifying {
				t.Fatalf("recheck with the slot down: %s", got.State)
			}
		})
	}
}

// A check failing on two samples in a row fails the change with the reasons; the
// staging slot is then stopped.
func TestStreamVerificationFailsAndStops(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newSVWorld(t, be.DB)
			w.results = []ProbeResult{{Check: "channel.first_segment", Result: "pass", Value: "3 segments"},
				{Check: "ts.conformity", Result: "fail", Value: "PIDs pmt 0x1100 video 0x200 audio 0x201 (want 0x1000 0x100 0x101)"}}
			c := w.staged("v2")
			w.tick() // verifying, first sample: one fail
			if got := w.get(c.ID); got.State != StateVerifying {
				t.Fatalf("one failed sample already ended it: %s", got.State)
			}
			w.now = w.now.Add(3 * time.Second)
			w.sampled = w.now
			w.tick()
			got := w.get(c.ID)
			if got.State != StateFailed || got.EndReason == nil || *got.EndReason != "verification_failed" {
				t.Fatalf("state %s reason %v", got.State, got.EndReason)
			}
			if !strings.Contains(fmt.Sprint(got.VerifyReport["reasons"]), "PIDs pmt 0x1100") {
				t.Errorf("reasons %v", got.VerifyReport["reasons"])
			}
			w.tick()
			if len(w.fs.stopped) == 0 || w.fs.stopped[0] != c.ID {
				t.Fatalf("the staging slot was not stopped: %v", w.fs.stopped)
			}
		})
	}
}

// The staging slot running another version is not healthy: the change waits, then
// fails (slot_not_healthy).
func TestStagingSlotMustRunTheRelease(t *testing.T) {
	be := tvchangetest.Backends(t)[0]
	w := newSVWorld(t, be.DB)
	w.results = passing()
	c := w.staged("v3") // the fake slot reports v2
	w.tick()
	if got := w.get(c.ID); got.State != StateStaged {
		t.Fatalf("a slot on the wrong version moved it on: %s", got.State)
	}
	w.now = w.now.Add(slotStartTimeout + time.Second)
	w.tick()
	got := w.get(c.ID)
	if got.State != StateFailed || *got.EndReason != "slot_not_healthy" {
		t.Fatalf("%s %v", got.State, got.EndReason)
	}
}

// Without a staging runtime (one process, or staging not built) the database checks
// verify, slot.health is skipped with the reason.
func TestNoStagingRuntimeKeepsDatabaseChecks(t *testing.T) {
	be := tvchangetest.Backends(t)[0]
	w := newSVWorld(t, be.DB)
	w.fs.err = fmt.Errorf("%w: no TV slots on this host", ErrNoStagingRuntime)
	c := w.staged("v2")
	w.tick()
	got := w.get(c.ID)
	if got.State != StateReady {
		t.Fatalf("interim verification: %s %v", got.State, got.VerifyReport)
	}
	if ch := reportChecks(got); ch["slot.health"] != ResultSkip {
		t.Fatalf("checks %v", ch)
	}
	if !errors.Is(w.fs.err, ErrNoStagingRuntime) {
		t.Fatal("sentinel")
	}
}
