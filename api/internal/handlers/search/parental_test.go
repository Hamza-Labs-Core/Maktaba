package search

// Epic 57 story 57.03, test case T04: search under the parental filter. A hidden
// video is in no list, no total, no snippet and no count, in every mode; a search
// kept before a block loses the blocked video on its next read; recent searches
// are counted again. On Postgres (MAKTABA_TEST_PG_URL) and on SQLite.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

const (
	pfChild   = "00000000-0000-4000-8000-0000000000c1"
	pfWord    = "zarqa"
	pfHiTitle = "Zarqa-Teen-Title"
)

// underLimit is h with the limit e points to on every request (as the gate
// leaves it in the context); the test swaps the limit to change the policy.
func underLimit(h http.Handler, e *atomic.Pointer[parental.Effective]) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(parental.With(r.Context(), e.Load())))
	})
}

func nine() *parental.Effective {
	return &parental.Effective{UserID: pfChild, Restricted: true, MaxTier: parental.Tier9, Unrated: parental.UnratedHide, Version: 1}
}

type ytCounting struct{ n atomic.Int32 }

func (y *ytCounting) SearchYouTube(context.Context, string) ([]YTItem, error) {
	y.n.Add(1)
	return []YTItem{{}}, nil
}

// searchFilterCase runs T04 on db: vOK (all ages), vHi (16+) and vUnr (unrated)
// each have the word in their title and in two transcript segments; sem is the
// meaning search's answer (a passage of each).
func searchFilterCase(t *testing.T, db *sql.DB, vOK, vHi, vUnr string, sem []Hit) {
	t.Helper()
	child := &principal.Principal{UserID: pfChild, Libraries: []string{libA}}
	viewer := &principal.Principal{UserID: userB, Libraries: []string{libA}}
	yt := &ytCounting{}
	limit := &atomic.Pointer[parental.Effective]{}
	limit.Store(nine())
	base := router(&Handler{DB: db, NowFunc: time.Now, Semantic: fixedSemantic{hits: sem}, YouTube: yt})
	h := underLimit(base, limit)

	hidden := func(what, body string) {
		t.Helper()
		for _, s := range []string{vHi, vUnr, pfHiTitle, "Unrated-Zarqa", "teen-secret", "unrated-secret"} {
			if strings.Contains(body, s) {
				t.Errorf("%s names %s: %s", what, s, body)
			}
		}
	}
	for _, mode := range []string{"fts", "semantic", "hybrid"} {
		rec := call(t, h, child, http.MethodPost, "/api/search", `{"q":"`+pfWord+`","mode":"`+mode+`","view":"list","include":["youtube"]}`)
		resp := decode[Response](t, rec, 200)
		hidden("child "+mode, rec.Body.String())
		if resp.VideosTotal != 1 || len(resp.Videos) != 1 || resp.Videos[0].VideoID != vOK {
			t.Errorf("%s: titles %d %+v, want the one allowed video", mode, resp.VideosTotal, resp.Videos)
		}
		if resp.TranscriptTotal != 1 || len(resp.TranscriptVideos) != 1 || resp.TranscriptVideos[0].VideoID != vOK {
			t.Errorf("%s: transcripts %d %+v, want the one allowed video", mode, resp.TranscriptTotal, resp.TranscriptVideos)
		}
		for _, hit := range resp.Hits {
			if hit.VideoID != vOK {
				t.Errorf("%s: a hit of %s", mode, hit.VideoID)
			}
		}
		if resp.YouTube == nil || len(resp.YouTube.Items) != 0 || resp.YouTube.Reason != "disabled" {
			t.Errorf("%s: the YouTube block of a restricted account: %+v", mode, resp.YouTube)
		}
		// pages, and one video's passages: a hidden video answers as one not in the search
		for _, list := range []string{"titles", "transcripts"} {
			prec := call(t, h, child, http.MethodGet, "/api/search/results/"+resp.SearchID+"?list="+list, "")
			page := decode[resultsPage](t, prec, 200)
			hidden("child "+mode+" "+list, prec.Body.String())
			if page.Total != 1 {
				t.Errorf("%s %s: total %d, want 1", mode, list, page.Total)
			}
		}
		missing := call(t, h, child, http.MethodGet, "/api/search/passages?sid="+resp.SearchID+"&video_id=30000000-0000-4000-8000-00000000dead", "")
		for _, id := range []string{vHi, vUnr} {
			got := call(t, h, child, http.MethodGet, "/api/search/passages?sid="+resp.SearchID+"&video_id="+id, "")
			if got.Code != http.StatusNotFound || got.Code != missing.Code ||
				strings.ReplaceAll(got.Body.String(), id, "ID") != strings.ReplaceAll(missing.Body.String(), "30000000-0000-4000-8000-00000000dead", "ID") {
				t.Errorf("%s: passages of a hidden video: %d %s; of one not in the search: %d %s", mode, got.Code, got.Body.String(), missing.Code, missing.Body.String())
			}
		}
		if got := call(t, h, child, http.MethodGet, "/api/search/passages?sid="+resp.SearchID+"&video_id="+vOK, ""); got.Code != http.StatusOK {
			t.Errorf("%s: passages of the allowed video: %d %s", mode, got.Code, got.Body.String())
		}
	}
	if n := yt.n.Load(); n != 0 {
		t.Errorf("YouTube was asked %d times for a restricted account", n)
	}

	// the viewer and the admin find all three, and YouTube is asked for them
	for _, p := range []*principal.Principal{viewer, admin} {
		rec := call(t, base, p, http.MethodPost, "/api/search", `{"q":"`+pfWord+`","mode":"hybrid","view":"list","include":["youtube"]}`)
		resp := decode[Response](t, rec, 200)
		if resp.VideosTotal != 3 || resp.TranscriptTotal != 3 || resp.YouTube == nil || len(resp.YouTube.Items) != 1 {
			t.Errorf("%s: titles %d, transcripts %d, YouTube %+v; want 3, 3 and one item", p.UserID, resp.VideosTotal, resp.TranscriptTotal, resp.YouTube)
		}
	}
	// no one is refused
	if rec := httptest.NewRecorder(); true {
		base.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/search", strings.NewReader(`{"q":"`+pfWord+`"}`)))
		if rec.Code/100 == 2 || strings.Contains(rec.Body.String(), pfHiTitle) {
			t.Errorf("search as no one: %d %s", rec.Code, rec.Body.String())
		}
	}

	// a search kept under a wider limit, then the limit tightens (a block, a lower
	// tier): the kept result is filtered on its next read, and recent is recounted
	wide := nine()
	wide.MaxTier = parental.Tier18
	limit.Store(wide)
	rec := call(t, h, child, http.MethodPost, "/api/search", `{"q":"`+pfWord+`","mode":"hybrid","view":"list"}`)
	kept := decode[Response](t, rec, 200)
	if kept.VideosTotal != 2 || kept.TranscriptTotal != 2 || !strings.Contains(rec.Body.String(), vHi) || strings.Contains(rec.Body.String(), vUnr) {
		t.Fatalf("under an 18+ limit: titles %d, transcripts %d: %s", kept.VideosTotal, kept.TranscriptTotal, rec.Body.String())
	}
	recent := decode[recentList](t, call(t, h, child, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(recent) == 0 || recent[0].SearchID != kept.SearchID || recent[0].TitleCount != 2 || recent[0].TranscriptCount != 2 {
		t.Fatalf("recent under an 18+ limit: %+v", recent)
	}
	blocked := nine()
	blocked.MaxTier = parental.Tier18
	blocked.Rules = []parental.Rule{{Effect: parental.EffectBlock, SubjectKind: parental.SubjectVideo, SubjectID: vHi}}
	limit.Store(blocked)
	for _, list := range []string{"titles", "transcripts"} {
		prec := call(t, h, child, http.MethodGet, "/api/search/results/"+kept.SearchID+"?list="+list, "")
		page := decode[resultsPage](t, prec, 200)
		hidden("after the block, "+list, prec.Body.String())
		if page.Total != 1 {
			t.Errorf("after the block, %s: total %d, want 1", list, page.Total)
		}
	}
	if got := call(t, h, child, http.MethodGet, "/api/search/passages?sid="+kept.SearchID+"&video_id="+vHi, ""); got.Code != http.StatusNotFound {
		t.Errorf("after the block, the blocked video's passages: %d %s", got.Code, got.Body.String())
	}
	recent = decode[recentList](t, call(t, h, child, http.MethodGet, "/api/search/recent", ""), 200).Items
	for _, it := range recent {
		if it.Live && (it.TitleCount != 1 || it.TranscriptCount != 1) {
			t.Errorf("after the block, recent counts %d titles and %d transcripts for %s, want 1 and 1", it.TitleCount, it.TranscriptCount, it.SearchID)
		}
	}
	// an allow shows a 16+ video under the 9+ limit, everywhere the all-ages one is
	allowed := nine()
	allowed.Rules = []parental.Rule{{Effect: parental.EffectAllow, SubjectKind: parental.SubjectVideo, SubjectID: vHi}}
	limit.Store(allowed)
	resp := decode[Response](t, call(t, h, child, http.MethodPost, "/api/search", `{"q":"`+pfWord+`","mode":"hybrid","view":"list"}`), 200)
	if resp.VideosTotal != 2 || resp.TranscriptTotal != 2 {
		t.Errorf("with an allow rule: titles %d, transcripts %d, want 2 and 2", resp.VideosTotal, resp.TranscriptTotal)
	}

	// suggestions are the caller's own earlier words, never a title
	srec := call(t, h, child, http.MethodGet, "/api/search/suggest?q=Zar", "")
	if srec.Code != http.StatusOK {
		t.Errorf("suggest: %d", srec.Code)
	}
	hidden("suggest", srec.Body.String())
}

func TestSearchUnderParentalFilter_Postgres(t *testing.T) {
	db := pgSearchDB(t)
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash) VALUES ($1, 'search-child', 'x')`, pfChild); err != nil {
		t.Fatal(err)
	}
	vOK, vHi, vUnr := pgUUID(1), pgUUID(2), pgUUID(3)
	var sem []Hit
	for i, v := range []struct {
		id, title, text string
		tier            any
	}{{vOK, "Kid Zarqa", "kid-safe zarqa", 0}, {vHi, pfHiTitle + " zarqa", "teen-secret zarqa", 16}, {vUnr, "Unrated-Zarqa zarqa", "unrated-secret zarqa", nil}} {
		tr := pgVideo(t, db, v.id, libA, v.title)
		if _, err := db.Exec(`UPDATE videos SET age_tier = $2 WHERE id = $1`, v.id, v.tier); err != nil {
			t.Fatal(err)
		}
		pgTranscript(t, db, pgUUID(11+i), v.id, tr, true, v.text, "nothing", v.text+" again")
		var seg int64
		if err := db.QueryRow(`SELECT s.id FROM transcript_segments s JOIN transcripts t ON t.id = s.transcript_id WHERE t.video_id = $1 ORDER BY s.seq LIMIT 1`, v.id).Scan(&seg); err != nil {
			t.Fatal(err)
		}
		sem = append(sem, Hit{SegmentID: seg, VideoID: v.id, StartSec: 0, EndSec: 9, Snippet: v.text, Score: 0.9 - float64(i)/10})
	}
	searchFilterCase(t, db, vOK, vHi, vUnr, sem)
}

// The same on SQLite: the predicate is the same text on both stores.
func TestSearchUnderParentalFilter_SQLite(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.exec(`ALTER TABLE videos ADD COLUMN age_tier INTEGER`)
	f.exec(`CREATE TABLE search_history (query TEXT, query_norm TEXT, user_id TEXT, hits INTEGER, last_used_at TEXT)`)
	f.library(libA, "Lessons")
	vOK, vHi, vUnr := pgUUID(1), pgUUID(2), pgUUID(3)
	var sem []Hit
	for i, v := range []struct {
		id, title, text string
		tier            any
	}{{vOK, "Kid Zarqa", "kid-safe zarqa", 0}, {vHi, pfHiTitle + " zarqa", "teen-secret zarqa", 16}, {vUnr, "Unrated-Zarqa zarqa", "unrated-secret zarqa", nil}} {
		f.video(v.id, libA, v.title, 600)
		f.exec(`UPDATE videos SET age_tier = $2 WHERE id = $1`, v.id, v.tier)
		f.transcript("t"+v.id, v.id, true, v.text, "nothing", v.text+" again")
		var seg int64
		if err := db.QueryRow(`SELECT id FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq LIMIT 1`, "t"+v.id).Scan(&seg); err != nil {
			t.Fatal(err)
		}
		sem = append(sem, Hit{SegmentID: seg, VideoID: v.id, StartSec: 0, EndSec: 9, Snippet: v.text, Score: 0.9 - float64(i)/10})
	}
	searchFilterCase(t, db, vOK, vHi, vUnr, sem)
}

// failingSemantic is a meaning search that is down.
type failingSemantic struct{}

func (failingSemantic) Search(context.Context, string, int, Filters) ([]Hit, error) {
	return nil, errors.New("down")
}

// When the filter's check cannot be made nothing is answered: a kept search is
// refused, not served unfiltered.
func TestSearchUnderParentalFilter_FailsClosed(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "Lessons")
	v := pgUUID(1)
	f.video(v, libA, "Zarqa lesson", 600)
	f.transcript("t1", v, true, "zarqa")
	child := &principal.Principal{UserID: pfChild, Libraries: []string{libA}}
	base := router(&Handler{DB: db, NowFunc: time.Now, Semantic: failingSemantic{}})
	// kept with no limit: this store has no age_tier column, so the filter's
	// query fails for a restricted account
	kept := decode[Response](t, call(t, base, child, http.MethodPost, "/api/search", `{"q":"zarqa","mode":"fts"}`), 200)
	if kept.VideosTotal != 1 {
		t.Fatalf("kept: %+v", kept)
	}
	limit := &atomic.Pointer[parental.Effective]{}
	limit.Store(nine())
	h := underLimit(base, limit)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/search", `{"q":"zarqa","mode":"fts"}`},
		{http.MethodGet, "/api/search/results/" + kept.SearchID + "?list=titles", ""},
		{http.MethodGet, "/api/search/passages?sid=" + kept.SearchID + "&video_id=" + v, ""},
		{http.MethodGet, "/api/search/recent", ""},
	} {
		rec := call(t, h, child, c.method, c.path, c.body)
		if rec.Code < 500 || strings.Contains(rec.Body.String(), "Zarqa lesson") || strings.Contains(rec.Body.String(), v) {
			t.Errorf("%s %s with the filter unreadable: %d %s, want a refusal that names nothing", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}
