package streaming

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// A progress post refused because the account's time is over stores the position
// it carries before the 403, when the session is the caller's own and still open.
// The keeper is asked exactly what an accepted post asks (one Beat): storing the
// position opens no lease and counts no time.

const (
	stopOwner = "00000000-0000-4000-8000-0000005714a1"
	stopOther = "00000000-0000-4000-8000-0000005714a2"
	stopVideo = "00000000-0000-4000-8000-0000005714b1"
	stopOpen  = "00000000-0000-4000-8000-0000005714c1"
	stopShut  = "00000000-0000-4000-8000-0000005714c2"
)

// stopGate is a time keeper that answers every Beat with one state or error, and
// records every call made to it.
type stopGate struct {
	mu    sync.Mutex
	state parental.TimeState
	err   error
	calls []string
}

func (g *stopGate) note(call string) {
	g.mu.Lock()
	g.calls = append(g.calls, call)
	g.mu.Unlock()
}

func (g *stopGate) Admit(context.Context, *principal.Principal, string) (parental.TimeState, error) {
	g.note("admit")
	return parental.TimeState{Allowed: true}, nil
}

func (g *stopGate) Opened(context.Context, string, string, string, string) error {
	g.note("opened")
	return nil
}

func (g *stopGate) Beat(_ context.Context, userID, sessionID string) (parental.TimeState, error) {
	g.note("beat " + userID + " " + sessionID)
	return g.state, g.err
}

func (g *stopGate) Closed(context.Context, string) error {
	g.note("closed")
	return nil
}

type stopRig struct {
	t    *testing.T
	db   *sql.DB
	h    http.Handler
	gate *stopGate
	now  time.Time
}

func newStopRig(t *testing.T) *stopRig {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE streaming_sessions (id TEXT PRIMARY KEY, user_id TEXT, video_id TEXT, closed_at TIMESTAMP)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, duration_sec REAL)`,
		`CREATE TABLE playback_state (user_id TEXT, video_id TEXT, position_sec REAL, completed BOOLEAN,
			updated_at TIMESTAMP, PRIMARY KEY (user_id, video_id))`,
		`INSERT INTO videos VALUES ('` + stopVideo + `', 7200)`,
		`INSERT INTO streaming_sessions VALUES ('` + stopOpen + `', '` + stopOwner + `', '` + stopVideo + `', NULL)`,
		`INSERT INTO streaming_sessions VALUES ('` + stopShut + `', '` + stopOwner + `', '` + stopVideo + `', '2026-01-01 00:00:00')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	rig := &stopRig{t: t, db: db, gate: &stopGate{state: parental.TimeState{Allowed: true}}, now: time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)}
	h := &Handler{DB: db, Time: rig.gate, NowFunc: func() time.Time { return rig.now }}
	r := chi.NewRouter()
	h.Mount(r)
	rig.h = r
	return rig
}

