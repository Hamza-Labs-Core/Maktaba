package channels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// newBuilderEnv is the art env plus what the builder reads: the edited_at
// column (slot 0118), the plan's links, a guide, series and collections.
func newBuilderEnv(t *testing.T) artEnv {
	t.Helper()
	e := newArtEnv(t)
	for _, ddl := range []string{
		`ALTER TABLE channels ADD COLUMN edited_at TEXT`,
		`CREATE TABLE channel_schedule_state (channel_id TEXT NOT NULL, sched_gen INTEGER NOT NULL DEFAULT 0,
			stale BOOLEAN NOT NULL DEFAULT 0, PRIMARY KEY (channel_id, sched_gen))`,
		`CREATE TABLE tv_routing (scope TEXT PRIMARY KEY, live_gen INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE channel_programs (channel_id TEXT NOT NULL, start_at TEXT NOT NULL,
			end_at TEXT NOT NULL, title_snapshot TEXT)`,
		// slot 0128: the live readers read the live generation's view
		`CREATE VIEW channel_programs_live AS SELECT * FROM channel_programs`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, title TEXT, filename TEXT NOT NULL,
			duration_sec REAL, deleted_at TEXT)`,
		`CREATE TABLE series (id TEXT PRIMARY KEY, name TEXT NOT NULL, name_override TEXT)`,
		`CREATE TABLE series_episodes (series_id TEXT NOT NULL, video_id TEXT NOT NULL)`,
		`CREATE TABLE collections (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE collection_items (collection_id TEXT NOT NULL, video_id TEXT NOT NULL)`,
	} {
		if _, err := e.db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e artEnv) link(t *testing.T, key, channelID string) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO external_links (target, subject_kind, subject_id,
		external_id, title, content_hash, created_by_tool) VALUES
		('maktaba_channel', 'catalog', $1, $2, 'x', 'abc123', 1)`, key, channelID); err != nil {
		t.Fatal(err)
	}
}

