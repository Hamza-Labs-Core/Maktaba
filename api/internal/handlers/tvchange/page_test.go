package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	core "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// pageWorld runs the Changes page endpoints on either backend.
type pageWorld struct {
	t   *testing.T
	db  *sql.DB
	h   *Handler
	r   *chi.Mux
	now time.Time
}

func newPageWorld(t *testing.T, db *sql.DB) *pageWorld {
	w := &pageWorld{t: t, db: db, now: time.Date(2026, 6, 15, 20, 10, 0, 0, time.UTC)}
	ctl := core.NewController(db, nil)
	ctl.Enqueue = func(context.Context, string, map[string]any, string) error { return nil }
	ctl.Now = func() time.Time { return w.now }
	ctl.Store.Now = ctl.Now
	w.h = New(db, ctl)
	w.r = chi.NewRouter()
	w.h.Mount(w.r)
	return w
}

func (w *pageWorld) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.db.Exec(q, args...); err != nil {
		w.t.Fatalf("%s: %v", q, err)
	}
}

// ts is a channel_programs timestamp (native on Postgres; the scheduler's text on SQLite).
func (w *pageWorld) ts(t time.Time) any {
	if core.IsSQLite(w.db) {
		return t.UTC().Format("2006-01-02 15:04:05.000000+00:00")
	}
	return t.UTC()
}

func (w *pageWorld) block(ch string, gen int64, seq int, start time.Time, minutes int, title string) {
	w.t.Helper()
	w.exec(`INSERT INTO channel_programs (id, channel_id, seq, kind, start_at, end_at, source_duration, title_snapshot, sched_gen)
		VALUES ($1, $2, $3, 'program', $4, $5, $6, $7, $8)`, uuid.NewString(), ch, seq, w.ts(start),
		w.ts(start.Add(time.Duration(minutes)*time.Minute)), minutes*60000, `{"title": "`+title+`"}`, gen)
}

func (w *pageWorld) get(path, user string) (int, map[string]any) {
	w.t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: user, IsAdmin: true}))
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

const (
	pageAdmin = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	chA       = "00000000-0000-4000-8000-0000000000a1"
	chB       = "00000000-0000-4000-8000-0000000000b2"
)

