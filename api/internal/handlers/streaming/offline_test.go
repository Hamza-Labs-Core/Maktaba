package streaming

// A full node plays with the home's database down (PR #145): the video's library and
// length from the node's catalog (the person's library access still checked), the
// session's owner kept on the node, its streaming_sessions row written once the
// database answers again. Only the database failing counts as an outage.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

type catalogVideo struct {
	lib string
	dur float64
}

type fakeOffline struct {
	mu     sync.Mutex
	down   bool
	failed []error
	videos map[string]catalogVideo
	back   []func()
	ledger string // the ledger's file ("": in memory)
}

func (f *fakeOffline) WhenDBBack(fn func()) { f.mu.Lock(); f.back = append(f.back, fn); f.mu.Unlock() }

func (f *fakeOffline) DBDown() bool       { f.mu.Lock(); defer f.mu.Unlock(); return f.down }
func (f *fakeOffline) LedgerPath() string { return f.ledger }
func (f *fakeOffline) DBFailed(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, err)
	f.down = true
}
func (f *fakeOffline) Video(id string) (string, float64, bool) {
	v, ok := f.videos[id]
	return v.lib, v.dur, ok
}

type fakeService struct {
	mu     sync.Mutex
	closed []string
}

func (s *fakeService) Open(_ context.Context, req OpenSessionRequest) (OpenSessionResponse, error) {
	switch req.VideoID {
	case vNoProbe:
		return OpenSessionResponse{}, errors.New("streaming: not-found")
	case vNoDisk:
		return OpenSessionResponse{}, errors.New("streaming: insufficient-storage")
	case vBusy:
		return OpenSessionResponse{}, errors.New("streaming: resource-exhausted")
	}
	return OpenSessionResponse{SessionID: uuid.NewString(), Mode: "direct", DirectURL: "/stream/direct/" + req.VideoID}, nil
}
func (s *fakeService) Close(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = append(s.closed, id)
	return nil
}
func (s *fakeService) Capabilities(context.Context) (Capabilities, error) { return Capabilities{}, nil }

const (
	vHere = "22222222-0000-4000-8000-000000000001"
	vLib2 = "22222222-0000-4000-8000-000000000002"
	vGone = "22222222-0000-4000-8000-000000000009"
	// in the catalog, unknown to this machine's streaming role
	vNoProbe = "22222222-0000-4000-8000-000000000003"
	// the streaming role's disk too full to transcode it; every slot busy
	vNoDisk = "22222222-0000-4000-8000-000000000004"
	vBusy   = "22222222-0000-4000-8000-000000000005"
)

// deadDB is a database that does not answer: every connection is refused.
func deadDB() *sql.DB { return sql.OpenDB(deadConnector{}) }

type deadConnector struct{}

func (deadConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}
func (deadConnector) Driver() driver.Driver { return deadDriver{} }

type deadDriver struct{}

func (deadDriver) Open(string) (driver.Conn, error) { return nil, errors.New("dead") }

// closedDB is the home's database gone: every call on it fails.
func closedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return db
}

func offlineRig(t *testing.T, down bool) (http.Handler, *fakeOffline, *fakeService) {
	t.Helper()
	off := &fakeOffline{down: down, videos: map[string]catalogVideo{
		vHere: {lib: "lib1", dur: 1800}, vLib2: {lib: "lib2", dur: 60}, vNoProbe: {lib: "lib1", dur: 60},
		vNoDisk: {lib: "lib1", dur: 60}, vBusy: {lib: "lib1", dur: 60},
	}}
	svc := &fakeService{}
	h := &Handler{DB: deadDB(), Service: svc, Offline: off}
	r := chi.NewRouter()
	h.Mount(r)
	return r, off, svc
}

func as(user string, libs ...string) *principal.Principal {
	return &principal.Principal{UserID: user, Libraries: libs, Source: principal.SourceCookie}
}

