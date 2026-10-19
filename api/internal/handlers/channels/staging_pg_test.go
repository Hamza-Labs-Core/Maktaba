package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

type stagingWorld struct {
	t  *testing.T
	db *sql.DB
	r  http.Handler
	on bool
}

func newStagingWorld(t *testing.T) *stagingWorld {
	db, _ := clustertest.ScratchDB(t)
	w := &stagingWorld{t: t, db: db, on: true}
	d := tvchange.NewDrafts(db)
	d.EnabledFunc = func(context.Context) bool { return w.on }
	h := &Handler{DB: db, Drafts: d, DataDir: t.TempDir()}
	r := chi.NewRouter()
	h.Mount(r)
	h.MountSchedule(r)
	w.r = r
	return w
}

func (w *stagingWorld) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.db.Exec(q, args...); err != nil {
		w.t.Fatalf("%s: %v", q, err)
	}
}

func (w *stagingWorld) do(method, path, body string) (int, map[string]any) {
	w.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u1", IsAdmin: true}))
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (w *stagingWorld) pending() map[string]any {
	w.t.Helper()
	var raw []byte
	if err := w.db.QueryRow(`SELECT diff FROM tv_changes WHERE state = 'draft' AND source->>'kind' = 'editor'`).Scan(&raw); err != nil {
		w.t.Fatal(err)
	}
	var d map[string]any
	_ = json.Unmarshal(raw, &d)
	p, _ := d["pending"].(map[string]any)
	return p
}

const (
	chA = "6f1c9a2e-0000-4000-8000-0000000000c1"
	chB = "6f1c9a2e-0000-4000-8000-0000000000c2"
)

// Story 33.12 TC4 / AC6: with staging on, editor saves go into the open editor draft:
// the air (channels) is untouched, the editor reads the draft's values back.
func TestEditorSavesToDraft(t *testing.T) {
	w := newStagingWorld(t)
	w.exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'One', 'one', 'shuffle'), ($2, 102, 'Two', 'two', 'shuffle')`, chA, chB)

	code, out := w.do("PATCH", "/api/channels/"+chA, `{"name":"Uno","number":115}`)
	if code != 200 || out["name"] != "Uno" || out["number"] != float64(115) || out["draft"] == nil {
		t.Fatalf("patch: %d %v", code, out)
	}
	var name string
	var num int
	_ = w.db.QueryRow(`SELECT name, number FROM channels WHERE id = $1`, chA).Scan(&name, &num)
	if name != "One" || num != 101 {
		t.Fatalf("the air changed: %s %d", name, num)
	}
	code, out = w.do("GET", "/api/channels/"+chA, "")
	if code != 200 || out["name"] != "Uno" {
		t.Fatalf("get reads the draft: %d %v", code, out)
	}
	// a second save appends to the same draft
	if code, out := w.do("POST", "/api/channels", `{"name":"Three","mode":"shuffle"}`); code != http.StatusCreated || out["number"] != float64(116) {
		t.Fatalf("create: %d %v", code, out)
	}
	var drafts, live int
	_ = w.db.QueryRow(`SELECT count(*) FROM tv_changes WHERE state = 'draft'`).Scan(&drafts)
	_ = w.db.QueryRow(`SELECT count(*) FROM channels`).Scan(&live)
	if drafts != 1 || live != 2 {
		t.Fatalf("drafts %d channels %d", drafts, live)
	}
	// a collision in the lineup as the draft will have it
	if code, _ := w.do("PATCH", "/api/channels/"+chB, `{"number":115}`); code != http.StatusConflict {
		t.Fatalf("number collision in the draft: %d", code)
	}
	if code, _ := w.do("DELETE", "/api/channels/"+chB, ""); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := w.do("POST", "/api/channels/"+chA+"/regenerate", ""); code != http.StatusAccepted {
		t.Fatalf("regenerate: %d", code)
	}
	if code, _ := w.do("PATCH", "/api/channels/"+chA+"/strip", `{"enabled":true}`); code != 200 {
		t.Fatalf("strip: %d", code)
	}
	p := w.pending()
	chans, _ := p["channels"].(map[string]any)
	if len(chans) != 3 || chans[chB] != nil {
		t.Fatalf("pending channels %v", chans)
	}
	a, _ := chans[chA].(map[string]any)
	look, _ := a["mode_config"].(map[string]any)["look"].(map[string]any)
	if a["name"] != "Uno" || look["strip"] == nil {
		t.Fatalf("pending a %v", a)
	}
	if sched, _ := p["sched"].([]any); len(sched) != 1 || sched[0] != chA {
		t.Fatalf("sched %v", p["sched"])
	}
	if edited, _ := p["edited"].([]any); len(edited) != 1 {
		t.Fatalf("edited %v", p["edited"])
	}
	_, out = w.do("GET", "/api/channels", "")
	items, _ := out["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("list with the draft: %v", items)
	}
	var stale int
	_ = w.db.QueryRow(`SELECT count(*) FROM channel_schedule_state WHERE stale`).Scan(&stale)
	if stale != 0 {
		t.Fatal("the stale flag was set on the live generation")
	}

	// staging off: today's behaviour (TC7)
	w.on = false
	if code, _ := w.do("PATCH", "/api/channels/"+chB, `{"name":"Deux"}`); code != 200 {
		t.Fatalf("direct patch: %d", code)
	}
	_ = w.db.QueryRow(`SELECT name FROM channels WHERE id = $1`, chB).Scan(&name)
	if name != "Deux" {
		t.Fatalf("staging off writes directly: %s", name)
	}
}

// Story 33.3 TC5: the live readers read the live generation only.
func TestLiveReadersFilterGeneration(t *testing.T) {
	w := newStagingWorld(t)
	w.on = false
	w.exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'One', 'one', 'shuffle')`, chA)
	now := time.Now().UTC()
	for g, title := range []string{"live zero", "staged one"} {
		w.exec(`INSERT INTO channel_programs (channel_id, seq, kind, start_at, end_at, source_duration, title_snapshot, sched_gen)
			VALUES ($1, 0, 'slate', $2, $3, 3600000, jsonb_build_object('title', $4::text), $5)`,
			chA, now.Add(-time.Minute), now.Add(time.Hour), title, g)
	}
	title := func() string {
		_, out := w.do("GET", "/api/channels/"+chA, "")
		np, _ := out["now_playing"].(map[string]any)
		s, _ := np["title"].(string)
		return s
	}
	blocks := func(q string) int {
		_, out := w.do("GET", "/api/channels/"+chA+"/schedule"+q, "")
		b, _ := out["blocks"].([]any)
		return len(b)
	}
	if title() != "live zero" || blocks("") != 1 {
		t.Fatalf("generation 0 is live: %q %d", title(), blocks(""))
	}
	w.exec(`INSERT INTO tv_routing (scope, live_gen) VALUES ('cluster', 1)`)
	if title() != "staged one" || blocks("") != 1 {
		t.Fatalf("generation 1 is live: %q", title())
	}
	// a staged channel is not in the lineup
	w.exec(`INSERT INTO channels (id, number, name, slug, mode, lifecycle, enabled) VALUES ($1, 101, 'Staged', 'one', 'shuffle', 'staged', false)`, chB)
	_, out := w.do("GET", "/api/channels", "")
	if items, _ := out["items"].([]any); len(items) != 1 {
		t.Fatalf("lineup %v", items)
	}
}
