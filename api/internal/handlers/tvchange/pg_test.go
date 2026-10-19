package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	core "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

type queued struct {
	mu   sync.Mutex
	jobs []string
}

func (q *queued) enqueue(_ context.Context, stage string, payload map[string]any, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	id, _ := payload["change_id"].(string)
	q.jobs = append(q.jobs, stage+":"+id)
	return nil
}

func (q *queued) list() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string{}, q.jobs...)
}

type world struct {
	t   *testing.T
	db  *sql.DB
	r   http.Handler
	ctl *core.Controller
	q   *queued
	now time.Time
}

func newWorld(t *testing.T) *world {
	db, _ := clustertest.ScratchDB(t)
	q := &queued{}
	w := &world{t: t, db: db, q: q, now: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	ctl := core.NewController(db, nil)
	ctl.Enqueue = q.enqueue
	ctl.Now = func() time.Time { return w.now }
	ctl.Store.Now = ctl.Now
	h := New(db, ctl)
	h.ApplyQueue = func(_ context.Context, stage, digest, changeID string) error {
		q.mu.Lock()
		defer q.mu.Unlock()
		q.jobs = append(q.jobs, "apply:"+stage+":"+digest+":"+changeID)
		return nil
	}
	r := chi.NewRouter()
	h.Mount(r)
	w.r, w.ctl = r, ctl
	return w
}

func (w *world) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.db.Exec(q, args...); err != nil {
		w.t.Fatalf("%s: %v", q, err)
	}
}

