package router_test

// Epic 57 story 57.03: the one content filter behind the production router, against a real
// migrated Postgres (MAKTABA_TEST_PG_URL). A restricted child is shown only what its limit and
// rules allow on every surface; a hidden object answers exactly as a missing one; what a
// restricted account may not do at all answers 403 `restricted`; a policy that cannot be read
// refuses.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	pfAdmin    = "00000000-0000-4000-8000-000000573001"
	pfGuardian = "00000000-0000-4000-8000-000000573002"
	pfOtherG   = "00000000-0000-4000-8000-000000573003"
	pfChild    = "00000000-0000-4000-8000-000000573004"
	pfChild2   = "00000000-0000-4000-8000-000000573005"
	pfViewer   = "00000000-0000-4000-8000-000000573006"
	pfLibA     = "00000000-0000-4000-8000-00000573a000"
	pfLibB     = "00000000-0000-4000-8000-00000573b000"
	pfKid      = "00000000-0000-4000-8000-0000573a0001" // all ages, library A
	pfTeen     = "00000000-0000-4000-8000-0000573a0002" // 16+, library A
	pfTeen2    = "00000000-0000-4000-8000-0000573a0004" // 16+, library A, the late show's one episode
	pfUnrated  = "00000000-0000-4000-8000-0000573a0003" // no tier, library A
	pfKidB     = "00000000-0000-4000-8000-0000573b0001" // all ages, library B
	pfSeries   = "00000000-0000-4000-8000-0000573c0001" // the kid and the teen video
	pfSeries16 = "00000000-0000-4000-8000-0000573c0002" // one 16+ episode only
	pfCol      = "00000000-0000-4000-8000-0000573d0001" // the kid and the teen video
	pfCol16    = "00000000-0000-4000-8000-0000573d0002" // the teen video only
	pfTeenName = "Zarqa-Teen-Title"
)

// pfSigner signs nothing and remembers the lifetime it was asked for.
type pfSigner struct {
	mu  sync.Mutex
	ttl time.Duration
}

func (s *pfSigner) SignStreamURL(rawURL, _, _, _ string, ttl time.Duration) (string, time.Time, error) {
	s.mu.Lock()
	s.ttl = ttl
	s.mu.Unlock()
	return rawURL + "?sig=x", time.Now().Add(ttl), nil
}

// KeepTime: the signer can be told whether an account's time is kept, as
// signedurl.Minter can (story 57.14); one that cannot signs nothing for a
// restricted account.
func (s *pfSigner) KeepTime(func(string) (bool, error)) {}

type pfEnv struct {
	t      testing.TB
	db     *sql.DB
	dsn    string
	h      http.Handler
	gate   *parental.Gate
	signer *pfSigner
	hub    *ws.Hub
	// stream, when set, ends each request after that long: a live channel never ends itself
	stream time.Duration
	// as, when set, may answer another principal for a request (the bypass
	// rehearsal's mutation check takes the filter off one route with it)
	as func(req *http.Request, p *principal.Principal) *principal.Principal
	// cache is the restricted accounts' cache the home's mounts share
	cache *parental.Cache
}

// pfStandIns are the refused routes this test's router has no real mount for.
var pfStandIns = []struct{ method, path string }{
	{http.MethodPost, "/api/me/tokens"}, {http.MethodGet, "/api/me/tokens"},
	{http.MethodGet, "/api/channels/xmltv"}, {http.MethodGet, "/api/channels/playlist.m3u"},
	{http.MethodGet, "/api/plugins"}, {http.MethodGet, "/api/users"},
}

func setupFilter(t testing.TB) *pfEnv { return setupFilterWith(t, nil) }

// setupFilterWith is setupFilter with more of the home mounted: more, when set, mounts
// on the same router after MountP6 (the whole home of p57_home_pg_test.go), and the
// stand-ins step aside for the real routes it brought.
func setupFilterWith(t testing.TB, more func(r chi.Router, e *pfEnv)) *pfEnv {
	t.Helper()
	return setupFilterOn(t, more, nil)
}