func jsonReq(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestPatchMarksEditedAndListShowsPlanStatus(t *testing.T) {
	e := newBuilderEnv(t)
	insertChannel(t, e.db, "00000000-0000-4000-8000-0000000000c1", nil)
	e.link(t, "mix", "00000000-0000-4000-8000-0000000000c1")

	_, c := e.do(t, artAdmin, httptest.NewRequest("GET", "/api/channels/00000000-0000-4000-8000-0000000000c1", nil))
	if c.Plan == nil || !c.Plan.Owned || c.Plan.Edited {
		t.Fatalf("a fresh plan channel: %+v", c.Plan)
	}
	rec, c := e.do(t, artAdmin, jsonReq("PATCH", "/api/channels/00000000-0000-4000-8000-0000000000c1", `{"name":"Mine"}`))
	if rec.Code != 200 || c.Plan == nil || !c.Plan.Edited || c.Plan.EditedAt == nil {
		t.Fatalf("after an edit: %d %+v", rec.Code, c.Plan)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/channels", nil)
	e.srv.ServeHTTP(rec, req.WithContext(principal.WithPrincipal(req.Context(), artAdmin)))
	var list struct{ Items []Channel }
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Plan == nil || !list.Items[0].Plan.Edited {
		t.Fatalf("list: %s", rec.Body.String())
	}
}

func TestArtUploadIsNotAnEdit(t *testing.T) {
	e := newBuilderEnv(t)
	insertChannel(t, e.db, "00000000-0000-4000-8000-0000000000c1", nil)
	e.link(t, "mix", "00000000-0000-4000-8000-0000000000c1")
	if rec, _ := e.upload(t, "00000000-0000-4000-8000-0000000000c1", ArtLogo, "l.png", pngBytes); rec.Code != 200 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	_, c := e.do(t, artAdmin, httptest.NewRequest("GET", "/api/channels/00000000-0000-4000-8000-0000000000c1", nil))
	if c.Plan == nil || c.Plan.Edited {
		t.Fatalf("an upload is an override, not an edit: %+v", c.Plan)
	}
}

func TestFollowPlanHandsTheChannelBack(t *testing.T) {
	e := newBuilderEnv(t)
	insertChannel(t, e.db, "00000000-0000-4000-8000-0000000000c1", nil)
	e.link(t, "mix", "00000000-0000-4000-8000-0000000000c1")
	e.do(t, artAdmin, jsonReq("PATCH", "/api/channels/00000000-0000-4000-8000-0000000000c1", `{"name":"Mine"}`))

	rec, _ := e.do(t, artAdmin, httptest.NewRequest("POST", "/api/channels/00000000-0000-4000-8000-0000000000c1/follow-plan", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("follow-plan: %d %s", rec.Code, rec.Body.String())
	}
	var hash string
	if err := e.db.QueryRow(`SELECT content_hash FROM external_links WHERE external_id = '00000000-0000-4000-8000-0000000000c1'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != FollowPlanHash {
		t.Fatalf("link hash %q", hash)
	}
	var stale bool
	if err := e.db.QueryRow(`SELECT stale FROM channel_schedule_state WHERE channel_id = '00000000-0000-4000-8000-0000000000c1'`).Scan(&stale); err != nil || !stale {
		t.Fatalf("schedule not marked stale: %v %v", stale, err)
	}
	_, c := e.do(t, artAdmin, httptest.NewRequest("GET", "/api/channels/00000000-0000-4000-8000-0000000000c1", nil))
	if c.Plan == nil || c.Plan.Edited {
		t.Fatalf("edited mark kept: %+v", c.Plan)
	}
}

func TestFollowPlanRefusesAChannelNoPlanOwns(t *testing.T) {
	e := newBuilderEnv(t)
	insertChannel(t, e.db, "00000000-0000-4000-8000-0000000000c1", nil)
	if rec, _ := e.do(t, artAdmin, httptest.NewRequest("POST", "/api/channels/00000000-0000-4000-8000-0000000000c1/follow-plan", nil)); rec.Code != http.StatusConflict {
		t.Fatalf("got %d", rec.Code)
	}
	if rec, _ := e.do(t, artAdmin, httptest.NewRequest("POST", "/api/channels/nope/follow-plan", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("missing channel: %d", rec.Code)
	}
	user := &principal.Principal{UserID: "u", AccessAllLibraries: true}
	if rec, _ := e.do(t, user, httptest.NewRequest("POST", "/api/channels/00000000-0000-4000-8000-0000000000c1/follow-plan", nil)); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", rec.Code)
	}
}

func TestGuideUntilOnList(t *testing.T) {
	e := newBuilderEnv(t)
	insertChannel(t, e.db, "00000000-0000-4000-8000-0000000000c1", nil)
	end := time.Date(2026, 10, 20, 6, 0, 0, 0, time.UTC)
	for _, s := range []string{"2026-10-19T00:00:00Z", "2026-10-20T06:00:00Z"} {
		if _, err := e.db.Exec(`INSERT INTO channel_programs (channel_id, start_at, end_at) VALUES ('00000000-0000-4000-8000-0000000000c1', '2026-10-18T00:00:00Z', $1)`, s); err != nil {
			t.Fatal(err)
		}
	}
	_, c := e.do(t, artAdmin, httptest.NewRequest("GET", "/api/channels/00000000-0000-4000-8000-0000000000c1", nil))
	if c.GuideUntil == nil || !c.GuideUntil.Equal(end) {
		t.Fatalf("guide_until %v", c.GuideUntil)
	}
}

func TestReorderSwapsAndMarksOnlyTheMoved(t *testing.T) {
	e := newBuilderEnv(t)
	now := time.Now().UTC()
	for i, id := range []string{"00000000-0000-4000-8000-0000000000aa", "00000000-0000-4000-8000-0000000000bb", "00000000-0000-4000-8000-0000000000cc"} {
		if _, err := e.db.Exec(`INSERT INTO channels (id, number, name, slug, mode, created_at, updated_at)
			VALUES ($1, $2, $1, $1, 'shuffle', $3, $3)`, id, i+1, now); err != nil {
			t.Fatal(err)
		}
	}
	// a and b swap (a plain one-step update would collide), c stays
	if _, err := e.db.Exec(`CREATE UNIQUE INDEX ch_num ON channels (number)`); err != nil {
		t.Fatal(err)
	}
	rec, _ := e.do(t, artAdmin, jsonReq("POST", "/api/channels/reorder",
		`[{"id":"00000000-0000-4000-8000-0000000000aa","number":2},{"id":"00000000-0000-4000-8000-0000000000bb","number":1},{"id":"00000000-0000-4000-8000-0000000000cc","number":3}]`))
	if rec.Code != 200 {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}
	got := map[string]int{}
	edited := map[string]bool{}
	rows, err := e.db.Query(`SELECT id, number, edited_at IS NOT NULL FROM channels`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		var ed bool
		_ = rows.Scan(&id, &n, &ed)
		got[id], edited[id] = n, ed
	}
	if got["00000000-0000-4000-8000-0000000000aa"] != 2 || got["00000000-0000-4000-8000-0000000000bb"] != 1 || got["00000000-0000-4000-8000-0000000000cc"] != 3 {
		t.Fatalf("numbers %v", got)
	}
	if !edited["00000000-0000-4000-8000-0000000000aa"] || !edited["00000000-0000-4000-8000-0000000000bb"] || edited["00000000-0000-4000-8000-0000000000cc"] {
		t.Fatalf("edited %v", edited)
	}
}

func TestSources(t *testing.T) {
	e := newBuilderEnv(t)
	for _, q := range []string{
		`INSERT INTO videos (id, title, filename, duration_sec) VALUES ('v1', 'Ep 1', 'e1.mp4', 1500), ('v2', 'Ep 2', 'e2.mp4', 1500), ('v3', 'Clip', 'clip.mp4', 30)`,
		`INSERT INTO videos (id, title, filename, duration_sec, deleted_at) VALUES ('v4', 'Gone', 'g.mp4', 60, '2026-01-01')`,
		`INSERT INTO series (id, name) VALUES ('s1', 'Prophets'), ('s2', 'Other')`,
		`INSERT INTO series_episodes (series_id, video_id) VALUES ('s1', 'v1'), ('s1', 'v2'), ('s1', 'v4')`,
		`INSERT INTO collections (id, name) VALUES ('00000000-0000-4000-8000-0000000000c1', 'Short clips')`,
		`INSERT INTO collection_items (collection_id, video_id) VALUES ('00000000-0000-4000-8000-0000000000c1', 'v3')`,
		`INSERT INTO channel_plans (input_hash, plan) VALUES ('h', '{"lanes":{"kids.weekly":{"name":"Kids weekly","shows":["Prophets"],"hours":2.5,"episode_count":6},"news":{"name":"News","shows":[],"hours":1,"episode_count":2}}}')`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	get := func(qs string) (int, []Source) {
		rec, _ := e.do(t, artAdmin, httptest.NewRequest("GET", "/api/channels/sources?"+qs, nil))
		var out struct{ Items []Source }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Items
	}
	if code, s := get("kind=series&q=proph"); code != 200 || len(s) != 1 || s[0].ID != "s1" || s[0].Count != 2 || s[0].DurationMS != 3_000_000 {
		t.Fatalf("series: %d %+v", code, s)
	}
	if _, s := get("kind=collection"); len(s) != 1 || s[0].Count != 1 || s[0].Name != "Short clips" {
		t.Fatalf("collections: %+v", s)
	}
	if _, s := get("kind=lane&q=prophets"); len(s) != 1 || s[0].ID != "kids.weekly" || s[0].Count != 6 || s[0].DurationMS != 9_000_000 {
		t.Fatalf("lanes: %+v", s)
	}
	if _, s := get("kind=lane"); len(s) != 2 || s[0].ID != "kids.weekly" {
		t.Fatalf("lanes by size: %+v", s)
	}
	if _, s := get("kind=video&q=clip"); len(s) != 1 || s[0].ID != "v3" || s[0].DurationMS != 30_000 {
		t.Fatalf("videos: %+v", s)
	}
	if _, s := get("kind=video&q=gone"); len(s) != 0 {
		t.Fatalf("a deleted video is offered: %+v", s)
	}
	if code, _ := get("kind=nope"); code != http.StatusBadRequest {
		t.Fatalf("bad kind: %d", code)
	}
	if code, _ := get("kind=series&limit=x"); code != http.StatusBadRequest {
		t.Fatalf("bad limit: %d", code)
	}
	user := &principal.Principal{UserID: "u", AccessAllLibraries: true}
	if rec, _ := e.do(t, user, httptest.NewRequest("GET", "/api/channels/sources?kind=series", nil)); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d", rec.Code)
	}
}
