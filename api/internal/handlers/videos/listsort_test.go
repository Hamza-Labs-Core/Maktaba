package videos

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const (
	extLibA    = "00000000-0000-4000-8000-00000000000a"
	extLibB    = "00000000-0000-4000-8000-00000000000b"
	extUser    = "00000000-0000-4000-8000-0000000000f1"
	extSeriesA = "00000000-0000-4000-8000-0000000005a1"
	extSeriesB = "00000000-0000-4000-8000-0000000005b1"
)

// extVideo is one fixture row; a nil pointer is a NULL column.
type extVideo struct {
	id       string
	lib      string
	title    string
	mtime    *time.Time
	duration *float64
	plays    int
	// published is its YouTube upload date (videos.published_at): the
	// publish date over mtime when set
	published *time.Time
}

// publishDate is the date the list sorts and filters by: published, else mtime.
func (v extVideo) publishDate() *time.Time {
	if v.published != nil {
		return v.published
	}
	return v.mtime
}

func tp(y, m, d int) *time.Time {
	t := time.Date(y, time.Month(m), d, 12, 0, 0, 0, time.UTC)
	return &t
}

func fp(f float64) *float64 { return &f }

func xvid(n int) string {
	return "00000000-0000-4000-8000-0000000000" + string(rune('a'+n/10)) + string(rune('0'+n%10))
}

// extFixture: library A has rows with NULL mtimes and durations, ties on
// duration and plays; library B has one of everything the filters match.
// alpha, charlie and foxtrot have a YouTube upload date: alpha's file is from
// 2024 but it came out in 2016, foxtrot's is from 2025 and it came out in 2024.
func extFixture() []extVideo {
	return []extVideo{
		{xvid(1), extLibA, "alpha", tp(2024, 3, 1), fp(60), 3, tp(2016, 2, 2)},
		{xvid(2), extLibA, "bravo", tp(2023, 7, 9), fp(60), 0, nil},
		{xvid(3), extLibA, "charlie", nil, fp(120), 1, tp(2020, 8, 8)},
		{xvid(4), extLibA, "delta", tp(2024, 12, 31), nil, 3, nil},
		{xvid(5), extLibA, "echo", nil, nil, 0, nil},
		{xvid(6), extLibA, "foxtrot", tp(2025, 1, 1), fp(5), 2, tp(2024, 5, 5)},
		{xvid(7), extLibA, "golf", tp(2024, 1, 1), fp(30), 0, nil},
		{xvid(8), extLibB, "hotel", tp(2024, 6, 1), fp(90), 9, nil},
		{xvid(9), extLibB, "india", tp(2022, 6, 1), fp(10), 0, nil},
	}
}

func extDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`CREATE TABLE videos (
		id TEXT PRIMARY KEY, library_id TEXT, path TEXT, filename TEXT,
		title TEXT, description TEXT, state TEXT, detected_language TEXT,
		duration_sec REAL, size_bytes INTEGER, content_hash TEXT, metadata BLOB,
		created_at DATETIME, updated_at DATETIME, mtime DATETIME, published_at DATETIME)`)
	exec(`CREATE TABLE playback_state (user_id TEXT, video_id TEXT, position_sec REAL, completed BOOLEAN, updated_at DATETIME)`)
	exec(`CREATE TABLE watch_sessions (id TEXT, user_id TEXT, video_id TEXT, started_at DATETIME)`)
	exec(`CREATE TABLE series_episodes (series_id TEXT, video_id TEXT)`)
	exec(`CREATE TABLE user_favorites (user_id TEXT, video_id TEXT, created_at TEXT)`)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, v := range extFixture() {
		var mt, pub, dur any
		if v.mtime != nil {
			mt = *v.mtime
		}
		if v.published != nil {
			pub = *v.published
		}
		if v.duration != nil {
			dur = *v.duration
		}
		ts := base.Add(time.Duration(i) * time.Minute)
		exec(`INSERT INTO videos VALUES ($1,$2,'p','f',$3,NULL,'ready',NULL,$4,0,$5,NULL,$6,$6,$7,$8)`,
			v.id, v.lib, v.title, dur, "h"+v.id, ts, mt, pub)
		for j := 0; j < v.plays; j++ {
			exec(`INSERT INTO watch_sessions VALUES ($1,'someone',$2,$3)`, v.id+string(rune('a'+j)), v.id, ts)
		}
	}
	// the caller's playback: 1 watched, 2 in progress, 3 opened but at 0,
	// 8 (library B) watched; another user's state on 4 is not theirs
	exec(`INSERT INTO playback_state VALUES ($1,$2,0,1,$5), ($1,$3,40,0,$5), ($1,$4,0,0,$5), ($1,$6,0,1,$5), ('other',$7,10,1,$5)`,
		extUser, xvid(1), xvid(2), xvid(3), base, xvid(8), xvid(4))
	exec(`INSERT INTO series_episodes VALUES ($1,$2), ($1,$3), ($4,$5)`, extSeriesA, xvid(1), xvid(6), extSeriesB, xvid(8))
	return db
}