// seed is a staged change with a release (generation 7) that regenerates two channels;
// channel A's 20:00-21:00 programme is on the air, B's 20:00-20:30.
func (w *pageWorld) seed() string {
	w.t.Helper()
	w.exec(`INSERT INTO users (id, username, pw_hash) VALUES ($1, 'mahmoud', 'x')`, pageAdmin)
	w.exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'Quran', 'quran', 'shuffle'),
		($2, 102, 'Hadith', 'hadith', 'shuffle')`, chA, chB)
	rel := uuid.NewString()
	w.exec(`INSERT INTO tv_releases (id, digest, sched_gen) VALUES ($1, 'd1', 7)`, rel)
	w.exec(`INSERT INTO tv_routing (scope, live_gen) VALUES ('cluster', 0)`)
	at := time.Date(2026, 6, 15, 20, 0, 0, 0, time.UTC)
	w.block(chA, 0, 1, at, 60, "Tafsir 1")
	w.block(chA, 0, 2, at.Add(time.Hour), 60, "Tafsir 2")
	w.block(chA, 7, 1, at, 60, "Tafsir 1")
	w.block(chA, 7, 2, at.Add(time.Hour), 30, "Seerah 1")
	w.block(chB, 0, 1, at, 30, "Hadith 1")
	w.block(chB, 7, 1, at, 30, "Hadith 1")
	by := pageAdmin
	c := &core.Change{State: core.StateDraft, Title: "New grid", Source: map[string]any{"kind": core.SourceEditor},
		CreatedBy: &by}
	if err := w.h.Store.Insert(context.Background(), w.db, c); err != nil {
		w.t.Fatal(err)
	}
	diff := map[string]any{
		"lineup": map[string]any{"added": []any{}, "removed": []any{},
			"renumbered": []any{map[string]any{"id": chB, "from": 102, "to": 112}}, "renamed": []any{}, "changed": []any{}},
		"schedules": []any{
			map[string]any{"id": chB, "number": 112, "name": "Hadith", "reason": "regenerated", "divergence_at": "2026-06-15T20:30:00+00:00"},
			map[string]any{"id": chA, "number": 101, "name": "Quran", "reason": "regenerated", "divergence_at": "2026-06-15T21:00:00+00:00"},
		},
		"settings": map[string]any{}, "art": []any{}, "binary": nil,
		"pending": map[string]any{"channels": map[string]any{chB: map[string]any{"number": 112, "name": "Hadith"}}},
	}
	if err := w.h.Store.Set(context.Background(), w.db, c.ID, map[string]any{"state": core.StateStaged,
		"release_id": rel, "kinds": []string{"lineup", "schedule"}, "diff": diff}); err != nil {
		w.t.Fatal(err)
	}
	return c.ID
}

// TC2 (story 33.11): the schedule diff - the regenerated channels by number, a page at
// a time, and one channel's next 24 h before / after from its divergence.
func TestScheduleDiffAndBoundaries(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newPageWorld(t, be.DB)
			id := w.seed()

			code, out := w.get("/api/tv/changes/"+id+"/schedule-diff", pageAdmin)
			chans, _ := out["channels"].([]any)
			if code != 200 || len(chans) != 2 || out["pages"] != float64(1) {
				t.Fatalf("list %d %v", code, out)
			}
			if chans[0].(map[string]any)["number"] != float64(101) {
				t.Fatalf("by number %v", chans)
			}

			code, out = w.get("/api/tv/changes/"+id+"/schedule-diff?channel="+chA, pageAdmin)
			before, _ := out["before"].([]any)
			after, _ := out["after"].([]any)
			if code != 200 || len(before) != 1 || len(after) != 1 || out["unchanged"] != false {
				t.Fatalf("channel A %d %v", code, out)
			}
			if before[0].(map[string]any)["title"] != "Tafsir 2" || after[0].(map[string]any)["title"] != "Seerah 1" {
				t.Fatalf("titles %v / %v", before, after)
			}
			code, out = w.get("/api/tv/changes/"+id+"/schedule-diff?channel="+chB, pageAdmin)
			if code != 200 || out["unchanged"] != true {
				t.Fatalf("channel B unchanged %d %v", code, out)
			}
			if code, _ := w.get("/api/tv/changes/"+id+"/schedule-diff?channel="+uuid.NewString(), pageAdmin); code != 404 {
				t.Fatalf("a channel the change does not regenerate: %d", code)
			}

			// boundaries: a swap at 20:10 waits for each programme on the air (at most an hour)
			w.exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv_staging', '{"boundary_max_wait_minutes": 60}')`)
			code, out = w.get("/api/tv/changes/"+id+"/boundaries", pageAdmin)
			bs, _ := out["channels"].([]any)
			if code != 200 || len(bs) != 2 || out["swap_wait"] != "boundary" {
				t.Fatalf("boundaries %d %v", code, out)
			}
			byID := map[string]map[string]any{}
			for _, b := range bs {
				m := b.(map[string]any)
				byID[m["channel_id"].(string)] = m
			}
			if !strings.HasPrefix(byID[chA]["switch_at"].(string), "2026-06-15T21:00:00") || byID[chA]["on_air"] != "Tafsir 1" {
				t.Fatalf("A %v", byID[chA])
			}
			if !strings.HasPrefix(byID[chB]["switch_at"].(string), "2026-06-15T20:30:00") {
				t.Fatalf("B %v", byID[chB])
			}
			if !strings.HasPrefix(out["last_switch_at"].(string), "2026-06-15T21:00:00") {
				t.Fatalf("last %v", out["last_switch_at"])
			}
			// the default 30-minute cap: A's programme runs 50 more minutes, so it switches at 20:40
			w.exec(`DELETE FROM app_settings WHERE key = 'rules.tv_staging'`)
			_, out = w.get("/api/tv/changes/"+id+"/boundaries?at=2026-06-15T20:10:00Z", pageAdmin)
			for _, b := range out["channels"].([]any) {
				m := b.(map[string]any)
				if m["channel_id"] == chA && (!strings.HasPrefix(m["switch_at"].(string), "2026-06-15T20:40:00") || m["capped"] != true) {
					t.Fatalf("capped A %v", m)
				}
			}
			_, out = w.get("/api/tv/changes/"+id+"/boundaries?swap_wait=immediate", pageAdmin)
			for _, b := range out["channels"].([]any) {
				if m := b.(map[string]any); !strings.HasPrefix(m["switch_at"].(string), "2026-06-15T20:10:00") {
					t.Fatalf("immediate %v", m)
				}
			}
			if code, _ := w.get("/api/tv/changes/"+id+"/boundaries?at=tomorrow", pageAdmin); code != 400 {
				t.Fatalf("bad at %d", code)
			}
		})
	}
}