func call(h http.Handler, p *principal.Principal, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func open(t *testing.T, h http.Handler, p *principal.Principal, video string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	rec := call(h, p, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+video+`"}`)
	var s OpenSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	return s.SessionID, rec
}

func TestOfflinePlaybackFromTheCatalog(t *testing.T) {
	h, _, svc := offlineRig(t, true)
	amal := as("u-amal", "lib1")
	id, rec := open(t, h, amal, vHere)
	if rec.Code != http.StatusCreated || id == "" {
		t.Fatalf("open with the home's database down: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(h, amal, http.MethodGet, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), vHere) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	// the session's owner is the node's record: another user cannot close it
	if rec := call(h, as("u-other", "lib1"), http.MethodDelete, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("someone else's session: %d, want 404", rec.Code)
	}
	if rec := call(h, amal, http.MethodDelete, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("close: %d %s", rec.Code, rec.Body.String())
	}
	if len(svc.closed) != 1 || svc.closed[0] != id {
		t.Fatalf("the streaming session was not closed: %v", svc.closed)
	}
	if rec := call(h, amal, http.MethodDelete, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("closing it again: %d, want 404", rec.Code)
	}
	if rec := call(h, amal, http.MethodDelete, "/api/stream/sessions/"+uuid.NewString(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown session: %d, want 404", rec.Code)
	}
}

func TestOfflinePlaybackKeepsLibraryAccess(t *testing.T) {
	h, _, _ := offlineRig(t, true)
	if _, rec := open(t, h, as("u-amal", "lib1"), vLib2); rec.Code != http.StatusForbidden {
		t.Fatalf("a video of a library the person may not see: %d, want 403", rec.Code)
	}
	admin := &principal.Principal{UserID: "u-admin", IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceCookie}
	if _, rec := open(t, h, admin, vLib2); rec.Code != http.StatusCreated {
		t.Fatalf("an admin: %d", rec.Code)
	}
}

func TestOfflineAVideoOutsideTheCatalogIsUnavailable(t *testing.T) {
	h, _, _ := offlineRig(t, true)
	_, rec := open(t, h, as("u-amal", "lib1"), vGone)
	var p struct{ Type, Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != http.StatusServiceUnavailable || p.Type != ProblemUnavailableOffline || p.Code != "video_unavailable_offline" {
		t.Fatalf("a video the catalog lacks: %d %s", rec.Code, rec.Body.String())
	}
}

// A database not yet known to be down: its failure is recorded and the catalog answers.
func TestOfflineAFailingDatabaseFallsBack(t *testing.T) {
	h, off, _ := offlineRig(t, false)
	if _, rec := open(t, h, as("u-amal", "lib1"), vHere); rec.Code != http.StatusCreated {
		t.Fatalf("open on a failing database: %d %s", rec.Code, rec.Body.String())
	}
	if len(off.failed) == 0 || errors.Is(off.failed[0], sql.ErrNoRows) {
		t.Fatalf("the failure was not recorded: %v", off.failed)
	}
}

// The home has no catalog: a database failure stays an error there.
func TestOnTheHomeADatabaseFailureIsAnError(t *testing.T) {
	h := &Handler{DB: closedDB(t), Service: &fakeService{}}
	r := chi.NewRouter()
	h.Mount(r)
	if _, rec := open(t, r, as("u-amal", "lib1"), vHere); rec.Code != http.StatusInternalServerError {
		t.Fatalf("the home with its database failing: %d, want 500", rec.Code)
	}
}

// #157: a request that went away, or an error the database answered with, is not an
// outage: nothing is recorded and the catalog does not stand in.
func TestOfflineOnlyTheDatabaseFailingIsAnOutage(t *testing.T) {
	off := &fakeOffline{videos: map[string]catalogVideo{vHere: {lib: "lib1", dur: 1800}}}
	db, err := sql.Open("sqlite", ":memory:") // answers, without the tables: "no such table"
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := &Handler{DB: db, Service: &fakeService{}, Offline: off}
	r := chi.NewRouter()
	h.Mount(r)
	if _, rec := open(t, r, as("u-amal", "lib1"), vHere); rec.Code != http.StatusInternalServerError || len(off.failed) != 0 {
		t.Fatalf("an error the database answered with: %d, recorded %v", rec.Code, off.failed)
	}
	h.DB = deadDB()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := h.video(ctx, vHere); err == nil || len(off.failed) != 0 {
		t.Fatalf("a request that went away: %v, recorded %v", err, off.failed)
	}
}

// #157: the sessions opened (and closed) while the database was down are written back
// once it answers again, as they were: the home's history has them, and the progress
// the player reports on them finds them.
func TestOfflineSessionsAreWrittenBackWhenTheDatabaseAnswers(t *testing.T) {
	off := &fakeOffline{down: true, videos: map[string]catalogVideo{vHere: {lib: "lib1", dur: 1800}, vLib2: {lib: "lib2", dur: 60}}}
	hd := &Handler{DB: deadDB(), Service: &fakeService{}, Offline: off}
	h0 := chi.NewRouter()
	hd.Mount(h0)
	amal := as("u-amal", "lib1")
	open1, _ := open(t, h0, amal, vHere)
	admin := &principal.Principal{UserID: "u-admin", IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceCookie}
	closed2, _ := open(t, h0, admin, vLib2)
	if rec := call(h0, admin, http.MethodDelete, "/api/stream/sessions/"+closed2, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("close: %d", rec.Code)
	}
	if rec := call(h0, admin, http.MethodDelete, "/api/stream/sessions/"+closed2, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("closing it again: %d, want 404", rec.Code)
	}
	// the database answers again
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE streaming_sessions (id TEXT PRIMARY KEY, user_id TEXT, video_id TEXT,
		client_profile TEXT, mode TEXT, audio_track_id TEXT, subtitle_lang TEXT, start_sec REAL,
		max_bitrate_kbps INTEGER, burn_subs BOOLEAN, opened_at TIMESTAMP, last_seen_at TIMESTAMP, closed_at TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	hd.DB = db
	off.mu.Lock()
	off.down = false
	backs := append([]func(){}, off.back...)
	off.mu.Unlock()
	for _, fn := range backs {
		fn()
	}
	rows := map[string]bool{}
	q, err := db.Query(`SELECT id, closed_at IS NOT NULL FROM streaming_sessions`)
	if err != nil {
		t.Fatal(err)
	}
	for q.Next() {
		var id string
		var closed bool
		_ = q.Scan(&id, &closed)
		rows[id] = closed
	}
	_ = q.Close()
	if c, ok := rows[open1]; !ok || c {
		t.Fatalf("the open session: %v %v", ok, c)
	}
	if c, ok := rows[closed2]; !ok || !c {
		t.Fatalf("the closed session: %v %v", ok, c)
	}
	if p := hd.led().pending(); len(p) != 0 {
		t.Fatalf("still unwritten: %v", p)
	}
	// again: nothing written twice (the ids are there)
	hd.Reconcile(context.Background())
}

// #157: a video this machine's streaming role does not know while the home's database is
// down is unavailable offline (not "streaming unavailable").
func TestOfflineAVideoTheStreamingRoleLacksIsUnavailable(t *testing.T) {
	h, _, _ := offlineRig(t, true)
	_, rec := open(t, h, as("u-amal", "lib1"), vNoProbe)
	var p struct{ Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != http.StatusServiceUnavailable || p.Code != "video_unavailable_offline" {
		t.Fatalf("a video the streaming role does not know, offline: %d %s", rec.Code, rec.Body.String())
	}
}

// The streaming role's refusals: too little disk to prepare the video is said
// as such (507, its own code), not as busy; busy is 503 with its Retry-After
// sent. An opened session carries the video's catalogue length.
func TestOpenSessionRefusalsAndLength(t *testing.T) {
	h, _, _ := offlineRig(t, true)
	_, rec := open(t, h, as("u-amal", "lib1"), vNoDisk)
	var p struct{ Type, Title, Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != http.StatusInsufficientStorage || p.Code != "insufficient_storage" || p.Type != ProblemInsufficientStorage ||
		p.Title != "not enough disk space to prepare this video" {
		t.Errorf("no disk: %d %s", rec.Code, rec.Body.String())
	}
	_, rec = open(t, h, as("u-amal", "lib1"), vBusy)
	if rec.Code != http.StatusServiceUnavailable || rec.Result().Header.Get("Retry-After") != "5" {
		t.Errorf("busy: %d, Retry-After %q", rec.Code, rec.Result().Header.Get("Retry-After"))
	}
	_, rec = open(t, h, as("u-amal", "lib1"), vHere)
	var s OpenSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if rec.Code != http.StatusCreated || s.DurationSec != 1800 {
		t.Errorf("opened: %d, duration %v", rec.Code, s.DurationSec)
	}
}

func sessionsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE streaming_sessions (id TEXT PRIMARY KEY, user_id TEXT, video_id TEXT,
		client_profile TEXT, mode TEXT, audio_track_id TEXT, subtitle_lang TEXT, start_sec REAL,
		max_bitrate_kbps INTEGER, burn_subs BOOLEAN, opened_at TIMESTAMP, last_seen_at TIMESTAMP, closed_at TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// The sessions opened with the home's database down are kept on disk: a restart of this
// API still writes them back when the database answers.
func TestOfflineSessionsOutliveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stream-sessions.json")
	off := &fakeOffline{down: true, ledger: path, videos: map[string]catalogVideo{vHere: {lib: "lib1", dur: 1800}}}
	before := &Handler{DB: deadDB(), Service: &fakeService{}, Offline: off}
	r := chi.NewRouter()
	before.Mount(r)
	id, _ := open(t, r, as("u-amal", "lib1"), vHere)
	// this API starts again, the database answers
	db := sessionsDB(t)
	after := &Handler{DB: db, Service: &fakeService{}, Offline: &fakeOffline{ledger: path}}
	after.Reconcile(context.Background())
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM streaming_sessions WHERE id = ? AND user_id = 'u-amal'`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("the session opened before the restart: %d rows", n)
	}
}

// A session whose row does not write back does not hold the others back: they are
// written, it stays to be tried again.
func TestOneSessionThatDoesNotWriteBackLeavesTheOthers(t *testing.T) {
	off := &fakeOffline{down: true, videos: map[string]catalogVideo{vHere: {lib: "lib1", dur: 1800}}}
	hd := &Handler{DB: deadDB(), Service: &fakeService{}, Offline: off}
	r := chi.NewRouter()
	hd.Mount(r)
	bad, _ := open(t, r, as("u-amal", "lib1"), vHere)
	good, _ := open(t, r, as("u-badr", "lib1"), vHere)
	db := sessionsDB(t)
	if _, err := db.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON streaming_sessions WHEN NEW.id = '` + bad + `'
	    BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	hd.DB = db
	off.mu.Lock()
	off.down = false
	off.mu.Unlock()
	hd.Reconcile(context.Background())
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM streaming_sessions WHERE id = ?`, good).Scan(&n)
	if n != 1 {
		t.Fatal("a session that writes back waited for one that does not")
	}
	if p := hd.led().pending(); len(p) != 1 || p[bad].Owner != "u-amal" {
		t.Fatalf("still to write: %v", p)
	}
}

// A session the database has, closed with the database down: its closing is written
// when the database answers.
func TestAClosingWithTheDatabaseDownIsWrittenBack(t *testing.T) {
	off := &fakeOffline{down: true, videos: map[string]catalogVideo{vHere: {lib: "lib1", dur: 1800}}}
	hd := &Handler{DB: deadDB(), Service: &fakeService{}, Offline: off}
	r := chi.NewRouter()
	hd.Mount(r)
	amal := as("u-amal", "lib1")
	id, _ := open(t, r, amal, vHere)
	// the database answers: the row is written, the session goes on
	db := sessionsDB(t)
	hd.DB = db
	off.mu.Lock()
	off.down = false
	off.mu.Unlock()
	hd.Reconcile(context.Background())
	var rows int
	_ = db.QueryRow(`SELECT count(*) FROM streaming_sessions WHERE id = ? AND closed_at IS NULL`, id).Scan(&rows)
	if rows != 1 {
		t.Fatalf("the session's row: %d", rows)
	}
	// closed with the database down again
	off.mu.Lock()
	off.down = true
	off.mu.Unlock()
	if rec := call(r, amal, http.MethodDelete, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("close with the database down: %d", rec.Code)
	}
	off.mu.Lock()
	off.down = false
	off.mu.Unlock()
	hd.Reconcile(context.Background())
	var closed bool
	_ = db.QueryRow(`SELECT closed_at IS NOT NULL FROM streaming_sessions WHERE id = ?`, id).Scan(&closed)
	if !closed {
		t.Fatal("the closing made with the database down was not written back")
	}
	if p := hd.led().pending(); len(p) != 0 {
		t.Fatalf("still pending: %v", p)
	}
}
