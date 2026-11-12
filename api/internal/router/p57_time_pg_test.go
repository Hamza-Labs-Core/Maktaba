package router_test

// Epic 57 story 57.14 behind the production router, against a real migrated
// Postgres (MAKTABA_TEST_PG_URL). T03: with the limit reached a stream session is
// not created, its URLs are not minted and its progress post is refused, each with
// the problem type parental-time; and the wiring fails closed.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	tmChild  = "00000000-0000-4000-8000-000000571401"
	tmViewer = "00000000-0000-4000-8000-000000571402"
	tmLib    = "00000000-0000-4000-8000-00000571a000"
	tmVideo  = "00000000-0000-4000-8000-0000571a0001"

	tmProblem = "https://maktaba.tv/problems/parental-time"
)

// tmStreaming is a streaming service that keeps time limits: it records the
// sessions it opened and the leases it was given.
type tmStreaming struct {
	acStreaming
	mu     sync.Mutex
	opened int
	leases map[string]time.Time // by stream session id
	end    *time.Time           // the end streaming holds for the account, as last told
	fail   error
}

func (s *tmStreaming) OpenSession(ctx context.Context, req grpcstreaming.OpenSessionRequest) (grpcstreaming.OpenSessionResponse, error) {
	s.mu.Lock()
	s.opened++
	s.mu.Unlock()
	return s.acStreaming.OpenSession(ctx, req)
}

func (s *tmStreaming) SetTimeLimit(_ context.Context, _, _ string, lim *grpcstreaming.TimeLimit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	if lim != nil {
		s.end = lim.EndAt
	}
	if lim != nil && lim.SessionID != "" && lim.LeaseUntil != nil {
		s.leases[lim.SessionID] = *lim.LeaseUntil
	}
	return nil
}

func (s *tmStreaming) TimeServed(context.Context, string, string) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Time{}, true, s.fail
}

func (s *tmStreaming) lease(session string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.leases[session]
	return t, ok
}

// tmSigner is a signer that can be told how to learn whether an account's time is
// kept, as signedurl.Minter can: it writes the claims where the test reads them.
type tmSigner struct {
	kept func(string) (bool, error)
}

func (s *tmSigner) KeepTime(kept func(string) (bool, error)) { s.kept = kept }

func (s *tmSigner) SignStreamURL(rawURL, sessionID, userID, _ string, ttl time.Duration) (string, time.Time, error) {
	if s.kept == nil {
		return rawURL + "?unwired", time.Now().Add(ttl), nil
	}
	on, err := s.kept(userID)
	if err != nil {
		return "", time.Time{}, err
	}
	if on {
		return rawURL + "?tk=1&tls=" + sessionID, time.Now().Add(ttl), nil
	}
	return rawURL + "?tk=0", time.Now().Add(ttl), nil
}

// tmPlainSigner cannot be told: it signs the same for everyone.
type tmPlainSigner struct{}

func (tmPlainSigner) SignStreamURL(rawURL, _, _, _ string, ttl time.Duration) (string, time.Time, error) {
	return rawURL + "?plain", time.Now().Add(ttl), nil
}

type tmEnv struct {
	*acEnv
	keeper *parental.TimeKeeper
}