// The detail the page reads: the author's name, the audit rows, the release, the
// staging settings in the list, and a draft's summary.
func TestDetailForThePage(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newPageWorld(t, be.DB)
			id := w.seed()
			w.h.Store.Audit(context.Background(), w.db, pageAdmin, "stage", id, map[string]any{"from": "draft", "to": "staged"})

			code, out := w.get("/api/tv/changes/"+id, pageAdmin)
			if code != 200 {
				t.Fatalf("get %d %v", code, out)
			}
			if users, _ := out["users"].(map[string]any); users[pageAdmin] != "mahmoud" {
				t.Fatalf("users %v", out["users"])
			}
			if audit, _ := out["audit"].([]any); len(audit) != 1 || audit[0].(map[string]any)["action"] != "stage" {
				t.Fatalf("audit %v", out["audit"])
			}
			if rel, _ := out["release"].(map[string]any); rel["sched_gen"] != float64(7) {
				t.Fatalf("release %v", out["release"])
			}
			if _, has := out["pending_summary"]; has {
				t.Fatal("a staged change has its diff, not a draft summary")
			}

			code, out = w.get("/api/tv/changes", pageAdmin)
			cfg, _ := out["config"].(map[string]any)
			if code != 200 || cfg["swap_wait"] != "boundary" || cfg["quiet_window"] != "03:00-05:00" {
				t.Fatalf("list config %d %v", code, out["config"])
			}
			if ap, _ := cfg["auto_promote"].(map[string]any); ap["lineup"] != "approval" {
				t.Fatalf("auto promote %v", cfg)
			}

			// a draft: what it holds, named from the lineup
			d := &core.Change{State: core.StateDraft, Title: "Editor", Source: map[string]any{"kind": core.SourceEditor},
				Diff: map[string]any{"pending": map[string]any{
					"channels": map[string]any{chA: nil, "00000000-0000-4000-8000-0000000000c3": map[string]any{"number": 130, "name": "Seerah"}},
					"art":      map[string]any{chB: map[string]any{"logo": map[string]any{"path": "/x.png"}}},
					"settings": map[string]any{"tv_output": map[string]any{"fps": 25}},
					"filler": map[string]any{
						"pools": map[string]any{"p-new": map[string]any{"name": "Station ids", "created": true}, "p-old": nil},
						"items": map[string]any{"i1": map[string]any{"pool_id": "p-new", "video_id": "v1", "type": "station_id"}},
					},
				}}}
			if err := w.h.Store.Insert(context.Background(), w.db, d); err != nil {
				t.Fatal(err)
			}
			_, out = w.get("/api/tv/changes/"+d.ID, pageAdmin)
			ps, _ := out["pending_summary"].(map[string]any)
			chans, _ := ps["channels"].([]any)
			if len(chans) != 3 {
				t.Fatalf("pending %v", ps)
			}
			ops := map[string]string{}
			for _, c := range chans {
				m := c.(map[string]any)
				ops[m["name"].(string)] = m["op"].(string)
			}
			if ops["Quran"] != "removed" || ops["Seerah"] != "added" || ops["Hadith"] != "art" {
				t.Fatalf("ops %v", ops)
			}
			if s, _ := ps["settings"].([]any); len(s) != 1 || s[0] != "tv_output" {
				t.Fatalf("settings %v", ps["settings"])
			}
			// story 33.12 AC6: the filler pools the draft touches
			fl, _ := ps["filler"].([]any)
			if len(fl) != 2 {
				t.Fatalf("filler %v", ps["filler"])
			}
			gone, added := fl[0].(map[string]any), fl[1].(map[string]any)
			if gone["id"] != "p-old" || gone["op"] != "removed" ||
				added["name"] != "Station ids" || added["op"] != "added" || added["items_added"] != 1.0 {
				t.Fatalf("filler %v", fl)
			}
		})
	}
}

