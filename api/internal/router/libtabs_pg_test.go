package router_test

// A library's Series and Collections tabs (Epic 45 Phase 4, story 45.24)
// against a real migrated Postgres behind the production router: the series
// with the caller's progress and their sorts, the merged collections (topic,
// clip filler and stand-alone, curated, smart) with their counts and kind
// filter, a topic / clip group's page, and the role rule: a viewer of library
// A gets 404 for library B's tabs and groups, the same as an unknown library.
// Runs when MAKTABA_TEST_PG_URL is set (see access_pg_test.go).

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPGLibraryTabs(t *testing.T) {
	e, _ := setupAccess(t) // acLibA holds acVidA1, acLibB acVidB1; member sees A only
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	base := time.Now().UTC().Add(time.Minute) // after setupAccess's videos
	vid := func(lib, state string, i int) string {
		id := uuid.NewString()
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
			VALUES ($1,$2,$3,$4,$5,1000,$6,$7,$8,100,$6,$6)`,
			id, lib, "/media/lt/"+id+".mp4", id+".mp4", fmt.Sprintf("%064x", 1000+i), base.Add(time.Duration(i)*time.Minute), state, fmt.Sprintf("lt %d", i))
		return id
	}
	a2, a3, a4, hidden := vid(acLibA, "ready", 2), vid(acLibA, "ready", 3), vid(acLibA, "ready", 4), vid(acLibA, "missing", 5)

	// series: الفقه (A1, a2 and a hidden one), بلوغ المرام (a3, the newest), B's
	fiqh, bulugh, seriesB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO series (id, library_id, name) VALUES ($1,$4,'الفقه'), ($2,$4,'بلوغ المرام'), ($3,$5,'B series')`,
		fiqh, bulugh, seriesB, acLibA, acLibB)
	exec(`INSERT INTO series_episodes (series_id, video_id, episode) VALUES ($1,$2,1), ($1,$3,2), ($1,$4,3), ($5,$6,1), ($7,$8,1)`,
		fiqh, acVidA1, a2, hidden, bulugh, a3, seriesB, acVidB1)
	// the member finished lesson 1 of الفقه and is half-way through بلوغ المرام
	exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at) VALUES ($1,$2,100,true,$4), ($1,$3,50,false,$5)`,
		acMember, acVidA1, a3, base, base.Add(30*time.Minute))

	// topics, clip groups (A's filler and stand-alone, one only hidden, B's)
	topic, topicHidden, filler, standalone, clipB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO topic_collections (id, library_id, playlist_id, name, membership_hash) VALUES ($1,$3,'PL1','الصيام','h'), ($2,$3,'PL2','مخفي','h')`,
		topic, topicHidden, acLibA)
	exec(`INSERT INTO topic_items (topic_id, video_id, position) VALUES ($1,$2,2), ($1,$3,1), ($4,$5,1)`, topic, acVidA1, a2, topicHidden, hidden)
	exec(`INSERT INTO clip_groups (id, library_id, kind, basis, name, title, membership_hash) VALUES
		($1,$4,'clips','playlist','فوائد','A - مقاطع - فوائد','h'),
		($2,$4,'standalone','rest','متنوعة','A - منفردات متنوعة','h'),
		($3,$5,'clips','rest','متنوعة','B - مقاطع متنوعة','h')`, filler, standalone, clipB, acLibA, acLibB)
	exec(`INSERT INTO clip_group_items (group_id, video_id, position) VALUES ($1,$2,1), ($3,$4,1), ($5,$6,1)`,
		filler, a4, standalone, a3, clipB, acVidB1)

	lib := "/api/libraries/" + acLibA
	names := func(out map[string]any) []string { return itemIDs(out, "name") }
	eq := func(what string, got []string, want ...string) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}

	// ---- Series ----
	out := e.want(http.StatusOK, "member", "GET", lib+"/series", nil)
	eq("series by name", names(out), "الفقه", "بلوغ المرام")
	if out["total"] != 2.0 || out["next"] != nil {
		t.Errorf("series page %v / %v", out["total"], out["next"])
	}
	first := out["items"].([]any)[0].(map[string]any)
	if first["episodes"] != 2.0 || first["watched"] != 1.0 || first["in_progress"] != 0.0 || first["poster_video_id"] != acVidA1 {
		t.Errorf("الفقه card %v (want 2 visible episodes, 1 watched, lesson 1's poster)", first)
	}
	second := out["items"].([]any)[1].(map[string]any)
	if second["in_progress"] != 1.0 || second["last_watched_at"] == nil {
		t.Errorf("بلوغ المرام card %v", second)
	}
	eq("series newest", names(e.want(http.StatusOK, "member", "GET", lib+"/series?sort=newest", nil)), "بلوغ المرام", "الفقه")
	eq("series recently watched", names(e.want(http.StatusOK, "member", "GET", lib+"/series?sort=recent", nil)), "بلوغ المرام", "الفقه")
	// another user watched nothing: their recent order is by name, no progress
	other := e.want(http.StatusOK, "other", "GET", lib+"/series?sort=recent", nil)
	eq("other's recent", names(other), "الفقه", "بلوغ المرام")
	if c := other["items"].([]any)[0].(map[string]any); c["watched"] != 0.0 {
		t.Errorf("another user's progress leaked: %v", c)
	}
	paged := e.want(http.StatusOK, "member", "GET", lib+"/series?limit=1", nil)
	if len(paged["items"].([]any)) != 1 || paged["next"] != 1.0 || paged["total"] != 2.0 {
		t.Errorf("series page 1 %v", paged)
	}
	if last := e.want(http.StatusOK, "member", "GET", lib+"/series?limit=1&offset=5", nil); last["total"] != 2.0 || len(last["items"].([]any)) != 0 {
		t.Errorf("past the end %v", last)
	}
	e.want(http.StatusBadRequest, "member", "GET", lib+"/series?sort=progress", nil)
	e.want(http.StatusBadRequest, "member", "GET", lib+"/series?limit=0", nil)

	// ---- Collections ----
	out = e.want(http.StatusOK, "member", "GET", lib+"/collections", nil)
	eq("collections, every kind in order", names(out), "الصيام", "فوائد", "متنوعة", "A picks", "A smart")
	eq("their kinds", itemIDs(out, "kind"), "topic", "clip", "clip", "curated", "smart")
	eq("clip subkinds", itemIDs(out, "subkind"), "filler", "standalone")
	counts := out["counts"].(map[string]any)
	if counts["all"] != 5.0 || counts["topic"] != 1.0 || counts["filler"] != 1.0 || counts["standalone"] != 1.0 || counts["curated"] != 2.0 {
		t.Errorf("counts %v", counts)
	}
	items := out["items"].([]any)
	topicCard := items[0].(map[string]any)
	if topicCard["videos"] != 2.0 || topicCard["automatic"] != true || fmt.Sprint(topicCard["poster_video_ids"]) != fmt.Sprint([]any{a2, acVidA1}) {
		t.Errorf("topic card %v (2 videos in position order)", topicCard)
	}
	// a list counts what the caller may see: the member not its B video, the admin both
	if c := items[3].(map[string]any); c["videos"] != 1.0 || c["automatic"] != false {
		t.Errorf("member's curated card %v", c)
	}
	if c := items[4].(map[string]any); c["videos"] != nil || c["smart_query"] == nil {
		t.Errorf("smart card %v", c)
	}
	admin := e.want(http.StatusOK, "admin", "GET", lib+"/collections?kind=curated", nil)
	eq("admin curated", names(admin), "A picks", "A smart")
	if c := admin["items"].([]any)[0].(map[string]any); c["videos"] != 2.0 {
		t.Errorf("admin's curated card %v", c)
	}
	eq("topic filter", names(e.want(http.StatusOK, "member", "GET", lib+"/collections?kind=topic", nil)), "الصيام")
	eq("stand-alone filter", names(e.want(http.StatusOK, "member", "GET", lib+"/collections?kind=standalone", nil)), "متنوعة")
	pg := e.want(http.StatusOK, "member", "GET", lib+"/collections?limit=2&offset=2", nil)
	eq("collections page 2", names(pg), "متنوعة", "A picks")
	if pg["next"] != 4.0 {
		t.Errorf("collections next %v", pg["next"])
	}
	e.want(http.StatusBadRequest, "member", "GET", lib+"/collections?kind=promo", nil)

	// ---- a topic's / clip group's page ----
	page := e.want(http.StatusOK, "member", "GET", lib+"/collections/topic/"+topic, nil)
	eq("topic videos in position order", itemIDs(page, "id"), a2, acVidA1)
	col := page["collection"].(map[string]any)
	if col["name"] != "الصيام" || col["videos"] != 2.0 || col["automatic"] != true {
		t.Errorf("topic head %v", col)
	}
	if v := page["items"].([]any)[1].(map[string]any); v["completed"] != true {
		t.Errorf("the member's playback on the topic page: %v", v)
	}
	clip := e.want(http.StatusOK, "member", "GET", lib+"/collections/clip/"+standalone+"?limit=1", nil)
	if c := clip["collection"].(map[string]any); c["subkind"] != "standalone" || len(clip["items"].([]any)) != 1 {
		t.Errorf("stand-alone page %v", clip)
	}
	e.want(http.StatusNotFound, "member", "GET", lib+"/collections/topic/"+filler, nil) // a clip group is not a topic
	e.want(http.StatusNotFound, "member", "GET", lib+"/collections/promo/"+topic, nil)
	e.want(http.StatusNotFound, "member", "GET", lib+"/collections/topic/not-a-uuid", nil)

	// ---- roles: library B is not the member's, and answers as one that does not exist ----
	libB := "/api/libraries/" + acLibB
	for _, path := range []string{
		libB + "/series", libB + "/collections", libB + "/collections/clip/" + clipB,
		lib + "/collections/clip/" + clipB, // B's group under A's id
		"/api/libraries/" + uuid.NewString() + "/series",
		"/api/libraries/not-a-uuid/collections",
	} {
		e.want(http.StatusNotFound, "member", "GET", path, nil)
	}
	e.want(http.StatusUnauthorized, "", "GET", lib+"/series", nil)
	e.want(http.StatusUnauthorized, "", "GET", lib+"/collections", nil)
	// the admin sees every library
	eq("admin B series", names(e.want(http.StatusOK, "admin", "GET", libB+"/series", nil)), "B series")
	eq("admin B collections", names(e.want(http.StatusOK, "admin", "GET", libB+"/collections", nil)), "متنوعة", "B picks")
	e.want(http.StatusOK, "admin", "GET", libB+"/collections/clip/"+clipB, nil)
}
