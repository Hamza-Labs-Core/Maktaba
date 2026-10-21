package vodfallback

// With the home unreachable, a video this machine can read still plays here; one it
// cannot read is "not available while the home is unreachable", never a dead end on the
// home proxy (PR #145).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func videosDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, path TEXT NOT NULL)`,
		`INSERT INTO videos VALUES ('here', '/Volumes/Originals/a.mkv'), ('gone', '/Volumes/Elsewhere/b.mkv')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestOfflineAVideoThisMachineCannotReadIsUnavailable(t *testing.T) {
	down := true
	homeCalls := 0
	homeAnswer := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"home_unreachable","status":503}`))
	}
	f := &Fallback{DB: videosDB(t),
		ToLocal: func(p string) string { return strings.Replace(p, "/Volumes/Originals/", "O:/", 1) },
		Stat: func(p string) error {
			if strings.HasPrefix(p, "O:/") {
				return nil
			}
			return errors.New("not mounted")
		},
		Home: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			homeCalls++
			homeAnswer(w)
		}),
		HomeDown: func() bool { return down },
	}
	h := f.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }))
	open := func(video string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/stream/sessions", strings.NewReader(`{"video_id":"`+video+`"}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	unavailable := func(rec *httptest.ResponseRecorder) bool {
		var p struct{ Type, Code, Title string }
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return rec.Code == http.StatusServiceUnavailable && p.Type == ProblemUnavailableOffline &&
			p.Code == "video_unavailable_offline" && strings.Contains(p.Title, "while the home is unreachable")
	}

	if rec := open("here"); rec.Code != http.StatusCreated || rec.Header().Get("X-Maktaba-VOD") != "local" {
		t.Fatalf("a readable video with the home down: %d %q, want it played here", rec.Code, rec.Header().Get("X-Maktaba-VOD"))
	}
	if rec := open("gone"); !unavailable(rec) || homeCalls != 0 {
		t.Fatalf("an unreadable video with the home down: %d %s (home asked %d), want 503 unavailable offline",
			rec.Code, rec.Body.String(), homeCalls)
	}
	if f.Current().Fallback {
		t.Fatal("no fallback to a home that does not answer is reported")
	}
	// the outage's first seconds: the home is tried and does not answer
	down = false
	if rec := open("gone"); !unavailable(rec) || homeCalls != 1 {
		t.Fatalf("the home proxy's home_unreachable: %d %s, want 503 unavailable offline", rec.Code, rec.Body.String())
	}
}

type pathCatalog struct {
	down   bool
	failed int
	paths  map[string]string
}

func (c *pathCatalog) DBDown() bool       { return c.down }
func (c *pathCatalog) DBFailed(err error) { c.failed++ }
func (c *pathCatalog) Path(id string) (string, bool) {
	p, ok := c.paths[id]
	return p, ok
}

// With the home's database down too, the path comes from this machine's catalog.
func TestOfflineTheCatalogNamesThePath(t *testing.T) {
	db := videosDB(t)
	_ = db.Close() // the home's database gone
	cat := &pathCatalog{paths: map[string]string{"here": "/Volumes/Originals/a.mkv", "gone": "/Volumes/Elsewhere/b.mkv"}}
	f := &Fallback{DB: db, Catalog: cat,
		ToLocal: func(p string) string { return strings.Replace(p, "/Volumes/Originals/", "O:/", 1) },
		Stat: func(p string) error {
			if strings.HasPrefix(p, "O:/") {
				return nil
			}
			return errors.New("not mounted")
		}}
	if ok, _ := f.Readable(context.Background(), "here"); !ok {
		t.Fatal("a video the catalog places on a readable share")
	}
	if cat.failed != 1 {
		t.Fatalf("the failed database call was not recorded: %d", cat.failed)
	}
	cat.down = true // known down: the database is not asked
	if ok, why := f.Readable(context.Background(), "gone"); ok || why == "" {
		t.Fatalf("a video the catalog places on a share this machine cannot read: %v %q", ok, why)
	}
	if ok, _ := f.Readable(context.Background(), "unknown"); !ok {
		t.Fatal("a video outside the catalog is left to the session handler")
	}
	if cat.failed != 1 {
		t.Fatalf("a database known down was asked: %d", cat.failed)
	}
}
