package tvchange

import (
	"net/http"
	"strings"
	"testing"
	"time"

	core "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// Stories 33.7 and 33.9 on the controller's side, against Postgres: the swap
// after its commit (the front door's operation ends → live; a failed one or one
// that skipped more than a quarter of its channels → rolled back), the reverse
// commit of the front door's automatic rollback, and POST /api/tv/rollback warm
// (the front door's operation) and cold (a rollback change).

func (w *world) release(gen int64) string {
	w.t.Helper()
	var id string
	if err := w.db.QueryRow(`INSERT INTO tv_releases (digest, sched_gen, kinds, settings, streaming_version)
		VALUES ($1, $2, '["lineup"]', '{"tv":{}}', 'v1') RETURNING id`, "d"+time.Now().Format("150405.000000"), gen).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) channel(n int) string {
	w.t.Helper()
	var id string
	if err := w.db.QueryRow(`INSERT INTO channels (number, name, slug, mode) VALUES ($1, $2, $3, 'shuffle') RETURNING id`,
		n, "C"+string(rune('A'+n%26)), "c-"+time.Now().Format("150405.000000")).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) op(change, kind, state string, committed bool, detail string) int64 {
	w.t.Helper()
	var id int64
	if err := w.db.QueryRow(`INSERT INTO tv_swap_ops (change_id, kind, state, committed, created_by, detail, reason)
		VALUES ($1, $2, $3, $4, 'test', $5::jsonb, CASE WHEN $2 = 'rollback' THEN 'auto:crash' ELSE '' END) RETURNING id`,
		change, kind, state, committed, detail).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) jobs(prefix string) int {
	n := 0
	for _, j := range w.q.list() {
		if strings.HasPrefix(j, prefix) {
			n++
		}
	}
	return n
}

func TestSwapGoesLiveWhenTheFrontDoorIsDone(t *testing.T) {
	w := newWorld(t)
	w.now = time.Now().UTC()
	id := w.change(core.StateSwapping, "editor", nil, "")
	w.op(id, "swap", "switching", true, `{}`)
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.state(id); got != core.StateSwapping {
		t.Fatalf("still switching, the change is %s", got)
	}
	w.exec(`UPDATE tv_swap_ops SET state = 'done', detail = '{"switched": 3, "skipped": 0, "to_slot": "amber", "from_slot": "blue"}' WHERE change_id = $1`, id)
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.state(id); got != core.StateLive {
		t.Fatalf("after the operation the change is %s", got)
	}
	var detail string
	if err := w.db.QueryRow(`SELECT detail::text FROM tv_change_events WHERE change_id = $1 AND action = 'swap_done'`, id).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"live_slot": "amber"`) {
		t.Fatalf("swap_done event: %q %v", detail, err)
	}
}

func TestSwapSkippingTooManyChannelsRollsBack(t *testing.T) {
	w := newWorld(t)
	w.now = time.Now().UTC()
	id := w.change(core.StateSwapping, "editor", nil, "")
	w.op(id, "swap", "done", true, `{"switched": 1, "skipped": 3}`)
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.jobs("tv_rollback:"+id) != 1 || w.state(id) != core.StateSwapping {
		t.Fatalf("jobs %v state %s: want the reverse commit enqueued", w.q.list(), w.state(id))
	}
	// a failed operation (its new slot was lost) rolls back too
	id2 := w.change(core.StateSwapping, "settings", nil, "")
	w.exec(`UPDATE tv_changes SET state = 'superseded' WHERE id = $1`, id)
	w.op(id2, "swap", "failed", true, `{"reason": "auto:crash"}`)
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.jobs("tv_rollback:"+id2) != 1 {
		t.Fatalf("a failed swap was not rolled back: %v", w.q.list())
	}
}

// The front door's automatic rollback (a crash in the grace) gets its reverse
// commit enqueued once.
func TestFrontDoorRollbackIsCommitted(t *testing.T) {
	w := newWorld(t)
	w.now = time.Now().UTC()
	id := w.change(core.StateLive, "editor", nil, "")
	w.op(id, "swap", "done", true, `{"switched": 1}`)
	op := w.op(id, "rollback", "switching", false, `{}`)
	for i := 0; i < 2; i++ {
		if err := w.ctl.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if n := w.jobs("tv_rollback:" + id); n != 1 {
		t.Fatalf("reverse commits enqueued: %d (%v)", n, w.q.list())
	}
	var at string
	if err := w.db.QueryRow(`SELECT detail->>'enqueued_at' FROM tv_swap_ops WHERE id = $1`, op).Scan(&at); err != nil || at == "" {
		t.Fatalf("enqueued_at not recorded: %v", err)
	}
}

func TestRollbackWarmAndCold(t *testing.T) {
	w := newWorld(t)
	w.now = time.Now().UTC()
	if code, out := w.do("GET", "/api/tv/rollback", "", "u1"); code != 200 || out["mode"] != "none" || out["reason"] != "no_live_change" {
		t.Fatalf("no live change: %d %v", code, out)
	}
	prev, cur := w.release(0), w.release(1)
	c1, c2 := w.channel(101), w.channel(102)
	w.exec(`INSERT INTO tv_release_channels (release_id, channel_id, def, art, sched_changed) VALUES
		($1, $2, '{"number": 101, "name": "One"}', '{}', false),
		($3, $2, '{"number": 111, "name": "One"}', '{}', true),
		($3, $4, '{"number": 102, "name": "Two"}', '{}', true)`, prev, c1, cur, c2)
	live := w.change(core.StateLive, "editor", nil, "")
	w.exec(`UPDATE tv_changes SET release_id = $2, base_release_id = $3, swapped_at = now() WHERE id = $1`, live, cur, prev)
	w.exec(`INSERT INTO tv_routing (scope, live_release_id, live_gen, previous_release_id, previous_gen, epoch, switched_at, previous_until)
		VALUES ('cluster', $1, 1, $2, 0, 5, now(), now() + interval '15 minutes')`, cur, prev)
	w.exec(`INSERT INTO tv_slots (host_id, slot, role, role_epoch, state, beat_at) VALUES
		('home', 'amber', 'live', 4, 'healthy', now()), ('home', 'blue', 'previous', 4, 'healthy', now())`)
	other := w.change(core.StateReady, "settings", nil, "")

	code, out := w.do("GET", "/api/tv/rollback", "", "u1")
	if code != 200 || out["mode"] != "warm" || out["slot"] != "blue" || out["release_id"] != prev {
		t.Fatalf("warm point: %d %v", code, out)
	}
	code, out = w.do("POST", "/api/tv/rollback", "{}", "u1")
	if code != http.StatusAccepted || out["mode"] != "warm" || out["op_id"] == nil {
		t.Fatalf("warm rollback: %d %v", code, out)
	}
	var n int
	var rel string
	if err := w.db.QueryRow(`SELECT count(*), max(o.release_id::text) FROM tv_channel_switch s JOIN tv_swap_ops o ON o.id = s.op_id
		WHERE o.kind = 'rollback' AND o.change_id = $1`, live).Scan(&n, &rel); err != nil || n != 2 || rel != prev {
		t.Fatalf("rollback operation: %d channels, release %s (%v)", n, rel, err)
	}
	if w.jobs("tv_rollback:"+live) != 1 {
		t.Fatalf("reverse commit not enqueued: %v", w.q.list())
	}
	if got := w.state(other); got != core.StateCancelled {
		t.Fatalf("the change in flight is %s (story 33.9 EC2: cancelled)", got)
	}
	// the controller does not enqueue it a second time
	if err := w.ctl.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.jobs("tv_rollback:"+live) != 1 {
		t.Fatalf("reverse commit enqueued twice: %v", w.q.list())
	}

	// after the grace: cold — a rollback change re-staging the previous release
	w.exec(`DELETE FROM tv_swap_ops`)
	w.exec(`UPDATE tv_routing SET previous_until = now() - interval '1 minute'`)
	code, out = w.do("GET", "/api/tv/rollback", "", "u1")
	if code != 200 || out["mode"] != "cold" {
		t.Fatalf("cold point: %d %v", code, out)
	}
	code, out = w.do("POST", "/api/tv/rollback", "{}", "u1")
	rb, _ := out["rollback_change_id"].(string)
	if code != http.StatusAccepted || out["mode"] != "cold" || rb == "" {
		t.Fatalf("cold rollback: %d %v", code, out)
	}
	var pending, source string
	var approved string
	if err := w.db.QueryRow(`SELECT diff->'pending'::text, source::text, approved_by::text FROM tv_changes WHERE id = $1`, rb).
		Scan(&pending, &source, &approved); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source, `"kind": "rollback"`) || !strings.Contains(pending, `"rollback": true`) ||
		!strings.Contains(pending, `"number": 101`) || !strings.Contains(pending, c2+`": null`) ||
		!strings.Contains(approved, "u1") {
		t.Fatalf("rollback change: source %s pending %s approved %s", source, pending, approved)
	}
	if w.jobs("tv_stage:"+rb) != 1 {
		t.Fatalf("the rollback change was not staged: %v", w.q.list())
	}

	// retention over: none
	w.exec(`UPDATE tv_routing SET switched_at = now() - interval '25 hours'`)
	w.exec(`UPDATE tv_changes SET state = 'cancelled' WHERE id = $1`, rb)
	if code, out := w.do("POST", "/api/tv/rollback", "{}", "u1"); code != http.StatusConflict ||
		!strings.Contains(out["detail"].(string), "no_rollback_point") {
		t.Fatalf("expired rollback point: %d %v", code, out)
	}
}

// A change in swapping whose commit has not run yet (its tv_swap job is queued)
// is not on the air: tv_routing's previous release is the one before the live
// one. A rollback then would put a release two changes old on the air and mark
// the wrong change rolled back — it answers none (swap_not_committed) until the
// commit has run.
func TestNoRollbackBeforeTheSwapCommit(t *testing.T) {
	w := newWorld(t)
	w.now = time.Now().UTC()
	older, prev, next := w.release(0), w.release(1), w.release(2)
	live := w.change(core.StateLive, "editor", nil, "")
	w.exec(`UPDATE tv_changes SET release_id = $2, base_release_id = $3, swapped_at = now() WHERE id = $1`, live, prev, older)
	w.exec(`INSERT INTO tv_routing (scope, live_release_id, live_gen, previous_release_id, previous_gen, epoch, switched_at, previous_until)
		VALUES ('cluster', $1, 1, $2, 0, 5, now(), now() + interval '15 minutes')`, prev, older)
	swapping := w.change(core.StateSwapping, "settings", nil, "")
	w.exec(`UPDATE tv_changes SET release_id = $2, base_release_id = $3 WHERE id = $1`, swapping, next, prev)

	if code, out := w.do("GET", "/api/tv/rollback", "", "u1"); code != 200 || out["mode"] != "none" ||
		out["reason"] != "swap_not_committed" {
		t.Fatalf("point before the commit: %d %v", code, out)
	}
	if code, out := w.do("POST", "/api/tv/rollback", "{}", "u1"); code != http.StatusConflict {
		t.Fatalf("rollback before the commit: %d %v", code, out)
	}
	if w.jobs("tv_rollback:") != 0 {
		t.Fatalf("a reverse commit was enqueued: %v", w.q.list())
	}
	// the commit ran (tv_routing airs the swapping change's release): warm, back to prev
	w.exec(`UPDATE tv_routing SET live_release_id = $1, previous_release_id = $2`, next, prev)
	if code, out := w.do("GET", "/api/tv/rollback", "", "u1"); code != 200 || out["mode"] != "warm" ||
		out["release_id"] != prev || out["change_id"] != swapping {
		t.Fatalf("point after the commit: %d %v", code, out)
	}
}
