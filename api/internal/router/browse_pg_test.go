package router_test

// The browsing routes (Epic 45 story 45.4) against a real migrated Postgres
// behind the production router: the Postgres SQL (LATERAL posters, the year
// of mtime), the routes beside the libraries handler's {id}, and a viewer
// of library A never seeing library B. Runs when MAKTABA_TEST_PG_URL is set
// (see access_pg_test.go).

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPGBrowse(t *testing.T) {
	e, _ := setupAccess(t) // acLibA holds acVidA1, acLibB acVidB1; member and other see A only
	db := e.db
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	now := time.Now().UTC()
	// more of library A: one hidden, one with no duration; a B series
	a2, a3, hidden := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for i, r := range []struct {
		id, state string
		dur       any
		mtime     time.Time
	}{
		{a2, "ready", 50.0, time.Date(2023, 5, 1, 0, 0, 0, 0, time.UTC)},
		{a3, "ready", nil, time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
		{hidden, "missing", 70.0, time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
	} {
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
			VALUES ($1,$2,$3,$4,$5,1000,$6,$7,'t',$8,$9,$9)`,
			r.id, acLibA, "/media/ac/"+r.id+".mp4", r.id+".mp4", fmt.Sprintf("%064x", i+1), r.mtime, r.state, r.dur, now.Add(time.Duration(i+1)*time.Second))
	}
	exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at) VALUES ($1,$2,40,false,$4), ($1,$3,40,false,$4)`,
		acMember, acVidA1, acVidB1, now)
	for i, vid := range []string{acVidA1, acVidA1, acVidB1, acVidB1, acVidB1} {
		exec(`INSERT INTO watch_sessions (id, user_id, video_id, started_at) VALUES ($1,$2,$3,$4)`,
			uuid.NewString(), acOther, vid, now.Add(-time.Duration(i)*time.Hour))
	}
	seriesB := uuid.NewString()
	exec(`INSERT INTO series (id, library_id, name) VALUES ($1,$2,'B series')`, seriesB, acLibB)
	exec(`INSERT INTO series_episodes (series_id, video_id) VALUES ($1,$2)`, seriesB, acVidB1)

	// summary: the member sees library A only, without the hidden video
	sum := e.want(http.StatusOK, "member", "GET", "/api/libraries/summary", nil)
	items, _ := sum["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("member summary %v", sum)
	}
	card := items[0].(map[string]any)
	if card["id"] != acLibA || card["videos"] != float64(3) || len(card["poster_video_ids"].([]any)) != 3 {
		t.Errorf("member card %v", card)
	}
	all := e.want(http.StatusOK, "admin", "GET", "/api/libraries/summary", nil)
	if n := len(all["items"].([]any)); n != 2 {
		t.Errorf("admin summary has %d libraries", n)
	}
	// the libraries handler's {id} still answers beside it
	e.want(http.StatusOK, "admin", "GET", "/api/libraries/"+acLibA, nil)

	// pins
	e.want(http.StatusNotFound, "member", "PUT", "/api/me/pinned-libraries", map[string]any{"library_ids": []string{acLibB}})
	pins := e.want(http.StatusOK, "member", "PUT", "/api/me/pinned-libraries", map[string]any{"library_ids": []string{acLibA, acLibA}})
	if ids := pins["library_ids"].([]any); len(ids) != 1 || ids[0] != acLibA {
		t.Errorf("pins %v", pins)
	}

	// home: library A's rows only
	home := e.want(http.StatusOK, "member", "GET", "/api/home", nil)
	assertOnlyLib(t, "home continue", home["continue_watching"], acLibA, 1)
	assertOnlyLib(t, "home recently added", home["recently_added"], acLibA, 3)
	pinned := home["pinned"].([]any)
	if len(pinned) != 1 {
		t.Fatalf("home pinned %v", home["pinned"])
	}
	assertOnlyLib(t, "pinned recently added", pinned[0].(map[string]any)["recently_added"], acLibA, 3)

	// hub: B is 404 to the member, A has its rows
	e.want(http.StatusNotFound, "member", "GET", "/api/libraries/"+acLibB+"/hub", nil)
	hub := e.want(http.StatusOK, "member", "GET", "/api/libraries/"+acLibA+"/hub", nil)
	rows := hub["rows"].(map[string]any)
	assertOnlyLib(t, "hub most watched", rows["most_watched"], acLibA, 1)
	if ys := rows["years"].([]any); len(ys) < 2 || ys[0].(map[string]any)["year"] != float64(now.Year()) {
		t.Errorf("years %v", ys)
	}
	bhub := e.want(http.StatusOK, "admin", "GET", "/api/libraries/"+acLibB+"/hub", nil)
	if s := bhub["rows"].(map[string]any)["series"].([]any); len(s) != 1 {
		t.Errorf("B series %v", s)
	}

	// the extended list: every sort pages, a B series lists nothing to the member
	for _, sort := range []string{"published_at", "duration_sec", "plays"} {
		for _, order := range []string{"asc", "desc"} {
			seen := map[string]bool{}
			q := url.Values{"sort": {sort}, "order": {order}, "limit": {"1"}}
			for range 10 {
				page := e.want(http.StatusOK, "admin", "GET", "/api/videos?"+q.Encode(), nil)
				for _, it := range page["items"].([]any) {
					id := it.(map[string]any)["id"].(string)
					if seen[id] {
						t.Fatalf("%s %s: %s repeated", sort, order, id)
					}
					seen[id] = true
				}
				next, _ := page["next"].(string)
				if next == "" {
					break
				}
				q.Set("cursor", next)
			}
			if len(seen) != 5 {
				t.Errorf("%s %s: %d rows, want 5", sort, order, len(seen))
			}
		}
	}
	list := e.want(http.StatusOK, "member", "GET", "/api/videos?series="+seriesB, nil)
	if n := len(list["items"].([]any)); n != 0 {
		t.Errorf("member lists %d videos of a B series", n)
	}
	list = e.want(http.StatusOK, "member", "GET", "/api/videos?watched=in_progress&year="+fmt.Sprint(now.Year()), nil)
	assertOnlyLib(t, "member in progress", list["items"], acLibA, 1)

	// anonymous
	for _, path := range []string{"/api/libraries/summary", "/api/home", "/api/me/pinned-libraries", "/api/libraries/" + acLibA + "/hub"} {
		if code, _ := e.do("", "GET", path, nil); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("anonymous %s: %d", path, code)
		}
	}
}

func assertOnlyLib(t *testing.T, what string, row any, lib string, n int) {
	t.Helper()
	cards, _ := row.([]any)
	if len(cards) != n {
		t.Errorf("%s: %d cards, want %d: %v", what, len(cards), n, row)
	}
	for _, c := range cards {
		if got := c.(map[string]any)["library_id"]; got != lib {
			t.Errorf("%s: a card of library %v", what, got)
		}
	}
}