// post sends one progress post a debounce interval after the one before.
func (r *stopRig) post(p *principal.Principal, session string, position float64) (int, map[string]any) {
	r.t.Helper()
	r.now = r.now.Add(2 * time.Second)
	body, _ := json.Marshal(map[string]any{"position_sec": position})
	rec := call(r.h, p, http.MethodPost, "/api/stream/sessions/"+session+"/progress", string(body))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// stored is the owner's resume point for the video, or -1 when there is none.
func (r *stopRig) stored() float64 {
	r.t.Helper()
	var pos float64
	err := r.db.QueryRow(`SELECT position_sec FROM playback_state WHERE user_id = ? AND video_id = ?`, stopOwner, stopVideo).Scan(&pos)
	if err == sql.ErrNoRows {
		return -1
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return pos
}

func (r *stopRig) refused(what string, code int, body map[string]any, reason string) {
	r.t.Helper()
	if code != http.StatusForbidden || body["type"] != httperror.Type("parental-time") || body["code"] != "parental_time" || body["reason"] != reason {
		r.t.Fatalf("%s: %d %v, want 403 parental-time %s", what, code, body, reason)
	}
}

func TestProgressRefusedForTimeStoresItsPosition(t *testing.T) {
	owner := as(stopOwner)
	admin := &principal.Principal{UserID: stopOther, IsAdmin: true, Source: principal.SourceCookie}
	next := time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC)

	for _, reason := range []string{parental.ReasonLimitReached, parental.ReasonOutsideSlot} {
		t.Run(reason, func(t *testing.T) {
			r := newStopRig(t)
			if code, body := r.post(owner, stopOpen, 100); code != http.StatusOK {
				t.Fatalf("an accepted post: %d %v", code, body)
			}
			r.gate.state = parental.TimeState{Reason: reason, NextAllowedAt: &next}
			r.gate.calls = nil

			// the owner's post is refused, and its position is stored
			code, body := r.post(owner, stopOpen, 177.5)
			r.refused("the owner's post", code, body, reason)
			if body["next_allowed_at"] != "2026-11-13T06:00:00Z" {
				t.Fatalf("next_allowed_at = %v", body["next_allowed_at"])
			}
			if got := r.stored(); got != 177.5 {
				t.Fatalf("stored position after the refused post = %v, want 177.5", got)
			}
			// the keeper was asked once, as for any post: no lease opened, nothing
			// counted for the storing
			if len(r.gate.calls) != 1 || r.gate.calls[0] != "beat "+stopOwner+" "+stopOpen {
				t.Fatalf("the keeper's calls for a refused post = %v, want the one beat", r.gate.calls)
			}
			// a position past the video's end is clamped as an accepted post's is,
			// and marks the video completed
			code, body = r.post(owner, stopOpen, 9000)
			r.refused("a post past the end", code, body, reason)
			var done bool
			if err := r.db.QueryRow(`SELECT completed FROM playback_state WHERE user_id = ?`, stopOwner).Scan(&done); err != nil {
				t.Fatal(err)
			}
			if got := r.stored(); got != 7200 || !done {
				t.Fatalf("a position past the end stored as %v (completed %v), want 7200 completed", got, done)
			}

			// another account's post on the session (an admin may act on it) is
			// refused the same, and stores nothing
			code, body = r.post(admin, stopOpen, 300)
			r.refused("an admin's post on another's session", code, body, reason)
			if got := r.stored(); got != 7200 {
				t.Fatalf("an admin's refused post on another's session stored %v", got)
			}
			// one who may not act on the session gets a missing session's answer
			if code, _ := r.post(as(stopOther), stopOpen, 400); code != http.StatusNotFound {
				t.Fatalf("a stranger's post = %d, want 404", code)
			}
			// a closed session's refused post stores nothing
			code, body = r.post(owner, stopShut, 500)
			r.refused("a post on a closed session", code, body, reason)
			if got := r.stored(); got != 7200 {
				t.Fatalf("a closed session's refused post stored %v", got)
			}
		})
	}
}

// A session the keeper does not know as the account's (no_heartbeat), and a time
// that cannot be counted, store nothing.
func TestProgressRefusedOtherwiseStoresNothing(t *testing.T) {
	owner := as(stopOwner)
	r := newStopRig(t)
	r.gate.err = parental.ErrUnknownSession
	code, body := r.post(owner, stopOpen, 50)
	r.refused("an unknown session's post", code, body, parental.ReasonNoHeartbeat)
	if got := r.stored(); got != -1 {
		t.Fatalf("a no_heartbeat refusal stored %v", got)
	}
	r.gate.err = context.DeadlineExceeded
	if code, body := r.post(owner, stopOpen, 60); code != http.StatusServiceUnavailable || body["code"] != CodeTimeUnknown {
		t.Fatalf("a time that cannot be counted: %d %v", code, body)
	}
	if got := r.stored(); got != -1 {
		t.Fatalf("a 503 stored %v", got)
	}
}

// A video hidden from the account since its stream began answers 404 before the
// keeper is asked, and stores nothing.
func TestProgressOnHiddenVideoStoresNothing(t *testing.T) {
	r := newStopRig(t)
	r.gate.state = parental.TimeState{Reason: parental.ReasonLimitReached}
	r.now = r.now.Add(2 * time.Second)
	// a restricted account whose check cannot be made is refused: fail closed
	ctx := parental.With(principal.WithPrincipal(context.Background(), as(stopOwner)), &parental.Effective{Restricted: true})
	req := httptest.NewRequest(http.MethodPost, "/api/stream/sessions/"+stopOpen+"/progress", strings.NewReader(`{"position_sec":70}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	if code := rec.Code; code != http.StatusNotFound {
		t.Fatalf("a hidden video's post = %d, want 404", code)
	}
	if got := r.stored(); got != -1 || len(r.gate.calls) != 0 {
		t.Fatalf("a hidden video's post stored %v, keeper calls %v", got, r.gate.calls)
	}
}
