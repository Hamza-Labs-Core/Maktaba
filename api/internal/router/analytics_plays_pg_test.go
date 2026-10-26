package router_test

// Epic 44 (stories 44.2, 44.3, 44.5) against a real migrated Postgres behind
// the production router: a play recorded from the stream session's progress,
// with the app from X-Maktaba-Client; only the session's owner (or an admin)
// can move it; tracking off records none; the API's use counted per app.
// Runs when MAKTABA_TEST_PG_URL is set (see access_pg_test.go).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/watch"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/usage"
)

type playsEnv struct {
	t  *testing.T
	db *sql.DB
	h  http.Handler
	c  *usage.Counter
}

func setupPlays(t *testing.T) *playsEnv {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'pl-admin','x',true), ($2,'pl-member','x',false), ($3,'pl-other','x',false)`,
		acAdmin, acMember, acOther)
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'PlAlpha','{/media/pl-alpha}')`, acLibA)
	now := time.Now().UTC()
	exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
		VALUES ($1,$2,'/media/pl/a1.mp4','a1.mp4',encode(sha256(convert_to($4::text,'UTF8')),'hex'),1000,$3,'ready','t',1000,$3,$3)`,
		acVidA1, acLibA, now, acVidA1)

	c := &usage.Counter{}
	c.Attach(db)
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore(), Usage: c})
	router.MountP6(r, router.P6Deps{DB: db, StreamingClient: acStreaming{}})
	router.MountP29(r, router.P29Deps{DB: db, Driver: "postgres"})
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "admin":
			p = &principal.Principal{UserID: acAdmin, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}
		case "member":
			p = &principal.Principal{UserID: acMember, Libraries: []string{acLibA}, Source: principal.SourceJWT}
		case "other":
			p = &principal.Principal{UserID: acOther, Libraries: []string{acLibA}, Source: principal.SourceJWT}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return &playsEnv{t: t, db: db, h: h, c: c}
}

func (e *playsEnv) do(who, app, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	if body == nil {
		b = nil
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-As", who)
	if app != "" {
		req.Header.Set("X-Maktaba-Client", app)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *playsEnv) want(status int, who, app, method, path string, body any) map[string]any {
	e.t.Helper()
	got, out := e.do(who, app, method, path, body)
	if got != status {
		e.t.Fatalf("%s %s %s = %d (%v), want %d", who, method, path, got, out, status)
	}
	return out
}

type pgPlay struct {
	userID, state, app, version, platform, device string
	watched                                       int
	pct                                           float64
	ended                                         bool
}

// plays returns the plays of a stream session.
func (e *playsEnv) plays(sid string) []pgPlay {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT user_id, state, COALESCE(client_app,''), COALESCE(client_version,''),
		COALESCE(platform,''), COALESCE(device_type,''), duration_sec, percent_complete, ended_at IS NOT NULL
		FROM watch_sessions WHERE stream_session_id = $1`, sid)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []pgPlay
	for rows.Next() {
		var p pgPlay
		if err := rows.Scan(&p.userID, &p.state, &p.app, &p.version, &p.platform, &p.device, &p.watched, &p.pct, &p.ended); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// back moves the play's last progress back by d (the test cannot wait).
func (e *playsEnv) back(sid string, d time.Duration) {
	e.t.Helper()
	if _, err := e.db.Exec(`UPDATE watch_sessions SET last_heartbeat = last_heartbeat - $2::interval
		WHERE stream_session_id = $1`, sid, d.String()); err != nil {
		e.t.Fatal(err)
	}
}

const iosPlayer = "player/1.3.0 (ios; phone)"

func TestPGPlaysFromTheStreamSession(t *testing.T) {
	e := setupPlays(t)
	s := e.want(http.StatusCreated, "member", iosPlayer, http.MethodPost, "/api/stream/sessions", map[string]any{"video_id": acVidA1})
	sid, _ := s["session_id"].(string)
	path := "/api/stream/sessions/" + sid
	debounce := func() { time.Sleep(1100 * time.Millisecond) } // the progress debounce: 1 a second

	// another user cannot report progress on it: no play either
	e.want(http.StatusNotFound, "other", iosPlayer, http.MethodPost, path+"/progress", map[string]any{"position_sec": 10})
	if got := e.plays(sid); len(got) != 0 {
		t.Fatalf("another user's progress recorded a play: %+v", got)
	}

	// the first progress opens the play, with the app
	e.want(http.StatusOK, "member", iosPlayer, http.MethodPost, path+"/progress", map[string]any{"position_sec": 100})
	got := e.plays(sid)
	if len(got) != 1 {
		t.Fatalf("plays after the first progress: %+v", got)
	}
	if p := got[0]; p.userID != acMember || p.state != watch.StateActive || p.app != "player" || p.version != "1.3.0" ||
		p.platform != "ios" || p.device != "phone" || p.pct != 10 || p.watched != 0 {
		t.Fatalf("first play: %+v", p)
	}

	// played 10 s, then paused, then seeked forward
	e.back(sid, 10*time.Second)
	debounce()
	e.want(http.StatusOK, "member", iosPlayer, http.MethodPost, path+"/progress", map[string]any{"position_sec": 110})
	e.back(sid, 10*time.Second)
	debounce()
	e.want(http.StatusOK, "member", iosPlayer, http.MethodPost, path+"/progress", map[string]any{"position_sec": 110})
	e.back(sid, 10*time.Second)
	debounce()
	e.want(http.StatusOK, "member", iosPlayer, http.MethodPost, path+"/progress", map[string]any{"position_sec": 500})
	got = e.plays(sid)
	if len(got) != 1 || got[0].watched < 20 || got[0].watched > 22 || got[0].pct != 50 {
		t.Fatalf("after playing, pausing and seeking: %+v, want ~20 s at 50 %%", got)
	}

	// an admin may move it (it stays the owner's); closing the session stops it
	debounce()
	e.want(http.StatusOK, "admin", "", http.MethodPost, path+"/progress", map[string]any{"position_sec": 510})
	e.want(http.StatusNoContent, "member", iosPlayer, http.MethodDelete, path, nil)
	got = e.plays(sid)
	if len(got) != 1 || got[0].userID != acMember || got[0].state != watch.StateStopped || !got[0].ended {
		t.Fatalf("after the close: %+v", got)
	}
	// a progress after the close never reopens it (the resume point still lands)
	debounce()
	e.want(http.StatusOK, "member", iosPlayer, http.MethodPost, path+"/progress", map[string]any{"position_sec": 520})
	if got = e.plays(sid); len(got) != 1 || got[0].state != watch.StateStopped {
		t.Fatalf("after a progress past the close: %+v", got)
	}

	// played to the end: completed
	s = e.want(http.StatusCreated, "member", "web/0.3.25 (macos; desktop)", http.MethodPost, "/api/stream/sessions", map[string]any{"video_id": acVidA1})
	sid2, _ := s["session_id"].(string)
	e.want(http.StatusOK, "member", "web/0.3.25 (macos; desktop)", http.MethodPost,
		"/api/stream/sessions/"+sid2+"/progress", map[string]any{"position_sec": 990})
	if got = e.plays(sid2); len(got) != 1 || got[0].state != watch.StateCompleted || got[0].app != "web" || got[0].platform != "macos" {
		t.Fatalf("a play to the end: %+v", got)
	}

	// the reaper interrupts a play gone quiet
	s = e.want(http.StatusCreated, "other", "tv/1.0 (tvos; tv)", http.MethodPost, "/api/stream/sessions", map[string]any{"video_id": acVidA1})
	sid3, _ := s["session_id"].(string)
	e.want(http.StatusOK, "other", "tv/1.0 (tvos; tv)", http.MethodPost, "/api/stream/sessions/"+sid3+"/progress", map[string]any{"position_sec": 5})
	e.back(sid3, 10*time.Minute)
	if _, _, err := (&watch.Reaper{DB: e.db}).RunOnce(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got = e.plays(sid3); len(got) != 1 || got[0].state != watch.StateInterrupted || !got[0].ended {
		t.Fatalf("a quiet play after the reaper: %+v", got)
	}
}

// A person who turned tracking off: the resume point lands, no play.
func TestPGPlaysTrackingOff(t *testing.T) {
	e := setupPlays(t)
	e.want(http.StatusOK, "member", iosPlayer, http.MethodPut, "/api/me/activity/settings", map[string]any{"track_enabled": false})
	s := e.want(http.StatusCreated, "member", iosPlayer, http.MethodPost, "/api/stream/sessions", map[string]any{"video_id": acVidA1})
	sid, _ := s["session_id"].(string)
	e.want(http.StatusOK, "member", iosPlayer, http.MethodPost, "/api/stream/sessions/"+sid+"/progress", map[string]any{"position_sec": 100})
	if got := e.plays(sid); len(got) != 0 {
		t.Fatalf("tracking off, but a play: %+v", got)
	}
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM playback_state WHERE user_id = $1`, acMember).Scan(&n); err != nil || n != 1 {
		t.Fatalf("resume point: %d (%v)", n, err)
	}
}

// /api/watch/start (Android TV, which opens no stream session) records the app.
func TestPGWatchStartRecordsTheApp(t *testing.T) {
	e := setupPlays(t)
	out := e.want(http.StatusCreated, "member", "tv/1.0 (androidtv; tv)", http.MethodPost, "/api/watch/start", map[string]any{"video_id": acVidA1})
	id, _ := out["session_id"].(string)
	var app, version, platform, device string
	if err := e.db.QueryRow(`SELECT client_app, client_version, platform, device_type FROM watch_sessions WHERE id = $1`, id).
		Scan(&app, &version, &platform, &device); err != nil {
		t.Fatal(err)
	}
	if app != "tv" || version != "1.0" || platform != "androidtv" || device != "tv" {
		t.Fatalf("watch start labels: %s %s %s %s", app, version, platform, device)
	}
	// the body's own hints win over the header's
	out = e.want(http.StatusCreated, "member", "tv/1.0 (androidtv; tv)", http.MethodPost, "/api/watch/start",
		map[string]any{"video_id": acVidA1, "device_type": "tv-box", "platform": "android"})
	id, _ = out["session_id"].(string)
	if err := e.db.QueryRow(`SELECT platform, device_type FROM watch_sessions WHERE id = $1`, id).Scan(&platform, &device); err != nil ||
		platform != "android" || device != "tv-box" {
		t.Fatalf("body hints: %s %s (%v)", platform, device, err)
	}
}

// The API's use per app: requests, errors and people a day, added up across
// flushes; retention removes old days.
func TestPGUsagePerApp(t *testing.T) {
	e := setupPlays(t)
	for i := 0; i < 3; i++ {
		e.want(http.StatusOK, "member", iosPlayer, http.MethodGet, "/api/me/activity/settings", nil)
	}
	e.want(http.StatusOK, "other", "web/0.3.25 (macos; desktop)", http.MethodGet, "/api/me/activity/settings", nil)
	e.want(http.StatusNotFound, "other", "web/0.3.25 (macos; desktop)", http.MethodGet, "/api/stream/sessions/"+uuid.NewString(), nil)
	ctx := context.Background()
	if err := e.c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	e.want(http.StatusOK, "member", iosPlayer, http.MethodGet, "/api/me/activity/settings", nil)
	if err := e.c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format(time.DateOnly)
	var requests, errs int
	if err := e.db.QueryRow(`SELECT requests, errors FROM api_usage_daily WHERE day = $1 AND client_app = 'player' AND platform = 'ios'`, today).
		Scan(&requests, &errs); err != nil || requests != 4 || errs != 0 {
		t.Fatalf("player today: %d requests, %d errors (%v); want 4, 0", requests, errs, err)
	}
	if err := e.db.QueryRow(`SELECT requests FROM api_usage_daily WHERE day = $1 AND client_app = 'web'`, today).
		Scan(&requests); err != nil || requests != 2 {
		t.Fatalf("web today: %d (%v); want 2", requests, err)
	}
	var people int
	if err := e.db.QueryRow(`SELECT count(*) FROM api_usage_people_daily WHERE day = $1`, today).Scan(&people); err != nil || people != 2 {
		t.Fatalf("people today: %d (%v); want 2", people, err)
	}

	// retention: a day older than the window goes with the reaper's purge
	if _, err := e.db.Exec(`INSERT INTO api_usage_daily (day, client_app, platform, requests) VALUES (CURRENT_DATE - 400, 'web', 'web', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO api_usage_people_daily (day, client_app, user_id) VALUES (CURRENT_DATE - 400, 'web', $1)`, acOther); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&watch.Reaper{DB: e.db, RetentionDays: 365}).RunOnce(ctx, true); err != nil {
		t.Fatal(err)
	}
	var old int
	if err := e.db.QueryRow(`SELECT (SELECT count(*) FROM api_usage_daily WHERE day < CURRENT_DATE - 365) +
		(SELECT count(*) FROM api_usage_people_daily WHERE day < CURRENT_DATE - 365)`).Scan(&old); err != nil || old != 0 {
		t.Fatalf("rows past retention: %d (%v)", old, err)
	}
	if err := e.db.QueryRow(`SELECT count(*) FROM api_usage_daily WHERE day = $1`, today).Scan(&requests); err != nil || requests == 0 {
		t.Fatalf("today's rows went with the purge: %d (%v)", requests, err)
	}
}