// story 33.6 AC1: the preview is minted for a staged change only (verifying, ready,
// approved), signed with the cluster stream key, an hour; a staged-but-not-verifying
// change or a missing key answers 409.
func TestPreviewStatesAndToken(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newPageWorld(t, be.DB)
			id := w.seed() // staged
			post := func() (int, map[string]any) {
				req := httptest.NewRequest("POST", "/api/tv/changes/"+id+"/preview", nil)
				req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: pageAdmin, IsAdmin: true}))
				rec := httptest.NewRecorder()
				w.r.ServeHTTP(rec, req)
				out := map[string]any{}
				_ = json.Unmarshal(rec.Body.Bytes(), &out)
				return rec.Code, out
			}
			if code, _ := post(); code != 409 {
				t.Fatalf("preview of a staged change: %d", code)
			}
			w.exec(`UPDATE tv_changes SET state = 'ready' WHERE id = $1`, id)
			if core.IsSQLite(w.db) {
				if code, _ := post(); code != 409 {
					t.Fatalf("no stream key: %d", code)
				}
				return
			}
			w.exec(`INSERT INTO tv_cluster_secret (id, stream_key) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET stream_key = EXCLUDED.stream_key`,
				[]byte("0123456789abcdef0123456789abcdef"))
			code, out := post()
			if code != 200 {
				t.Fatalf("preview: %d %v", code, out)
			}
			tok, _ := out["token"].(string)
			if parts := strings.Split(tok, "."); len(parts) != 3 || parts[0] != id {
				t.Fatalf("token %q", tok)
			}
			exp, _ := time.Parse(time.RFC3339, out["expires_at"].(string))
			if d := time.Until(exp); d < 59*time.Minute || d > 61*time.Minute {
				t.Fatalf("expires in %v", d)
			}
			chans, _ := out["channels"].([]any)
			if len(chans) != 0 {
				// the seed's release has no channels; the URLs are checked end to end
				t.Logf("channels %v", chans)
			}
			if !strings.Contains(out["m3u"].(string), "/staging/lineup.m3u?pt=") || out["hls_base"] != "/stream/staging/channel/" {
				t.Fatalf("urls %v", out)
			}
		})
	}
}

// The changes list carries no diff: a channel plan's is tens of MB, and the page read
// every one of them (~500 MB, 40 s a load) to show a table that never uses it. Opening
// a change still returns its whole diff, and the in-flight change is still named.
func TestListLeavesTheDiffsOut(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newPageWorld(t, be.DB)
			id := w.seed()

			code, out := w.get("/api/tv/changes", pageAdmin)
			items, _ := out["items"].([]any)
			if code != 200 || len(items) != 1 {
				t.Fatalf("list %d %v", code, out)
			}
			item := items[0].(map[string]any)
			if d, _ := item["diff"].(map[string]any); len(d) != 0 {
				t.Fatalf("the list carries a diff: %v", d)
			}
			if item["id"] != id || item["state"] != "staged" || item["title"] != "New grid" {
				t.Fatalf("item %v", item)
			}
			if out["in_flight"] != id {
				t.Fatalf("in_flight %v, want %s", out["in_flight"], id)
			}

			code, one := w.get("/api/tv/changes/"+id, pageAdmin)
			diff, _ := one["diff"].(map[string]any)
			if code != 200 || diff["lineup"] == nil || diff["schedules"] == nil {
				t.Fatalf("the change lost its diff: %d %v", code, one)
			}
		})
	}
}