func (e *tmEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *tmEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

// today is the home day now, as the database's rows name it.
func (e *tmEnv) today() string {
	e.t.Helper()
	store := parental.NewStore(e.db)
	loc, err := store.HomeZone(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return parental.DayKey(time.Now(), loc)
}

// setupTimeRouter: a restricted child and a viewer who both read one library with
// one video, behind the production router with client for streaming and signer.
func setupTimeRouter(t *testing.T, db *sql.DB, client grpcstreaming.Client, signer streaming.URLSigner) *tmEnv {
	t.Helper()
	e := &tmEnv{acEnv: &acEnv{t: t, db: db}}
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	router.MountP6(r, router.P6Deps{DB: db, StreamingClient: client, URLSigner: signer,
		OnParentalTime: func(k *parental.TimeKeeper) { e.keeper = k }})
	if e.keeper == nil {
		t.Fatal("MountP6 made no time keeper")
	}
	router.MountP29(r, router.P29Deps{DB: db, Driver: "postgres", Time: e.keeper})
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "child":
			p = &principal.Principal{UserID: tmChild, Libraries: []string{tmLib}, Source: principal.SourceJWT}
		case "viewer":
			p = &principal.Principal{UserID: tmViewer, Libraries: []string{tmLib}, Source: principal.SourceJWT}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func seedTime(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	for _, q := range []string{
		`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ('` + tmChild + `','tm-child','x',false), ('` + tmViewer + `','tm-viewer','x',false)`,
		`INSERT INTO libraries (id, name, roots) VALUES ('` + tmLib + `','TmLib','{/media/tm}')`,
		`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, age_tier, updated_at, created_at)
			VALUES ('` + tmVideo + `','` + tmLib + `','/media/tm/v.mp4','v.mp4',encode(sha256('tm'),'hex'),1000,now(),'ready','t',7200,0,now(),now())`,
		`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ('` + tmChild + `', 18, 1)`,
		`INSERT INTO parental_time_rules (user_id, days, daily_minutes) VALUES ('` + tmChild + `', 127, 60)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed %s: %v", q, err)
		}
	}
	return db
}

func open(e *tmEnv, as string) (int, map[string]any) {
	return e.do(as, http.MethodPost, "/api/stream/sessions", map[string]any{"video_id": tmVideo})
}

// T03.
func TestPGTimeLimitRefusesStreams(t *testing.T) {
	db := seedTime(t)
	svc := &tmStreaming{leases: map[string]time.Time{}}
	e := setupTimeRouter(t, db, svc, &tmSigner{})

	// inside the limit: the session opens, its URL says the time is kept and names
	// the session, and streaming holds that session's lease
	code, s := open(e, "child")
	if code != http.StatusCreated {
		t.Fatalf("inside the limit: %d %v", code, s)
	}
	session, _ := s["session_id"].(string)
	if url, _ := s["direct_url"].(string); !strings.HasSuffix(url, "?tk=1&tls="+session) {
		t.Fatalf("the child's URL = %q, want it to name session %s", url, session)
	}
	if until, ok := svc.lease(session); !ok || !until.After(time.Now()) || until.After(time.Now().Add(61*time.Second)) {
		t.Fatalf("the session's lease = %v (%v), want one of at most 60 s", until, ok)
	}
	// a viewer's URL says the time is not kept, and names no session
	code, v := open(e, "viewer")
	if url, _ := v["direct_url"].(string); code != http.StatusCreated || !strings.HasSuffix(url, "?tk=0") {
		t.Fatalf("the viewer: %d %v", code, v)
	}
	e.want(http.StatusOK, "child", http.MethodPost, "/api/stream/sessions/"+session+"/progress", map[string]any{"position_sec": 5})
	wcode, w := e.do("child", http.MethodPost, "/api/watch/start", map[string]any{"video_id": tmVideo})
	if wcode != http.StatusCreated {
		t.Fatalf("a watch inside the limit: %d %v", wcode, w)
	}

	// the limit is reached
	e.exec(`INSERT INTO parental_time_usage (user_id, day, library_id, seconds) VALUES ($1, $2::date, $3, 3600)
		ON CONFLICT (user_id, day, library_id) DO UPDATE SET seconds = 3600`, tmChild, e.today(), tmLib)
	refused := func(what string, code int, body map[string]any) {
		t.Helper()
		if code != http.StatusForbidden || body["type"] != tmProblem || body["code"] != "parental_time" ||
			body["reason"] != parental.ReasonLimitReached {
			t.Fatalf("%s with the limit reached: %d %v, want 403 parental-time limit_reached", what, code, body)
		}
		next, _ := body["next_allowed_at"].(string)
		if at, err := time.Parse(time.RFC3339, next); err != nil || !at.After(time.Now()) {
			t.Fatalf("%s: next_allowed_at = %q", what, next)
		}
	}
	openedBefore := svc.opened
	code, body := open(e, "child") // creating a session, and minting its URLs again
	refused("opening a session", code, body)
	if svc.opened != openedBefore || body["direct_url"] != nil {
		t.Fatal("a refused session was opened at streaming, or its URL handed out")
	}
	time.Sleep(1100 * time.Millisecond) // progress is taken once a second per session; a skipped post renews nothing
	state := e.count(`SELECT count(*) FROM playback_state WHERE user_id = $1 AND position_sec = 77`, tmChild)
	code, body = e.do("child", http.MethodPost, "/api/stream/sessions/"+session+"/progress", map[string]any{"position_sec": 77})
	refused("a progress post", code, body)
	if e.count(`SELECT count(*) FROM playback_state WHERE user_id = $1 AND position_sec = 77`, tmChild) != state {
		t.Fatal("a refused progress post was written")
	}
	// streaming was told the account's playback has ended: it serves no more of
	// any session, whatever its lease
	if svc.end == nil || svc.end.After(time.Now()) {
		t.Fatalf("streaming holds the end %v for the account, want one that has passed", svc.end)
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE child_id = $1 AND kind = 'time_up'`, tmChild); n != 1 {
		t.Fatalf("%d time_up events in the child's activity, want 1", n)
	}
	code, body = e.do("child", http.MethodPost, "/api/watch/start", map[string]any{"video_id": tmVideo})
	refused("starting a watch", code, body)
	code, body = e.do("child", http.MethodPost, "/api/watch/heartbeat", map[string]any{"session_id": w["session_id"], "position_sec": 9})
	refused("a watch heartbeat", code, body)
	// a session the home never opened for the child renews nothing
	code, body = e.do("child", http.MethodPost, "/api/stream/sessions/"+uuid.NewString()+"/progress", map[string]any{"position_sec": 1})
	if code != http.StatusNotFound {
		t.Fatalf("an unknown session's progress: %d %v", code, body)
	}
	// the viewer is not touched
	if code, v := open(e, "viewer"); code != http.StatusCreated {
		t.Fatalf("the viewer with the child's limit reached: %d %v", code, v)
	}
	// own time says the same
	mine := e.want(http.StatusOK, "child", http.MethodGet, "/api/me/parental/time?library_id="+tmLib, nil)
	if mine["allowed"] != false || mine["reason"] != parental.ReasonLimitReached {
		t.Fatalf("own time: %v", mine)
	}
}

// The wiring fails closed: a streaming service that keeps no time limits, one that
// cannot be told, and a signer that cannot say whether the time is kept all refuse
// a restricted account's stream; nobody else is touched.
func TestPGTimeWiringFailsClosed(t *testing.T) {
	db := seedTime(t)
	unavailable := func(what string, code int, body map[string]any) {
		t.Helper()
		if code != http.StatusServiceUnavailable || body["direct_url"] != nil {
			t.Fatalf("%s: %d %v, want 503 and no URL", what, code, body)
		}
	}
	// streaming without the time calls (an older build, or a stub)
	e := setupTimeRouter(t, db, acStreaming{}, &tmSigner{})
	code, body := open(e, "child")
	unavailable("streaming keeps no time limits", code, body)
	if code, v := open(e, "viewer"); code != http.StatusCreated {
		t.Fatalf("the viewer: %d %v", code, v)
	}
	// streaming cannot be reached to take the lease
	svc := &tmStreaming{leases: map[string]time.Time{}, fail: errors.New("streaming is down")}
	e = setupTimeRouter(t, db, svc, &tmSigner{})
	code, body = open(e, "child")
	unavailable("streaming cannot be told", code, body)
	// a signer that cannot carry the claims signs nothing for a kept account
	svc = &tmStreaming{leases: map[string]time.Time{}}
	e = setupTimeRouter(t, db, svc, tmPlainSigner{})
	code, body = open(e, "child")
	unavailable("a signer that cannot be told", code, body)
	code, v := open(e, "viewer")
	if url, _ := v["direct_url"].(string); code != http.StatusCreated || !strings.HasSuffix(url, "?plain") {
		t.Fatalf("the viewer through a plain signer: %d %v", code, v)
	}
	// the policy cannot be read: the stream is refused, not let through
	e = setupTimeRouter(t, db, &tmStreaming{leases: map[string]time.Time{}}, &tmSigner{})
	e.exec(`ALTER TABLE parental_time_rules RENAME TO parental_time_rules_gone`)
	code, body = open(e, "child")
	e.exec(`ALTER TABLE parental_time_rules_gone RENAME TO parental_time_rules`)
	unavailable("the rules cannot be read", code, body)
}