// setupFilterOn is setupFilterWith over another handle of the same database: open,
// when set, gives the database the routes and the gate read (a node's pair of the
// home's database and its own copy, p57_node_pg_test.go). The test's own statements
// still go straight to the scratch database.
func setupFilterOn(t testing.TB, more func(r chi.Router, e *pfEnv), open func(dsn string) *sql.DB) *pfEnv {
	t.Helper()
	db, dsn := clustertest.ScratchDB(t)
	served := db
	if open != nil {
		served = open(dsn)
	}
	e := &pfEnv{t: t, db: db, dsn: dsn, gate: parental.NewGate(), signer: &pfSigner{}, hub: ws.NewHub()}
	e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES
		($1,'pf-admin','x',true), ($2,'pf-guardian','x',false), ($3,'pf-other-guardian','x',false),
		($4,'pf-child','x',false), ($5,'pf-child-two','x',false), ($6,'pf-viewer','x',false)`,
		pfAdmin, pfGuardian, pfOtherG, pfChild, pfChild2, pfViewer)
	e.exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'PfAlpha','{/media/pf-a}'), ($2,'PfBeta','{/media/pf-b}')`, pfLibA, pfLibB)
	for _, u := range []string{pfGuardian, pfOtherG, pfChild, pfChild2, pfViewer} {
		e.exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1,$2), ($1,$3)`, u, pfLibA, pfLibB)
	}
	for _, v := range []struct {
		id, lib, title string
		tier           any
	}{{pfKid, pfLibA, "Kid-Title", 0}, {pfTeen, pfLibA, pfTeenName, 16}, {pfTeen2, pfLibA, "Late-Episode-Title", 16}, {pfUnrated, pfLibA, "Unrated-Title", nil}, {pfKidB, pfLibB, "KidB-Title", 0}} {
		e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at, age_tier)
			VALUES ($1::uuid, $2::uuid, '/media/pf/' || $1::text || '.mp4', $1::text || '.mp4',
			        encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), 1000, now(), 'ready', $3, 100, now(), now(), $4)`, v.id, v.lib, v.title, v.tier)
	}
	e.exec(`INSERT INTO series (id, library_id, name) VALUES ($1, $3, 'Pf show'), ($2, $3, 'Pf late show')`, pfSeries, pfSeries16, pfLibA)
	e.exec(`INSERT INTO series_episodes (series_id, video_id) VALUES ($1, $3), ($1, $4), ($2, $5)`, pfSeries, pfSeries16, pfKid, pfTeen, pfTeen2)
	e.exec(`INSERT INTO collections (id, library_id, name) VALUES ($1, $3, 'Pf picks'), ($2, $3, 'Pf late picks')`, pfCol, pfCol16, pfLibA)
	e.exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1, $3, 0), ($1, $4, 1), ($2, $4, 0)`, pfCol, pfCol16, pfKid, pfTeen)
	e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1), ($2, 9, 1)`, pfChild, pfChild2)
	e.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1,$2), ($3,$4)`, pfChild, pfGuardian, pfChild2, pfOtherG)
	// a home with restricted accounts has parental controls on (story 57.12; slot 0225
	// starts them off where nothing is set up)
	e.exec(`UPDATE app_settings SET value = 'true' WHERE key = 'parental.enabled'`)

	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore(), Parental: e.gate})
	// a streaming service that keeps time limits (story 57.14): without one a
	// restricted account's stream is refused
	router.MountP6(r, router.P6Deps{DB: served, StreamingClient: &tmStreaming{leases: map[string]time.Time{}}, URLSigner: e.signer, ParentalGate: e.gate, EventHub: e.hub})
	if e.gate.Cache() == nil {
		t.Fatal("MountP6 gave the gate no cache")
	}
	// The routes MountP6 does not mount (sign-in's tokens, live TV's exports, plugin admin:
	// each needs a service this test does not start) stand here as handlers that only say
	// they were reached, so the gate's answer for them is tested on the registry's own
	// patterns. TestParentalSurfacesAreMounted holds those patterns to the real mounts.
	reached := func(w http.ResponseWriter, req *http.Request) {
		if principal.FromContext(req.Context()) == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"reached":true}`))
	}
	if more != nil {
		more(r, e)
	}
	for _, s := range pfStandIns {
		if r.Match(chi.NewRouteContext(), s.method, s.path) {
			if more != nil {
				continue // the whole home mounts the real route
			}
			t.Fatalf("%s %s is mounted now: test the real route", s.method, s.path)
		}
		r.MethodFunc(s.method, s.path, reached)
	}
	libs := []string{pfLibA, pfLibB}
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "admin":
			p = &principal.Principal{UserID: pfAdmin, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}
		case "guardian":
			p = &principal.Principal{UserID: pfGuardian, Libraries: libs, Source: principal.SourceJWT}
		case "other-guardian":
			p = &principal.Principal{UserID: pfOtherG, Libraries: libs, Source: principal.SourceJWT}
		case "child":
			p = &principal.Principal{UserID: pfChild, Libraries: libs, Source: principal.SourceJWT}
		case "child-cookie": // T11: the same child on a cookie session
			p = &principal.Principal{UserID: pfChild, Libraries: libs, Source: principal.SourceCookie}
		case "child-token": // the same child through a personal access token made before it was restricted
			p = &principal.Principal{UserID: pfChild, Libraries: libs, Source: principal.SourcePAT}
		case "child-all": // the same child, were its sign-in to say it has every library
			p = &principal.Principal{UserID: pfChild, AccessAllLibraries: true, Source: principal.SourceJWT}
		case "viewer":
			p = &principal.Principal{UserID: pfViewer, Libraries: libs, Source: principal.SourceJWT}
		case "admin-token": // the admin token: an admin with no user row
			p = &principal.Principal{IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceAdminToken}
		case "child-cloud": // the child signed in through Maktaba Cloud: a play-only sign-in
			p = &principal.Principal{UserID: pfChild, Libraries: libs, Source: principal.SourceJWT}
			p.ApplyCloudScope(principal.CloudScopePlay, libs)
		case "child-relay": // the same sign-in, forwarded by the relay agent on this machine
			p = &principal.Principal{UserID: pfChild, Libraries: libs, Source: principal.SourceJWT}
			p.ApplyCloudScope(principal.CloudScopePlay, libs)
			req.RemoteAddr = "127.0.0.1:49152"
			req.Header.Set("X-Maktaba-Via", "relay")
		case "viewer-device": // an unrestricted account whose device said 9+ (the token's `ac`)
			limit := parental.Tier9
			p = &principal.Principal{UserID: pfViewer, Libraries: libs, Source: principal.SourceJWT, AgeCeiling: &limit}
		}
		if e.as != nil {
			p = e.as(req, p)
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func (e *pfEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

// rule adds a rule for the child and drops the cache, as the parental routes do.
func (e *pfEnv) rule(effect, kind, id string) {
	e.t.Helper()
	e.exec(`INSERT INTO parental_rules (user_id, effect, subject_kind, subject_id) VALUES ($1,$2,$3,$4)`, pfChild, effect, kind, id)
	e.exec(`UPDATE parental_policies SET version = version + 1 WHERE user_id = $1`, pfChild)
	e.gate.Cache().Invalidate("")
}

func (e *pfEnv) clearRules() {
	e.t.Helper()
	e.exec(`DELETE FROM parental_rules WHERE user_id = $1`, pfChild)
	e.exec(`UPDATE parental_policies SET version = version + 1, max_tier = 9 WHERE user_id = $1`, pfChild)
	e.gate.Cache().Invalidate("")
}

func (e *pfEnv) raw(who, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Test-As", who)
	if e.stream > 0 {
		ctx, cancel := context.WithTimeout(req.Context(), e.stream)
		defer cancel()
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *pfEnv) get(who, path string) (int, string) {
	e.t.Helper()
	rec := e.raw(who, http.MethodGet, path, "")
	return rec.Code, rec.Body.String()
}

// pfRequestID is the one part of a problem body that differs between two requests.
var pfRequestID = regexp.MustCompile(`"requestId":"[^"]*"`)

// same fails unless the answer for the hidden id is the answer for an id that names nothing:
// the status, every header and the body, once each id is written the same way.
func (e *pfEnv) same(who, method, pathFmt, hidden, body string) {
	e.t.Helper()
	if code := e.alike(who, method, pathFmt, hidden, body); code < 400 {
		e.t.Errorf("%s %s %s: a hidden id answers %d", who, method, pathFmt, code)
	}
}

// alike is same for a route whose handler answers a missing id with a success (it reads no
// row): the two answers must still be one. It returns the status.
func (e *pfEnv) alike(who, method, pathFmt, hidden, body string) int {
	e.t.Helper()
	missing := uuid.NewString()
	a := e.raw(who, method, strings.ReplaceAll(pathFmt, "{id}", hidden), strings.ReplaceAll(body, "{id}", hidden))
	b := e.raw(who, method, strings.ReplaceAll(pathFmt, "{id}", missing), strings.ReplaceAll(body, "{id}", missing))
	norm := func(rec *httptest.ResponseRecorder, id string) (int, string, string) {
		h := rec.Header().Clone()
		h.Del("X-Request-Id")
		h.Del("Date")
		h.Del("Traceparent")
		var hb bytes.Buffer
		_ = h.Write(&hb)
		body := pfRequestID.ReplaceAllString(rec.Body.String(), `"requestId":"R"`)
		return rec.Code, strings.ReplaceAll(hb.String(), id, "ID"), strings.ReplaceAll(body, id, "ID")
	}
	ac, ah, ab := norm(a, hidden)
	bc, bh, bb := norm(b, missing)
	if ac != bc || ah != bh || ab != bb {
		e.t.Errorf("%s %s %s: a hidden id answers\n  %d %s %s\na missing id answers\n  %d %s %s", who, method, pathFmt, ac, ah, ab, bc, bh, bb)
	}
	if strings.Contains(a.Body.String(), pfTeenName) {
		e.t.Errorf("%s %s %s: the hidden answer names the title", who, method, pathFmt)
	}
	return ac
}

var pfVideoByID = []string{
	"/api/videos/{id}", "/api/videos/{id}/chapters", "/api/videos/{id}/context", "/api/videos/{id}/enrichment",
	"/api/videos/{id}/poster", "/api/videos/{id}/segments", "/api/videos/{id}/stats", "/api/videos/{id}/subtitles",
	"/api/videos/{id}/transcript/edits", "/api/videos/{id}/trickplay", "/api/videos/{id}/trickplay/sprite",
	"/api/me/history/{id}",
}

// T02, T03, T11: every by-id read of a hidden video, series and collection answers as a missing
// one, for the child on a bearer token, a cookie and an API token alike.
func TestP57FilterByID(t *testing.T) {
	t.Parallel()
	e := setupFilter(t)
	for _, who := range []string{"child", "child-cookie", "child-token", "child-all"} {
		for _, id := range []string{pfTeen, pfUnrated} {
			for _, p := range pfVideoByID {
				e.same(who, http.MethodGet, p, id, "")
			}
		}
		for _, p := range []string{"/api/series/{id}", "/api/series/{id}/episodes"} {
			e.same(who, http.MethodGet, p, pfSeries16, "")
		}
		for _, p := range []string{"/api/collections/{id}", "/api/collections/{id}/items"} {
			e.same(who, http.MethodGet, p, pfCol16, "")
		}
		// writes that name a hidden video
		e.same(who, http.MethodPost, "/api/videos/{id}/downloaded", pfTeen, `{}`)
		e.alike(who, http.MethodGet, "/api/videos/{id}/downloaded", pfTeen, "")
		// the playback channel opens for any id; a hidden one opens as a missing one does,
		// on a channel nothing publishes to
		e.stream = 300 * time.Millisecond
		e.alike(who, http.MethodGet, "/ws/playback/{id}", pfTeen, "")
		e.stream = 0
		e.same(who, http.MethodPost, "/api/stream/sessions", pfTeen, `{"video_id":"{id}"}`)
		e.same(who, http.MethodPost, "/api/watch/start", pfTeen, `{"video_id":"{id}"}`)
		e.same(who, http.MethodPost, "/api/me/play-queues", pfTeen, `{"source":{"kind":"video","id":"{id}"}}`)
		// another spelling of the hidden id is no way round
		if code, body := e.get(who, "/api/videos/"+strings.ToUpper(pfTeen)); code == http.StatusOK || strings.Contains(body, pfTeenName) {
			t.Errorf("%s: the hidden video in upper case answers %d", who, code)
		}
		if code, body := e.get(who, "/api/videos/"+strings.ReplaceAll(pfTeen, "-", "")); code == http.StatusOK || strings.Contains(body, pfTeenName) {
			t.Errorf("%s: the hidden video without hyphens answers %d", who, code)
		}
		// text that is no id is never written back as it was asked, in a page (what a
		// browser's navigation gets) or in JSON, in the body or in a header
		for _, accept := range []string{"text/html", "application/json"} {
			// sent raw, as no browser sends it: an encoded one comes back encoded
			req := httptest.NewRequest(http.MethodGet, "/api/videos/<script>alert(1)<!--", nil)
			req.Header.Set("X-Test-As", who)
			req.Header.Set("Accept", accept)
			if accept == "text/html" {
				req.Header.Set("Sec-Fetch-Dest", "document")
			}
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, req)
			var hb bytes.Buffer
			_ = rec.Header().Write(&hb)
			if rec.Code < 400 || strings.Contains(rec.Body.String(), "<script>") || strings.Contains(hb.String(), "<script>") {
				t.Errorf("%s: a script asked for as an id (%s) answers %d %s %s", who, accept, rec.Code, hb.String(), rec.Body.String())
			}
		}
		// what the limit allows is there
		for _, p := range []string{"/api/videos/" + pfKid, "/api/series/" + pfSeries, "/api/series/" + pfSeries + "/episodes",
			"/api/collections/" + pfCol, "/api/collections/" + pfCol + "/items", "/api/videos/" + pfKidB} {
			code, body := e.get(who, p)
			if code != http.StatusOK {
				t.Errorf("%s GET %s: %d, want 200", who, p, code)
			}
			if strings.Contains(body, pfTeen) || strings.Contains(body, pfTeenName) {
				t.Errorf("%s GET %s names the hidden video: %s", who, p, body)
			}
		}
	}
	// the others see it all: the viewer, both guardians and the admin
	for _, who := range []string{"viewer", "guardian", "other-guardian", "admin"} {
		for _, p := range []string{"/api/videos/" + pfTeen, "/api/videos/" + pfUnrated, "/api/series/" + pfSeries16, "/api/collections/" + pfCol16} {
			if code, _ := e.get(who, p); code != http.StatusOK {
				t.Errorf("%s GET %s: %d, want 200", who, p, code)
			}
		}
		if _, body := e.get(who, "/api/series/"+pfSeries+"/episodes"); !strings.Contains(body, pfTeen) {
			t.Errorf("%s: the series' episodes leave out the 16+ one: %s", who, body)
		}
	}
	// no one is refused by the route itself, hidden or not
	if code, body := e.get("", "/api/videos/"+pfTeen); code/100 == 2 || strings.Contains(body, pfTeenName) {
		t.Errorf("GET a video as no one: %d %s", code, body)
	}
}

// seriesCardEpisodes is the episode count on a series' card in who's grouped list
// (GET /api/videos?group=series), or -1 when the list has no such card.
func (e *pfEnv) seriesCardEpisodes(who, series string) int {
	e.t.Helper()
	code, body := e.get(who, "/api/videos?group=series&library_id="+pfLibA)
	var list struct {
		Items []struct {
			Kind, ID     string
			EpisodeCount int `json:"episode_count"`
		} `json:"items"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &list) != nil {
		e.t.Fatalf("%s: the grouped list answers %d %s", who, code, body)
	}
	for _, it := range list.Items {
		if it.Kind == "series" && it.ID == series {
			return it.EpisodeCount
		}
	}
	return -1
}

var pfLists = []string{
	"/api/videos", "/api/videos?library_id=" + pfLibA, "/api/videos?q=Zarqa", "/api/videos/facets",
	// one card per series (group.go): the card counts and names the episodes that pass only
	"/api/videos?group=series", "/api/videos?group=series&library_id=" + pfLibA, "/api/videos?group=series&sort=name",
	"/api/libraries", "/api/libraries/summary", "/api/libraries/" + pfLibA, "/api/libraries/" + pfLibA + "/hub",
	"/api/libraries/" + pfLibA + "/categories", "/api/libraries/" + pfLibA + "/collections", "/api/libraries/" + pfLibA + "/series",
	"/api/home", "/api/home/featured", "/api/recommendations", "/api/me/activity", "/api/me/history",
	"/api/collections", "/api/collections?library_id=" + pfLibA, "/api/series", "/api/series?library_id=" + pfLibA,
	"/api/me/favorites", "/api/me/playlists", "/api/me/play-queues/current",
}

// T04, T05: no list, row, count-bearing page or search names a hidden video; a row another
// account warmed is not the child's. Search itself and the live rows (T07) are in
// p57_gaps_pg_test.go.
func TestP57FilterLists(t *testing.T) {
	t.Parallel()
	e := setupFilter(t)
	// the child watched and liked the teen video before it was restricted
	e.exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at) VALUES ($1,$2,10,false,now())`, pfChild, pfTeen)
	// T05: the admin's and the viewer's Home and library pages first
	for _, who := range []string{"admin", "viewer"} {
		for _, p := range pfLists {
			e.get(who, p)
		}
	}
	code, body := e.get("viewer", "/api/videos?library_id="+pfLibA)
	if code != http.StatusOK || !strings.Contains(body, pfTeen) || !strings.Contains(body, pfUnrated) {
		t.Fatalf("the viewer's list lacks the 16+ or the unrated video: %d %s", code, body)
	}
	if n := e.seriesCardEpisodes("viewer", pfSeries); n != 2 {
		t.Fatalf("the viewer's card of the show counts %d episodes, want 2", n)
	}
	for _, who := range []string{"child", "child-cookie", "child-token", "child-all"} {
		for _, p := range pfLists {
			code, body := e.get(who, p)
			if code >= 500 {
				t.Errorf("%s GET %s: %d %s", who, p, code, body)
			}
			for _, hidden := range []string{pfTeen, pfTeenName, pfTeen2, "Late-Episode-Title", pfUnrated, "Unrated-Title", pfSeries16, "Pf late show", pfCol16, "Pf late picks"} {
				if strings.Contains(body, hidden) {
					t.Errorf("%s GET %s names %s: %s", who, p, hidden, body)
				}
			}
		}
		code, body := e.get(who, "/api/videos?library_id="+pfLibA)
		if code != http.StatusOK || !strings.Contains(body, pfKid) {
			t.Errorf("%s: the list lacks the all-ages video: %d %s", who, code, body)
		}
		var list struct {
			Total *int `json:"total"`
		}
		if json.Unmarshal([]byte(body), &list) == nil && list.Total != nil && *list.Total != 1 {
			t.Errorf("%s: the list counts %d videos, want the 1 shown", who, *list.Total)
		}
		// a series card counts the episodes that pass only: the show has an all-ages
		// and a 16+ episode
		if n := e.seriesCardEpisodes(who, pfSeries); n != 1 {
			t.Errorf("%s: the show's card counts %d episodes, want the 1 shown", who, n)
		}
		// closed: the answer of a route that is not there (search and the live rows
		// are filtered now: p57_gaps_pg_test.go)
		for _, c := range []struct{ method, path, body string }{
			{http.MethodGet, "/api/tv/guide", ""},
		} {
			rec := e.raw(who, c.method, c.path, c.body)
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), pfTeenName) {
				t.Errorf("%s %s %s: %d %s, want 404", who, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
	}
	if rec := e.raw("viewer", http.MethodPost, "/api/search", `{"q":"Zarqa"}`); rec.Code == http.StatusNotFound {
		t.Errorf("the viewer's search: %d", rec.Code)
	}

	// an allow shows a 16+ video; a block on its series then hides it again (block wins)
	e.rule(parental.EffectAllow, parental.SubjectVideo, pfTeen)
	e.rule(parental.EffectAllow, parental.SubjectVideo, pfTeen2)
	if code, body := e.get("child", "/api/videos?library_id="+pfLibA); code != http.StatusOK || !strings.Contains(body, pfTeen) || strings.Contains(body, pfUnrated) {
		t.Errorf("after an allow: %d %s", code, body)
	}
	for _, p := range []string{"/api/videos/" + pfTeen, "/api/videos/" + pfTeen2, "/api/series/" + pfSeries16} {
		if code, _ := e.get("child", p); code != http.StatusOK {
			t.Errorf("after an allow, GET %s: %d", p, code)
		}
	}
	e.rule(parental.EffectBlock, parental.SubjectSeries, pfSeries16)
	if _, body := e.get("child", "/api/videos?library_id="+pfLibA); strings.Contains(body, pfTeen2) || !strings.Contains(body, pfTeen) {
		t.Errorf("after a block on its series the list names the video, or lost the other: %s", body)
	}
	if _, body := e.get("child", "/api/series"); strings.Contains(body, pfSeries16) {
		t.Errorf("the blocked series is listed: %s", body)
	}
	e.same("child", http.MethodGet, "/api/videos/{id}", pfTeen2, "")
	e.same("child", http.MethodGet, "/api/series/{id}", pfSeries16, "")
	e.clearRules()

	// a blocked library is one the child was never given
	e.rule(parental.EffectBlock, parental.SubjectLibrary, pfLibB)
	for _, who := range []string{"child", "child-all"} {
		if code, body := e.get(who, "/api/libraries"); code != http.StatusOK || strings.Contains(body, pfLibB) || !strings.Contains(body, pfLibA) {
			t.Errorf("%s: libraries with one blocked: %d %s", who, code, body)
		}
		if _, body := e.get(who, "/api/videos"); strings.Contains(body, pfKidB) || !strings.Contains(body, pfKid) {
			t.Errorf("%s: the list with a library blocked: %s", who, body)
		}
		for _, p := range []string{"/api/libraries/{id}", "/api/libraries/{id}/hub", "/api/libraries/{id}/series", "/api/libraries/{id}/collections", "/api/libraries/{id}/categories"} {
			e.same(who, http.MethodGet, p, pfLibB, "")
		}
		e.same(who, http.MethodGet, "/api/videos/{id}", pfKidB, "")
	}
	if code, body := e.get("viewer", "/api/libraries"); code != http.StatusOK || !strings.Contains(body, pfLibB) {
		t.Errorf("the viewer's libraries: %d %s", code, body)
	}
}

// T06, T08: a queue made before a block skips the blocked video; a stream is refused at its
// start and when its session is read again; a restricted account's stream URL lives 15 minutes
// at most.
func TestP57FilterQueueAndStream(t *testing.T) {
	e := setupFilter(t)
	e.exec(`UPDATE parental_policies SET max_tier = 18, version = version + 1 WHERE user_id = $1`, pfChild)
	e.gate.Cache().Invalidate("")

	rec := e.raw("child", http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"library","id":"`+pfLibA+`"}}`)
	var q struct {
		ID    string `json:"id"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &q) != nil || !strings.Contains(rec.Body.String(), pfTeen) || !strings.Contains(rec.Body.String(), pfKid) {
		t.Fatalf("the child's queue under an 18+ limit: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), pfUnrated) {
		t.Errorf("the queue holds the unrated video: %s", rec.Body.String())
	}

	open := func(who, video string) (int, map[string]any) {
		rec := e.raw(who, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+video+`"}`)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	code, sess := open("child", pfTeen)
	sid, _ := sess["session_id"].(string)
	if code/100 != 2 || sid == "" {
		t.Fatalf("the child's stream under an 18+ limit: %d %v", code, sess)
	}
	if e.signer.ttl <= 0 || e.signer.ttl > 15*time.Minute {
		t.Errorf("a restricted account's stream URL lives %v, want 15 minutes at most", e.signer.ttl)
	}
	if exp, err := time.Parse(time.RFC3339, sess["expires_at"].(string)); err != nil || time.Until(exp) > 15*time.Minute+time.Second {
		t.Errorf("the answer's expires_at = %v (%v), want within 15 minutes", sess["expires_at"], err)
	}
	if code, _ := open("viewer", pfTeen); code/100 != 2 || e.signer.ttl <= 15*time.Minute {
		t.Errorf("the viewer's stream: %d, URL lifetime %v (want the usual, over 15 minutes)", code, e.signer.ttl)
	}
	e.exec(`INSERT INTO streaming_sessions (id, user_id, video_id, mode, opened_at, last_seen_at) VALUES ($1,$2,$3,'direct',now(),now())
		ON CONFLICT (id) DO NOTHING`, sid, pfChild, pfTeen)
	if code, body := e.get("child", "/api/stream/sessions/"+sid); code != http.StatusOK {
		t.Fatalf("the child's own session: %d %s", code, body)
	}

	// the guardian blocks the video
	var told []parental.Subject
	parental.SetBlockedHook(func(_ context.Context, eff *parental.Effective, s parental.Subject) {
		if eff.UserID == pfChild {
			told = append(told, s)
		}
	})
	defer parental.SetBlockedHook(nil)
	e.rule(parental.EffectBlock, parental.SubjectVideo, pfTeen)

	code, body := e.get("child", "/api/me/play-queues/"+q.ID)
	if code != http.StatusOK || strings.Contains(body, pfTeen) || !strings.Contains(body, pfKid) {
		t.Errorf("the queue after the block: %d %s", code, body)
	}
	if code, body := e.get("child", "/api/me/play-queues/current"); strings.Contains(body, pfTeen) {
		t.Errorf("the current queue after the block: %d %s", code, body)
	}
	// renewal: reading the session again, and asking for a new one
	missing := uuid.NewString()
	got, want := e.raw("child", http.MethodGet, "/api/stream/sessions/"+sid, ""), e.raw("child", http.MethodGet, "/api/stream/sessions/"+missing, "")
	if got.Code != http.StatusNotFound || got.Code != want.Code ||
		pfRequestID.ReplaceAllString(strings.ReplaceAll(got.Body.String(), sid, "ID"), "R") != pfRequestID.ReplaceAllString(strings.ReplaceAll(want.Body.String(), missing, "ID"), "R") {
		t.Errorf("the session after the block: %d %s, a missing one: %d %s", got.Code, got.Body.String(), want.Code, want.Body.String())
	}
	e.same("child", http.MethodPost, "/api/stream/sessions", pfTeen, `{"video_id":"{id}"}`)
	hb := e.raw("child", http.MethodPost, "/api/watch/heartbeat", `{"session_id":"`+sid+`","video_id":"`+pfTeen+`","position_sec":5}`)
	if hb.Code/100 == 2 {
		t.Errorf("a heartbeat on the blocked video: %d %s", hb.Code, hb.Body.String())
	}
	if code, _ := open("child", pfKid); code/100 != 2 {
		t.Errorf("the child's stream of a video still allowed: %d", code)
	}
	if len(told) == 0 {
		t.Error("the blocked hook was told of no refusal")
	}
	for _, s := range told {
		if s.Kind != parental.SubjectVideo || !strings.EqualFold(s.ID, pfTeen) {
			t.Errorf("the blocked hook was told of %v", s)
		}
	}
}

// T09 and README 6.6: what a restricted account may not do at all answers 403 `restricted`,
// whatever its token; everyone else gets the route's own answer.
func TestP57FilterRefusals(t *testing.T) {
	t.Parallel()
	e := setupFilter(t)
	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/me/tokens", `{"name":"t"}`},
		{http.MethodGet, "/api/search/youtube?q=cats", ""},
		{http.MethodGet, "/api/search/youtube/status", ""},
		{http.MethodPost, "/api/videos/" + pfKid + "/import-youtube", `{}`},
		{http.MethodGet, "/api/channels/xmltv", ""},
		{http.MethodGet, "/api/channels/playlist.m3u", ""},
		{http.MethodGet, "/api/plugins", ""},
		{http.MethodPut, "/api/home/featured", `{"video_id":"` + pfKid + `"}`},
		{http.MethodPost, "/api/tags", `{"name":"t"}`},
		{http.MethodDelete, "/api/tags/" + pfKid, ""},
		{http.MethodGet, "/api/users", ""},
		{http.MethodPost, "/api/libraries/" + pfLibA + "/scan", `{}`},
	}
	restricted := func(rec *httptest.ResponseRecorder) bool {
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code == http.StatusForbidden && out["code"] == "restricted" && out["type"] == parental.TypeRestricted
	}
	tokens := func() int {
		var n int
		if err := e.db.QueryRow(`SELECT count(*) FROM personal_access_tokens`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := tokens()
	for _, c := range routes {
		for _, who := range []string{"child", "child-cookie", "child-token", "child-all"} {
			if rec := e.raw(who, c.method, c.path, c.body); !restricted(rec) {
				t.Errorf("%s %s %s: %d %s, want 403 restricted", who, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
		for _, who := range []string{"viewer", "guardian", "other-guardian", "admin"} {
			if rec := e.raw(who, c.method, c.path, c.body); restricted(rec) || rec.Code == http.StatusServiceUnavailable && strings.Contains(rec.Body.String(), "parental") {
				t.Errorf("%s %s %s: refused as restricted: %d %s", who, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
		if rec := e.raw("", c.method, c.path, c.body); restricted(rec) || rec.Code/100 == 2 && c.path != "/api/channels/xmltv" && c.path != "/api/channels/playlist.m3u" {
			t.Errorf("no one %s %s: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	if after := tokens(); after < before {
		t.Fatalf("tokens went from %d to %d", before, after)
	}
	var childTokens int
	if err := e.db.QueryRow(`SELECT count(*) FROM personal_access_tokens WHERE user_id = $1`, pfChild).Scan(&childTokens); err != nil || childTokens != 0 {
		t.Errorf("the child holds %d tokens (%v), want none", childTokens, err)
	}
	// a route no entry names is not there for the child
	// (every mounted route has one: TestEveryRouteHasParentalSurface)

	// what stays open to the child: its own account and its own limit
	for _, p := range []string{"/api/me/parental", "/api/me/tokens"} {
		if code, body := e.get("child", p); code != http.StatusOK {
			t.Errorf("child GET %s: %d %s", p, code, body)
		}
	}
}

// A policy that cannot be read shows nothing: every content route refuses the signed-in
// caller, restricted or not, and no one is let in as unrestricted.
func TestP57FilterFailsClosed(t *testing.T) {
	t.Parallel()
	e := setupFilter(t)
	if code, _ := e.get("child", "/api/videos/"+pfKid); code != http.StatusOK {
		t.Fatalf("before: %d", code)
	}
	dead, err := sql.Open("postgres", "postgres://nobody:x@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dead.Close() }()
	e.gate.SetCache(parental.NewCache(parental.NewStore(dead)))
	for _, who := range []string{"child", "viewer", "admin"} {
		for _, p := range []string{"/api/videos", "/api/videos/" + pfTeen, "/api/videos/" + pfKid, "/api/home", "/api/tags", "/api/search/suggest?q=a"} {
			code, body := e.get(who, p)
			if code != http.StatusServiceUnavailable || strings.Contains(body, pfTeenName) || strings.Contains(body, "Kid-Title") {
				t.Errorf("%s GET %s with no policy store: %d %s, want 503", who, p, code, body)
			}
		}
	}
	// no one signed in is refused as before, by the route itself
	if code, body := e.get("", "/api/videos"); code/100 == 2 || code == http.StatusServiceUnavailable {
		t.Errorf("no one, with no policy store: %d %s", code, body)
	}
}

// A single item the filter refuses is one `blocked` event in the child's activity (57.08):
// once per subject per day, never for a list that merely left the item out, never for an
// account with no limit.
func TestP57FilterRecordsBlocked(t *testing.T) {
	e := setupFilter(t)
	count := func(user, subject string) int {
		t.Helper()
		var n int
		if err := e.db.QueryRow(`SELECT count(*) FROM parental_events
			WHERE child_id = $1 AND kind = 'blocked' AND ($2 = '' OR subject_id = $2)`, user, subject).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if code, _ := e.get("child", "/api/videos"); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	if n := count(pfChild, ""); n != 0 {
		t.Fatalf("a list wrote %d blocked events, want 0", n)
	}
	for range 2 {
		if code, _ := e.get("child", "/api/videos/"+pfTeen); code != http.StatusNotFound {
			t.Fatalf("hidden video: %d, want 404", code)
		}
	}
	if n := count(pfChild, pfTeen); n != 1 {
		t.Fatalf("two refusals of one video wrote %d blocked events, want 1", n)
	}
	if code, _ := e.get("child", "/api/videos/"+pfKid); code != http.StatusOK {
		t.Fatalf("allowed video: %d", code)
	}
	if n := count(pfChild, pfKid); n != 0 {
		t.Fatalf("an allowed video wrote %d blocked events", n)
	}
	if code, _ := e.get("viewer", "/api/videos/"+pfTeen); code != http.StatusOK {
		t.Fatalf("viewer: %d", code)
	}
	if n := count(pfViewer, ""); n != 0 {
		t.Fatalf("an account with no limit wrote %d blocked events", n)
	}
}