func extPage(t *testing.T, h *Handler, p *principal.Principal, qs url.Values) (ids []string, next *string, code int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/videos?"+qs.Encode(), nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusOK {
		return nil, nil, rr.Code
	}
	var body struct {
		Items []Video `json:"items"`
		Next  *string `json:"next"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, v := range body.Items {
		ids = append(ids, v.ID)
	}
	return ids, body.Next, rr.Code
}

func extWalk(t *testing.T, h *Handler, p *principal.Principal, qs url.Values) []string {
	t.Helper()
	var all []string
	qs.Set("limit", "2")
	for range 20 {
		ids, next, code := extPage(t, h, p, qs)
		if code != http.StatusOK {
			t.Fatalf("%v: status %d", qs, code)
		}
		all = append(all, ids...)
		if next == nil {
			return all
		}
		qs.Set("cursor", *next)
	}
	t.Fatal("pagination did not end")
	return nil
}

func admin() *principal.Principal {
	return &principal.Principal{UserID: extUser, IsAdmin: true, AccessAllLibraries: true}
}

func viewerA() *principal.Principal {
	return &principal.Principal{UserID: extUser, Libraries: []string{extLibA}}
}

// expectedOrder sorts the fixture as the list must: the key in the
// direction, NULL keys last, the id breaking ties in the direction.
func expectedOrder(rows []extVideo, sortBy, order string) []string {
	type keyed struct {
		id   string
		null bool
		key  float64
		text string
	}
	var ks []keyed
	for _, v := range rows {
		k := keyed{id: v.id}
		switch sortBy {
		case "published_at":
			if d := v.publishDate(); d == nil {
				k.null = true
			} else {
				k.key = float64(d.Unix())
			}
		case "duration_sec":
			if v.duration == nil {
				k.null = true
			} else {
				k.key = *v.duration
			}
		case "plays":
			k.key = float64(v.plays)
		case "title":
			k.text = v.title
		}
		ks = append(ks, k)
	}
	asc := order == "asc"
	sort.SliceStable(ks, func(i, j int) bool {
		a, b := ks[i], ks[j]
		if a.null != b.null {
			return !a.null
		}
		less := func(x, y bool, eq bool) bool {
			if eq {
				if asc {
					return a.id < b.id
				}
				return a.id > b.id
			}
			if asc {
				return x
			}
			return y
		}
		if sortBy == "title" {
			return less(a.text < b.text, a.text > b.text, a.text == b.text)
		}
		if a.null {
			return less(false, false, true)
		}
		return less(a.key < b.key, a.key > b.key, a.key == b.key)
	})
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.id
	}
	return out
}

// Every new sort pages in both directions without repeats or gaps, in the
// right order, with NULL keys last whatever the direction.
func TestListSortsPageBothDirections(t *testing.T) {
	h := &Handler{DB: extDB(t)}
	rows := extFixture()
	for _, sortBy := range []string{"published_at", "duration_sec", "plays", "title"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(sortBy+"/"+order, func(t *testing.T) {
				got := extWalk(t, h, admin(), url.Values{"sort": {sortBy}, "order": {order}})
				if want := expectedOrder(rows, sortBy, order); !slices.Equal(got, want) {
					t.Fatalf("order\n got %v\nwant %v", got, want)
				}
			})
		}
	}
}

func TestListCursorCarriesSortAndOrder(t *testing.T) {
	h := &Handler{DB: extDB(t)}
	_, next, _ := extPage(t, h, admin(), url.Values{"sort": {"duration_sec"}, "limit": {"1"}})
	if next == nil {
		t.Fatal("want a next cursor")
	}
	for name, qs := range map[string]url.Values{
		"other order": {"sort": {"duration_sec"}, "order": {"asc"}, "cursor": {*next}},
		"other sort":  {"sort": {"published_at"}, "cursor": {*next}},
		"bad order":   {"order": {"sideways"}},
		"bad sort":    {"sort": {"rating"}},
		"bad year":    {"year": {"twenty"}},
		"bad watched": {"watched": {"maybe"}},
		"bad series":  {"series": {"x"}},
		"null on a non-null sort": {"sort": {"plays"}, "cursor": {encodeCursorRaw(t,
			listCursor{Sort: "plays", Order: "desc", Null: true, ID: xvid(1)})}},
	} {
		if _, _, code := extPage(t, h, admin(), qs); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	// the same sort and order go on
	if _, _, code := extPage(t, h, admin(), url.Values{"sort": {"duration_sec"}, "order": {"desc"}, "cursor": {*next}}); code != http.StatusOK {
		t.Errorf("same sort and order: status %d", code)
	}
}

func encodeCursorRaw(t *testing.T, c listCursor) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return encodeB64(b)
}

func TestListFilters(t *testing.T) {
	h := &Handler{DB: extDB(t)}
	for _, tc := range []struct {
		name string
		p    *principal.Principal
		qs   url.Values
		want []string
	}{
		// the year of the publish date: alpha (a 2024 file) came out in 2016,
		// foxtrot (a 2025 file) in 2024
		{"year", admin(), url.Values{"year": {"2024"}}, []string{xvid(4), xvid(6), xvid(7), xvid(8)}},
		{"year, viewer of A", viewerA(), url.Values{"year": {"2024"}}, []string{xvid(4), xvid(6), xvid(7)}},
		{"year of an upload date", admin(), url.Values{"year": {"2016"}}, []string{xvid(1)}},
		{"year of an mtime, no upload date", admin(), url.Values{"year": {"2023"}}, []string{xvid(2)}},
		{"watched", admin(), url.Values{"watched": {"watched"}}, []string{xvid(1), xvid(8)}},
		{"watched, viewer of A", viewerA(), url.Values{"watched": {"watched"}}, []string{xvid(1)}},
		{"in progress", admin(), url.Values{"watched": {"in_progress"}}, []string{xvid(2)}},
		{"unwatched, viewer of A", viewerA(), url.Values{"watched": {"unwatched"}},
			[]string{xvid(3), xvid(4), xvid(5), xvid(6), xvid(7)}},
		{"series", admin(), url.Values{"series": {extSeriesA}}, []string{xvid(1), xvid(6)}},
		{"series of B, admin", admin(), url.Values{"series": {extSeriesB}}, []string{xvid(8)}},
		{"series of B, viewer of A", viewerA(), url.Values{"series": {extSeriesB}}, nil},
		{"plays, viewer of A", viewerA(), url.Values{"sort": {"plays"}},
			expectedOrder(extFixture()[:7], "plays", "desc")},
		// the plays are counted over the filtered videos: the filters run
		// inside the count too
		{"plays, unwatched, viewer of A", viewerA(), url.Values{"sort": {"plays"}, "watched": {"unwatched"}},
			expectedOrder(extFixture()[2:7], "plays", "desc")},
		{"plays, series A", admin(), url.Values{"sort": {"plays"}, "series": {extSeriesA}, "order": {"asc"}},
			[]string{xvid(6), xvid(1)}},
		{"year and watched", admin(), url.Values{"year": {"2024"}, "watched": {"unwatched"}}, []string{xvid(4), xvid(6), xvid(7)}},
		// the admin token has no user row: nothing is watched
		{"no user", &principal.Principal{UserID: "admin-token", AccessAllLibraries: true}, url.Values{"watched": {"watched"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := extWalk(t, h, tc.p, tc.qs)
			if tc.qs.Get("sort") == "" {
				slices.Sort(got)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// A caller with no library sees nothing whatever the filter.
func TestListNoLibraries(t *testing.T) {
	h := &Handler{DB: extDB(t)}
	ids, _, code := extPage(t, h, &principal.Principal{UserID: extUser}, url.Values{"series": {extSeriesA}})
	if code != http.StatusOK || len(ids) != 0 {
		t.Fatalf("status %d, ids %v", code, ids)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/videos", nil)
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("anonymous: status %d, want 403", rr.Code)
	}
}

func encodeB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
