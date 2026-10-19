package tvchange

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// recorder is a Notifier subscriber that keeps what it got (story 33.13 TC4: a
// subscriber joins without the controller changing).
type recorder struct {
	mu  sync.Mutex
	got []notify.Notification
}

func (r *recorder) Deliver(_ context.Context, n notify.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, n)
	return nil
}

func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, n := range r.got {
		out = append(out, n.Kind)
	}
	return out
}

type feedWorld struct {
	t   *testing.T
	db  *sql.DB
	ctl *Controller
	hub *notify.Hub
	rec *recorder
	now time.Time
}

func newFeedWorld(t *testing.T, db *sql.DB) *feedWorld {
	w := &feedWorld{t: t, db: db, rec: &recorder{}, now: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	w.ctl = NewController(db, nil)
	w.ctl.Now = func() time.Time { return w.now }
	w.ctl.Store.Now = w.ctl.Now
	w.hub = notify.NewHub(db, nil)
	w.hub.Now = w.ctl.Now
	w.hub.Subscribe(w.rec)
	w.ctl.Notifier = w.hub
	return w
}

func (w *feedWorld) change(state string, kinds []string) *Change {
	w.t.Helper()
	c := &Change{State: state, Title: "Ramadan grid", Source: map[string]any{"kind": SourceManual}}
	if err := w.ctl.Store.Insert(context.Background(), w.db, c); err != nil {
		w.t.Fatal(err)
	}
	if err := w.ctl.Store.Set(context.Background(), w.db, c.ID, map[string]any{"kinds": kinds}); err != nil {
		w.t.Fatal(err)
	}
	c.Kinds = kinds
	return c
}

func (w *feedWorld) move(c *Change, to, action string, actor *string, fields map[string]any) {
	w.t.Helper()
	if err := w.ctl.Store.Move(context.Background(), w.db, c, to, action, actor, nil, fields); err != nil {
		w.t.Fatalf("%s -> %s: %v", c.State, to, err)
	}
}

func (w *feedWorld) feed() {
	w.t.Helper()
	cfg, err := LoadConfig(context.Background(), w.db)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.ctl.notifyEvents(context.Background(), cfg); err != nil {
		w.t.Fatal(err)
	}
}

// TC1 (story 33.13): the transitions that need an admin, went wrong or went on the air
// make one notice each, with a link to the change; the others make none.
func TestNotifyOnTransitions(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newFeedWorld(t, be.DB)
			w.feed() // the first run starts at the newest event: nothing old is announced
			admin := "11111111-1111-4111-8111-111111111111"

			a := w.change(StateStaged, []string{"lineup"})
			w.move(a, StateVerifying, ActionStartVerify, nil, nil)
			w.move(a, StateReady, ActionPass, nil, nil)
			w.feed()
			if got := w.rec.kinds(); len(got) != 1 || got[0] != NotifyReady {
				t.Fatalf("ready: %v", got)
			}
			n := w.rec.got[0]
			if n.Link != "/admin/live-tv/changes/"+a.ID || n.Severity != notify.SeverityWarn || n.TitleKey != "notify."+NotifyReady {
				t.Fatalf("ready notice %+v", n)
			}
			if n.Params["number"] == nil {
				t.Fatalf("params %v", n.Params)
			}
			// an admin approves: no notice; the swap: live
			w.move(a, StateApproved, ActionApprove, &admin, nil)
			w.move(a, StateSwapping, ActionSwapStart, nil, nil)
			w.move(a, StateLive, ActionSwap, nil, nil)
			w.feed()
			if got := w.rec.kinds(); len(got) != 2 || got[1] != NotifyLive {
				t.Fatalf("live: %v", got)
			}

			b := w.change(StateStaged, []string{"schedule"})
			w.move(b, StateVerifying, ActionStartVerify, nil, nil)
			w.move(b, StateFailed, ActionFail, nil, map[string]any{"end_reason": "verification_failed"})
			w.feed()
			if got := w.rec.kinds(); len(got) != 3 || got[2] != NotifyFailed || w.rec.got[2].Severity != notify.SeverityError {
				t.Fatalf("failed: %v", got)
			}

			// an auto-promote rule switched on: "scheduled", not "needs approval"
			if _, err := be.DB.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv_staging', '{"auto_promote_art": "quiet_window"}')`); err != nil {
				t.Fatal(err)
			}
			c := w.change(StateStaged, []string{"art"})
			w.move(c, StateVerifying, ActionStartVerify, nil, nil)
			w.move(c, StateReady, ActionPass, nil, nil)
			w.move(c, StateSwapping, ActionAutoPromote, nil, nil)
			w.move(c, StateReady, ActionSwapFailed, nil, nil)
			w.feed()
			got := w.rec.kinds()
			if len(got) != 5 || got[3] != NotifyAutoScheduled || got[4] != NotifySwapFailed {
				t.Fatalf("auto + swap failed: %v", got)
			}
			if w.rec.got[3].Params["rule"] != RuleQuietWindow {
				t.Fatalf("rule %v", w.rec.got[3].Params)
			}

			// the notices are stored for the bell (the hub's first subscriber)
			var stored int
			if err := be.DB.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&stored); err != nil || stored != 5 {
				t.Fatalf("stored %d %v", stored, err)
			}
			// the feed does not repeat itself
			w.feed()
			if len(w.rec.kinds()) != 5 {
				t.Fatal("repeated")
			}
		})
	}
}

