package router_test

// Epic 57 story 57.01: the age-tier routes behind the production router, against a real
// migrated Postgres (MAKTABA_TEST_PG_URL): who may call each, what each stores, and what the
// recompute then writes on the videos.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

type tiersEnv struct {
	t  *testing.T
	db *sql.DB
	h  http.Handler
	rc *parental.Recomputer
}

func setupTiers(t *testing.T) *tiersEnv {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	rc := router.MountP57Tiers(r, router.P57TiersDeps{DB: db})
	if rc == nil {
		t.Fatal("MountP57Tiers answered no recompute")
	}
	if _, err := rc.Drain(context.Background()); err != nil { // the migration's own job
		t.Fatal(err)
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "admin":
			p = &principal.Principal{UserID: uuid.NewString(), IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceCookie}
		case "viewer":
			p = &principal.Principal{UserID: uuid.NewString(), AccessAllLibraries: true, Source: principal.SourceCookie}
		case "token":
			p = &principal.Principal{UserID: uuid.NewString(), AccessAllLibraries: true, Source: principal.SourcePAT}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return &tiersEnv{t: t, db: db, h: h, rc: rc}
}

func (e *tiersEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *tiersEnv) video(lib string) string {
	e.t.Helper()
	id := uuid.NewString()
	e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
		VALUES ($1::uuid, $2::uuid, '/media/v/' || $1::text || '.mp4', $1::text || '.mp4',
		        encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), 1000, now(), 'ready', 't', 100, now(), now())`, id, lib)
	return id
}

func (e *tiersEnv) do(who, method, path, body string) (int, map[string]any, http.Header) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-As", who)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out, rec.Header()
}

// put asks as the admin and checks the answer's tier (-1: null), source and count.
func (e *tiersEnv) put(path, body string, tier int, source string, count int) {
	e.t.Helper()
	code, out, _ := e.do("admin", http.MethodPut, path, body)
	if code != http.StatusOK {
		e.t.Fatalf("PUT %s %s as admin: %d %v", path, body, code, out)
	}
	got := -1
	if n, ok := out["tier"].(float64); ok {
		got = int(n)
	}
	gotSource, _ := out["source"].(string)
	if got != tier || gotSource != source || out["recompute_count"] != float64(count) {
		e.t.Fatalf("PUT %s %s = %v, want tier %d from %q over %d videos", path, body, out, tier, source, count)
	}
}

// want checks a video's stored tier (-1: unrated) and source after the queue is worked.
func (e *tiersEnv) want(video string, tier int, source string) {
	e.t.Helper()
	if _, err := e.rc.Drain(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	var (
		gotTier   sql.NullInt64
		gotSource sql.NullString
	)
	if err := e.db.QueryRow(`SELECT age_tier, age_tier_source FROM videos WHERE id = $1`, video).Scan(&gotTier, &gotSource); err != nil {
		e.t.Fatal(err)
	}
	got := -1
	if gotTier.Valid {
		got = int(gotTier.Int64)
	}
	if got != tier || gotSource.String != source {
		e.t.Fatalf("video tier = %d from %q, want %d from %q", got, gotSource.String, tier, source)
	}
}

// T08 and the acceptance criteria, through the router: each route as no one, a viewer, a
// viewer's API token and the admin; what the admin's change stores; what the videos become.
func TestP57TiersPG(t *testing.T) {
	t.Parallel()
	e := setupTiers(t)
	lib, other := uuid.NewString(), uuid.NewString()
	e.exec(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'tiers', '{/media/tiers}'), ($2, 'other', '{/media/other}')`, lib, other)
	v, v2 := e.video(lib), e.video(lib)
	ser, col, ch, src := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	e.exec(`INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'show')`, ser, lib)
	e.exec(`INSERT INTO series_episodes (series_id, video_id) VALUES ($1, $2)`, ser, v)
	e.exec(`INSERT INTO collections (id, library_id, name) VALUES ($1, $2, 'picks')`, col, lib)
	e.exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1, $2, 0), ($1, $3, 1)`, col, v, v2)
	e.exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 957, 'c', 'c-957', 'shuffle')`, ch)
	e.exec(`INSERT INTO youtube_sources (id, library_id, kind, url, external_id) VALUES ($1, $2, 'channel', 'https://example.test/c', 'UC57')`, src, lib)
	e.exec(`INSERT INTO youtube_downloads (library_id, youtube_id, source_id, video_id) VALUES ($1, 'yt57', $2, $3)`, lib, src, v)

	routes := []string{
		"/api/videos/" + v + "/age-tier",
		"/api/libraries/" + lib + "/age-tier",
		"/api/series/" + ser + "/age-tier",
		"/api/collections/" + col + "/age-tier",
		"/api/channels/" + ch + "/age-tier",
		"/api/libraries/" + lib + "/youtube/sources/" + src + "/age-tier",
	}
	for _, path := range routes {
		for who, want := range map[string]int{"": http.StatusUnauthorized, "viewer": http.StatusForbidden, "token": http.StatusForbidden} {
			if code, _, _ := e.do(who, http.MethodPut, path, `{"tier":18}`); code != want {
				t.Errorf("PUT %s as %q: %d, want %d", path, who, code, want)
			}
		}
	}
	var stored int
	if err := e.db.QueryRow(`SELECT (SELECT count(*) FROM videos WHERE age_tier_admin IS NOT NULL)
		+ (SELECT count(*) FROM libraries WHERE age_tier IS NOT NULL) + (SELECT count(*) FROM series WHERE age_tier IS NOT NULL)
		+ (SELECT count(*) FROM collections WHERE age_tier IS NOT NULL) + (SELECT count(*) FROM channels WHERE age_tier IS NOT NULL)
		+ (SELECT count(*) FROM youtube_sources WHERE age_tier IS NOT NULL) + (SELECT count(*) FROM parental_recompute_jobs)`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("a refused caller stored %d tiers or jobs", stored)
	}

	// the ratings table: any signed-in user, no one else
	if code, _, _ := e.do("", http.MethodGet, "/api/parental/ratings", ""); code != http.StatusUnauthorized {
		t.Errorf("GET ratings as no one: %d, want 401", code)
	}
	code, table, hdr := e.do("viewer", http.MethodGet, "/api/parental/ratings", "")
	if code != http.StatusOK || table["systems"] == nil || table["tiers"] == nil {
		t.Fatalf("GET ratings as a viewer: %d %v", code, table)
	}
	if hdr.Get("Cache-Control") != "private, max-age=300" {
		t.Errorf("ratings Cache-Control = %q", hdr.Get("Cache-Control"))
	}

	// the library's default reaches both videos
	e.put(routes[1], `{"tier":9}`, 9, "library_default", 2)
	e.want(v, 9, "library_default")
	e.want(v2, 9, "library_default")
	// a YouTube source's tier: the one video downloaded from it
	e.put(routes[5], `{"tier":13}`, 13, "admin_container", 1)
	e.want(v, 13, "admin_container")
	e.want(v2, 9, "library_default")
	// a collection's and a series' tier: the strictest container wins
	e.put(routes[3], `{"tier":0}`, 0, "admin_container", 2)
	e.want(v, 13, "admin_container")
	e.want(v2, 0, "admin_container")
	e.put(routes[2], `{"tier":18}`, 18, "admin_container", 1)
	e.want(v, 18, "admin_container")
	// the admin's tier on the video is written before the answer
	e.put(routes[0], `{"tier":16}`, 16, "admin_video", 1)
	var now sql.NullInt64
	if err := e.db.QueryRow(`SELECT age_tier FROM videos WHERE id = $1`, v).Scan(&now); err != nil || now.Int64 != 16 {
		t.Fatalf("the video's tier after the answer = %v (%v), want 16", now, err)
	}
	// cleared: the answer is what the video falls back to
	e.put(routes[0], `{"tier":null}`, 18, "admin_container", 1)
	e.put(routes[2], `{"tier":null}`, -1, "", 1)
	e.want(v, 13, "admin_container")
	// a channel keeps its tier for itself
	e.put(routes[4], `{"tier":16}`, 16, "admin_container", 0)
	e.want(v, 13, "admin_container")
	var chTier int
	if err := e.db.QueryRow(`SELECT age_tier FROM channels WHERE id = $1`, ch).Scan(&chTier); err != nil || chTier != 16 {
		t.Fatalf("the channel's tier = %d (%v), want 16", chTier, err)
	}

	// what the admin is refused
	for path, want := range map[string]int{
		"/api/videos/" + uuid.NewString() + "/age-tier":                                http.StatusNotFound,
		"/api/series/" + uuid.NewString() + "/age-tier":                                http.StatusNotFound,
		"/api/libraries/" + other + "/youtube/sources/" + src + "/age-tier":            http.StatusNotFound, // another library's source
		"/api/libraries/" + lib + "/youtube/sources/" + uuid.NewString() + "/age-tier": http.StatusNotFound,
		"/api/collections/not-an-id/age-tier":                                          http.StatusBadRequest,
	} {
		if code, _, _ := e.do("admin", http.MethodPut, path, `{"tier":9}`); code != want {
			t.Errorf("PUT %s as admin: %d, want %d", path, code, want)
		}
	}
	for _, body := range []string{`{"tier":12}`, `{}`, `{"tier":"13"}`, `{"tier":9,"extra":1}`, `not json`} {
		if code, _, _ := e.do("admin", http.MethodPut, routes[1], body); code != http.StatusBadRequest {
			t.Errorf("PUT %s as admin: %d, want 400", body, code)
		}
	}
	e.want(v, 13, "admin_container")
}