func (w *world) do(method, path, body, user string) (int, map[string]any) {
	w.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: user, IsAdmin: true}))
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (w *world) change(state, kind string, createdBy *string, extra string) string {
	w.t.Helper()
	var id string
	var by any
	if createdBy != nil {
		by = *createdBy
	}
	if err := w.db.QueryRow(`INSERT INTO tv_changes (state, title, source, kinds, created_by, digest)
		VALUES ($1, 't', jsonb_build_object('kind', $2::text), '["lineup"]', $3, $4) RETURNING id`,
		state, kind, by, nullIf(extra)).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (w *world) state(id string) string {
	var s string
	if err := w.db.QueryRow(`SELECT state FROM tv_changes WHERE id = $1`, id).Scan(&s); err != nil {
		w.t.Fatal(err)
	}
	return s
}

// TC2 / AC4: at most one change in staged…swapping; staging another answers 409
// change_in_flight with its id; the draft queues and is staged when its turn comes.
func TestOneInFlight(t *testing.T) {
	w := newWorld(t)
	a := w.change(core.StateStaged, core.SourceEditor, nil, "")
	b := w.change(core.StateDraft, core.SourceEditor, nil, "")
	code, out := w.do("POST", "/api/tv/changes/"+b+"/stage", "", "u1")
	if code != http.StatusConflict || out["error"] != "change_in_flight" || out["change_id"] != a {
		t.Fatalf("stage while in flight: %d %v", code, out)
	}
	if len(w.q.list()) != 0 {
		t.Fatal("nothing is enqueued while another change is in flight")
	}
	// the in-flight change ends: the controller stages the queued draft
	w.exec(`UPDATE tv_changes SET state = 'cancelled' WHERE id = $1`, a)
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.q.list(); len(got) == 0 || got[0] != "tv_stage:"+b {
		t.Fatalf("queued %v", got)
	}
	// an invalid transition answers 409 with the state
	code, out = w.do("POST", "/api/tv/changes/"+a+"/approve", "", "u1")
	if code != http.StatusConflict || out["state"] != "cancelled" {
		t.Fatalf("approve a cancelled change: %d %v", code, out)
	}
}

// TC4 / AC5: with require_second_admin the author cannot approve.
func TestSecondAdmin(t *testing.T) {
	w := newWorld(t)
	w.exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv_staging', '{"require_second_admin": true}')`)
	author := "11111111-1111-4111-8111-111111111111"
	id := w.change(core.StateReady, core.SourceEditor, &author, "")
	code, out := w.do("POST", "/api/tv/changes/"+id+"/approve", "", author)
	if code != http.StatusConflict || out["error"] != "second_admin_required" {
		t.Fatalf("author approves: %d %v", code, out)
	}
	code, out = w.do("POST", "/api/tv/changes/"+id+"/approve", "", "22222222-2222-4222-8222-222222222222")
	if code != http.StatusOK || out["state"] != core.StateApproved {
		t.Fatalf("second admin: %d %v", code, out)
	}
	acts, _ := out["allowed_actions"].([]any)
	if len(acts) == 0 {
		t.Fatal("allowed_actions")
	}
}

// A ready change waits for an admin (every rule is approval); approved, the controller
// starts the swap (the tv_swap job) at its swap_at.
func TestApprovalThenSwap(t *testing.T) {
	w := newWorld(t)
	id := w.change(core.StateReady, core.SourceEditor, nil, "")
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.state(id) != core.StateReady || len(w.q.list()) != 1 { // only the daily tv_gc
		t.Fatalf("a ready change went on by itself: %s %v", w.state(id), w.q.list())
	}
	at := w.now.Add(time.Hour).Format(time.RFC3339)
	if code, out := w.do("POST", "/api/tv/changes/"+id+"/approve", `{"swap_at":"`+at+`"}`, "u1"); code != 200 {
		t.Fatalf("approve: %d %v", code, out)
	}
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.state(id) != core.StateApproved {
		t.Fatal("swapped before its swap_at")
	}
	w.now = w.now.Add(61 * time.Minute)
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.state(id) != core.StateSwapping {
		t.Fatalf("state %s", w.state(id))
	}
	if got := w.q.list(); got[len(got)-1] != "tv_swap:"+id {
		t.Fatalf("queued %v", got)
	}
	// history: every transition is an event
	_, out := w.do("GET", "/api/tv/changes/"+id, "", "u1")
	if ev, _ := out["events"].([]any); len(ev) < 2 {
		t.Fatalf("events %v", out["events"])
	}
}

// Story 33.12 AC2: approving a producer's reviewed draft posts the queue's Apply with
// its digest (the re-run stages it, already approved).
func TestApproveProducerDraftIsTheQueuesApply(t *testing.T) {
	w := newWorld(t)
	id := w.change(core.StateDraft, core.SourceWriteChannels, nil, "dg1")
	code, out := w.do("POST", "/api/tv/changes/"+id+"/approve", "", "u1")
	if code != 200 || out["state"] != core.StateDraft {
		t.Fatalf("approve draft: %d %v", code, out)
	}
	if got := w.q.list(); len(got) != 1 || got[0] != "apply:write_channels:dg1:"+id {
		t.Fatalf("apply %v", got)
	}
	appr, _ := out["approved_by"].([]any)
	if len(appr) != 1 {
		t.Fatalf("approved_by %v", out["approved_by"])
	}
}

// TC5 / AC9: the state is in the database: a fresh controller (an API restart) takes
// a change in verifying on - the checks run, the change becomes ready (or failed).
func TestControllerResume(t *testing.T) {
	w := newWorld(t)
	ch := "6f1c9a2e-0000-4000-8000-0000000000a1"
	w.exec(`INSERT INTO channels (id, number, name, slug, mode, logo_path) VALUES ($1, 101, 'One', 'one', 'shuffle', '/l.png')`, ch)
	var rel string
	if err := w.db.QueryRow(`INSERT INTO tv_releases (digest, sched_gen) VALUES ('d1', 1) RETURNING id`).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	w.exec(`INSERT INTO tv_release_channels (release_id, channel_id, def) VALUES ($1, $2,
		'{"id":"6f1c9a2e-0000-4000-8000-0000000000a1","number":101,"slug":"one","name":"One","enabled":true,"logo_path":"/l.png"}')`, rel, ch)
	// generation 0 (live) and 1 agree on the block on the air, and 1 covers the next day
	start := w.now.Add(-10 * time.Minute)
	for g := 0; g <= 1; g++ {
		for i := 0; i < 50; i++ {
			s := start.Add(time.Duration(i) * 30 * time.Minute)
			w.exec(`INSERT INTO channel_programs (channel_id, seq, kind, start_at, end_at, source_duration, sched_gen)
				VALUES ($1, $2, 'slate', $3, $4, 1800000, $5)`, ch, i, s, s.Add(30*time.Minute), g)
		}
	}
	id := w.change(core.StateVerifying, core.SourceEditor, nil, "")
	w.exec(`UPDATE tv_changes SET release_id = $2 WHERE id = $1`, id, rel)
	fresh := core.NewController(w.db, nil)
	fresh.Now = w.ctl.Now
	fresh.Enqueue = w.q.enqueue
	if err := fresh.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.state(id); got != core.StateReady {
		var vr []byte
		_ = w.db.QueryRow(`SELECT verify_report FROM tv_changes WHERE id = $1`, id).Scan(&vr)
		t.Fatalf("state %s, report %s", got, vr)
	}
	// generation 1 cutting the programme on the air fails verification
	w.exec(`UPDATE channel_programs SET end_at = end_at - interval '1 minute' WHERE sched_gen = 1 AND seq = 0`)
	w.exec(`UPDATE tv_changes SET state = 'verifying' WHERE id = $1`, id)
	if err := fresh.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.state(id); got != core.StateFailed {
		t.Fatalf("a cut programme passed: %s", got)
	}
}

func TestReleasesSlotsAndNotYet(t *testing.T) {
	w := newWorld(t)
	var rel string
	if err := w.db.QueryRow(`INSERT INTO tv_releases (digest, sched_gen, kinds) VALUES ('d0', 0, '["bootstrap"]') RETURNING id`).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	w.exec(`INSERT INTO tv_routing (scope, live_release_id, live_gen) VALUES ('cluster', $1, 0)`, rel)
	code, out := w.do("GET", "/api/tv/releases", "", "u1")
	items, _ := out["items"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["live"] != true {
		t.Fatalf("releases: %d %v", code, out)
	}
	code, out = w.do("GET", "/api/tv/slots", "", "u1")
	if code != 200 || len(out["slots"].([]any)) == 0 {
		t.Fatalf("slots: %d %v", code, out)
	}
	// story 33.9: nothing went live through a change yet — no rollback point
	if code, _ := w.do("POST", "/api/tv/rollback", "", "u1"); code != http.StatusConflict {
		t.Fatalf("rollback: %d", code)
	}
	code, out = w.do("POST", "/api/tv/changes", `{"title":"Hand made"}`, "u1")
	if code != http.StatusCreated || out["state"] != "draft" {
		t.Fatalf("create: %d %v", code, out)
	}
	info := Info(t.Context(), w.db).(StagingInfo)
	if !info.Enabled || info.LiveReleaseID == nil || *info.LiveReleaseID != rel {
		t.Fatalf("info %+v", info)
	}
}

// GET /api/tv/slots reports the real TV slots (story 33.1): roles, the routing
// epoch, a slot that missed three beats as unhealthy; one live slot when the
// engine runs in one process.
func TestSlotsReportsTheRealSlots(t *testing.T) {
	w := newWorld(t)
	code, out := w.do("GET", "/api/tv/slots", "", "u1")
	if code != 200 || out["runtime"] != "single" || len(out["slots"].([]any)) != 1 {
		t.Fatalf("no slot rows: %d %v", code, out)
	}
	w.exec(`INSERT INTO tv_slots (host_id, slot, role, role_epoch, state, pid, warm, encoders_used, beat_at)
		VALUES ('home', 'blue', 'live', 2, 'healthy', 11, '["c1"]', 2, now()),
		       ('home', 'green', 'standby', 2, 'healthy', 12, '[]', 0, now() - interval '1 minute')`)
	code, out = w.do("GET", "/api/tv/slots", "", "u1")
	slots, _ := out["slots"].([]any)
	if code != 200 || out["runtime"] != "slots" || len(slots) != 2 {
		t.Fatalf("slots: %d %v", code, out)
	}
	blue, green := slots[0].(map[string]any), slots[1].(map[string]any)
	if blue["role"] != "live" || blue["state"] != "healthy" || blue["epoch"] != float64(2) ||
		len(blue["warm"].([]any)) != 1 || green["role"] != "standby" || green["state"] != "unhealthy" {
		t.Fatalf("blue %v green %v", blue, green)
	}
}