// EC1: repeated failures of the same change notify once an hour at most.
func TestNotifyRateLimited(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newFeedWorld(t, be.DB)
			ctx := context.Background()
			p := notify.Payload{Severity: notify.SeverityError, Subject: "tv_change:x"}
			for i := 0; i < 3; i++ {
				if err := w.hub.Notify(ctx, NotifyFailed, p); err != nil {
					t.Fatal(err)
				}
			}
			if got := w.rec.kinds(); len(got) != 1 {
				t.Fatalf("within the hour: %v", got)
			}
			w.now = w.now.Add(61 * time.Minute)
			if err := w.hub.Notify(ctx, NotifyFailed, p); err != nil {
				t.Fatal(err)
			}
			if got := w.rec.kinds(); len(got) != 2 {
				t.Fatalf("after the hour: %v", got)
			}
		})
	}
}

// AC1: a ready change waiting more than a day, and a rollback point that ends within
// the hour.
func TestNotifyTimers(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newFeedWorld(t, be.DB)
			ctx := context.Background()
			c := w.change(StateReady, []string{"lineup"})
			ready := w.now.Add(-25 * time.Hour)
			c.ReadyAt = &ready
			if err := w.ctl.notifyTimers(ctx, []*Change{c}); err != nil {
				t.Fatal(err)
			}
			if got := w.rec.kinds(); len(got) != 1 || got[0] != NotifyWaiting {
				t.Fatalf("waiting: %v", got)
			}
			if err := w.ctl.notifyTimers(ctx, []*Change{c}); err != nil {
				t.Fatal(err)
			}
			if len(w.rec.kinds()) != 1 {
				t.Fatal("waiting notice repeated within the day")
			}
			var rel string
			if IsSQLite(be.DB) {
				rel = "22222222-2222-4222-8222-222222222222"
				if _, err := be.DB.Exec(`INSERT INTO tv_releases (id, digest, sched_gen) VALUES ($1, 'd', 1)`, rel); err != nil {
					t.Fatal(err)
				}
			} else if err := be.DB.QueryRow(`INSERT INTO tv_releases (digest, sched_gen) VALUES ('d', 1) RETURNING id`).Scan(&rel); err != nil {
				t.Fatal(err)
			}
			if _, err := be.DB.Exec(`INSERT INTO tv_routing (scope, live_gen, previous_release_id, previous_until)
				VALUES ('cluster', 0, $1, $2)`, rel, ts(be.DB, w.now.Add(40*time.Minute))); err != nil {
				t.Fatal(err)
			}
			if err := w.ctl.notifyTimers(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if got := w.rec.kinds(); len(got) != 2 || got[1] != NotifyRollbackEnds {
				t.Fatalf("rollback point: %v", got)
			}
		})
	}
}

// TC2 / AC3: every automatic transition writes an audit_log row (actor_source = the
// event's source, from / to in the payload); audit_log refuses edits on Postgres
// (append-only, slot 0059).
func TestAuditRowsAppendOnly(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newFeedWorld(t, be.DB)
			w.feed()
			admin := "11111111-1111-4111-8111-111111111111"
			a := w.change(StateStaged, []string{"lineup"})
			w.move(a, StateVerifying, ActionStartVerify, nil, nil)
			w.move(a, StateReady, ActionPass, nil, nil)
			w.move(a, StateApproved, ActionApprove, &admin, nil) // the handler audits this one
			w.feed()
			rows, err := w.ctl.Store.AuditRows(context.Background(), a.ID)
			if err != nil {
				t.Fatal(err)
			}
			// the creation (by its producer, here "manual"), staged -> verifying, -> ready
			if len(rows) != 3 || rows[0].Action != "create" || rows[0].ActorSource != SourceManual {
				t.Fatalf("audit rows %+v", rows)
			}
			for i, want := range []string{StateVerifying, StateReady} {
				r := rows[i+1]
				if r.ActorSource != "system" || r.ActorUserID != nil || r.Payload["to"] != want {
					t.Fatalf("row %d %+v", i, r)
				}
			}
			if IsSQLite(be.DB) {
				return
			}
			if _, err := be.DB.Exec(`UPDATE audit_log SET action = 'x' WHERE id = $1`, rows[0].ID); err == nil ||
				!strings.Contains(err.Error(), "append") {
				t.Fatalf("audit_log update: %v", err)
			}
			if _, err := be.DB.Exec(`DELETE FROM audit_log WHERE id = $1`, rows[0].ID); err == nil {
				t.Fatal("audit_log delete allowed")
			}
			// the category check (widened in slot 0130) takes tv_change
			var n int
			if err := be.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE category = 'tv_change'`).Scan(&n); err != nil || n < 2 {
				t.Fatalf("tv_change rows %d %v", n, err)
			}
		})
	}
}
