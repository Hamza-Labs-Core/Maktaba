package filler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

const (
	livePool  = "11111111-0000-4000-8000-000000000001"
	liveItem  = "22222222-0000-4000-8000-000000000001"
	videoA    = "33333333-0000-4000-8000-00000000000a"
	videoB    = "33333333-0000-4000-8000-00000000000b"
	usingChan = "44444444-0000-4000-8000-000000000001"
	otherChan = "44444444-0000-4000-8000-000000000002"
)

type stagingWorld struct {
	t  *testing.T
	db *sql.DB
	on bool
	r  chi.Router
}

func newStagingWorld(t *testing.T) *stagingWorld {
	w := &stagingWorld{t: t, db: tvchangetest.SQLite(t), on: true}
	for _, s := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, title TEXT, duration_sec REAL)`,
		`CREATE TABLE filler_pools (id TEXT PRIMARY KEY, channel_id TEXT, name TEXT NOT NULL, external_key TEXT,
			created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE filler_items (id TEXT PRIMARY KEY, pool_id TEXT NOT NULL, video_id TEXT NOT NULL, type TEXT NOT NULL,
			duration_ms INTEGER, created_at TEXT DEFAULT CURRENT_TIMESTAMP, UNIQUE (pool_id, video_id))`,
		`INSERT INTO videos (id, title, duration_sec) VALUES ('` + videoA + `', 'Sting', 30), ('` + videoB + `', 'Headlines', 90)`,
		`INSERT INTO filler_pools (id, name) VALUES ('` + livePool + `', 'Bumpers')`,
		`INSERT INTO filler_items (id, pool_id, video_id, type, duration_ms) VALUES ('` + liveItem + `', '` + livePool + `', '` + videoA + `', 'bumper', 30000)`,
		// a channel whose filler list names the pool, and one that does not
		`INSERT INTO channels (id, number, name, mode_config) VALUES ('` + usingChan + `', 101, 'Quran', '{"filler":{"lists":[{"pool":"` + livePool + `","weight":1}]}}')`,
		`INSERT INTO channels (id, number, name, mode_config) VALUES ('` + otherChan + `', 102, 'Kids', '{}')`,
	} {
		if _, err := w.db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	d := tvchange.NewDrafts(w.db)
	d.EnabledFunc = func(context.Context) bool { return w.on }
	w.r = chi.NewRouter()
	(&Handler{DB: w.db, Drafts: d}).Mount(w.r)
	return w
}

func (w *stagingWorld) do(method, path, body string) *httptest.ResponseRecorder {
	w.t.Helper()
	req := adminReq(method, path, body)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u1", IsAdmin: true}))
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, req)
	return rec
}

func (w *stagingWorld) count(table string) int {
	w.t.Helper()
	var n int
	if err := w.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func (w *stagingWorld) list() []Pool {
	w.t.Helper()
	rec := w.do(http.MethodGet, "/api/filler/pools", "")
	if rec.Code != http.StatusOK {
		w.t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}
	var out struct{ Items []Pool }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		w.t.Fatal(err)
	}
	return out.Items
}

func (w *stagingWorld) draft() *tvchange.Change {
	w.t.Helper()
	c, err := tvchange.NewDrafts(w.db).Open(context.Background(), tvchange.SourceEditor)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

func byName(pools []Pool, name string) *Pool {
	for i := range pools {
		if pools[i].Name == name {
			return &pools[i]
		}
	}
	return nil
}

// Story 33.12 AC6: with staging on, pool and item edits go into the editor's draft
// change; the tables (what airs) stay as they are; the list shows the draft's view.
func TestFillerEditsGoToTheDraft(t *testing.T) {
	w := newStagingWorld(t)

	rec := w.do(http.MethodPost, "/api/filler/pools", `{"name":"Station ids"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var created Pool
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Draft == nil || created.Draft.Op != "added" || created.Draft.ChangeID == "" {
		t.Fatalf("created %+v", created)
	}
	if n := w.count("filler_pools"); n != 1 {
		t.Fatalf("the new pool was written to the table (%d pools)", n)
	}

	rec = w.do(http.MethodPost, "/api/filler/pools/"+created.ID+"/items",
		`{"items":[{"video_id":"`+videoB+`","type":"station_id"},{"video_id":"`+videoB+`","type":"station_id"}]}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"draft_change"`) {
		t.Fatalf("add items %d %s", rec.Code, rec.Body.String())
	}
	if n := w.count("filler_items"); n != 1 {
		t.Fatalf("the item was written to the table (%d items)", n)
	}
	// a clip the live pool already has is a no-op, a new one is drafted
	rec = w.do(http.MethodPost, "/api/filler/pools/"+livePool+"/items",
		`{"items":[{"video_id":"`+videoA+`","type":"bumper"},{"video_id":"`+videoB+`","type":"filler"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add to the live pool %d %s", rec.Code, rec.Body.String())
	}
	var added struct{ Items []Item }
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	if len(added.Items) != 1 || added.Items[0].VideoID != videoB || !added.Items[0].Pending {
		t.Fatalf("added %+v", added.Items)
	}

	pools := w.list()
	sid, bump := byName(pools, "Station ids"), byName(pools, "Bumpers")
	if sid == nil || sid.Draft == nil || sid.Draft.Op != "added" || len(sid.Items) != 1 || !sid.Items[0].Pending ||
		sid.Items[0].Title != "Headlines" || sid.Items[0].DurationMS == nil || *sid.Items[0].DurationMS != 90000 {
		t.Fatalf("the drafted pool in the list: %+v", sid)
	}
	if bump == nil || bump.Draft == nil || bump.Draft.Op != "items" || len(bump.Items) != 2 {
		t.Fatalf("the live pool in the list: %+v", bump)
	}

	// removing the live clip and renaming the live pool: drafted, not applied
	if rec := w.do(http.MethodDelete, "/api/filler/items/"+liveItem, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("delete item %d %s", rec.Code, rec.Body.String())
	}
	if rec := w.do(http.MethodPatch, "/api/filler/pools/"+livePool, `{"name":"Stings"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename %d %s", rec.Code, rec.Body.String())
	}
	var name string
	_ = w.db.QueryRow(`SELECT name FROM filler_pools WHERE id = $1`, livePool).Scan(&name)
	if name != "Bumpers" || w.count("filler_items") != 1 {
		t.Fatalf("live tables changed: name %q, %d items", name, w.count("filler_items"))
	}
	pools = w.list()
	if p := byName(pools, "Stings"); p == nil || p.Draft.Op != "edited" || len(p.Items) != 1 || p.Items[0].VideoID != videoB {
		t.Fatalf("after the drafted removal and rename: %+v", pools)
	}

	// the draft holds it all, and regenerates the channel whose filler names the pool
	dr := w.draft()
	if dr == nil {
		t.Fatal("no draft")
	}
	pd := dr.Pending()
	if len(pd.FillerPools()) != 2 || len(pd.FillerItems()) != 3 {
		t.Fatalf("pending filler pools %v items %v", pd.FillerPools(), pd.FillerItems())
	}
	if v, ok := pd.FillerItems()[liveItem]; !ok || v != nil {
		t.Fatalf("the live item's removal: %v %v", v, ok)
	}
	if got := strings.Join(pd.Sched(), ","); got != usingChan {
		t.Fatalf("sched %q, want the channel naming the pool", got)
	}
	if n, _ := dr.Diff["summary"].(map[string]any)["filler"].(float64); n != 5 {
		t.Fatalf("summary %v", dr.Diff["summary"])
	}

	// a pool added in the draft and deleted again is forgotten with its clips
	if rec := w.do(http.MethodDelete, "/api/filler/pools/"+created.ID, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("delete drafted pool %d %s", rec.Code, rec.Body.String())
	}
	pd = w.draft().Pending()
	if _, ok := pd.FillerPools()[created.ID]; ok || len(pd.FillerItems()) != 2 {
		t.Fatalf("after deleting the drafted pool: pools %v items %v", pd.FillerPools(), pd.FillerItems())
	}
	// deleting the live pool drafts its removal; the list drops it
	if rec := w.do(http.MethodDelete, "/api/filler/pools/"+livePool, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("delete live pool %d", rec.Code)
	}
	if pools := w.list(); len(pools) != 0 {
		t.Fatalf("pools after the drafted removals: %+v", pools)
	}
	if w.count("filler_pools") != 1 {
		t.Fatal("the live pool was deleted from the table")
	}
	if rec := w.do(http.MethodPost, "/api/filler/pools/"+livePool+"/items", `{"items":[{"video_id":"`+videoA+`"}]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("add to a pool the draft removes: %d", rec.Code)
	}
}

// With staging off the edit goes to the table at once, as before.
func TestFillerEditsWithoutStaging(t *testing.T) {
	w := newStagingWorld(t)
	w.on = false
	if rec := w.do(http.MethodPost, "/api/filler/pools", `{"name":"Direct"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	if w.count("filler_pools") != 2 || w.draft() != nil {
		t.Fatal("staging off: the pool went to a draft")
	}
	if rec := w.do(http.MethodDelete, "/api/filler/items/"+liveItem, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete %d", rec.Code)
	}
	if w.count("filler_items") != 0 {
		t.Fatal("staging off: the item was not deleted")
	}
	if p := byName(w.list(), "Bumpers"); p == nil || p.Draft != nil {
		t.Fatalf("list %+v", p)
	}
}
