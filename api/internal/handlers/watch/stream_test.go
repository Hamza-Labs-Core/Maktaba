package watch

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
)

func TestStreamCreditedSeconds(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		gap       time.Duration
		prev, pos float64
		want      int
	}{
		{"played in real time", 10 * time.Second, 100, 110, 10},
		{"paused", 10 * time.Second, 110, 110, 0},
		{"seeked back", 10 * time.Second, 110, 50, 0},
		{"seeked forward: the gap only", 10 * time.Second, 110, 900, 10},
		{"stalled: what it played", 10 * time.Second, 110, 113, 3},
		{"a long gap is capped", time.Hour, 0, 7200, 300},
		{"a clock gone back", -time.Second, 0, 10, 0},
	}
	for _, c := range cases {
		if got := StreamCreditedSeconds(t0, t0.Add(c.gap), c.prev, c.pos, DefaultStaleTimeout); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

// streamDB is SQLite with the tables the play's record touches, as the SQLite
// migrations make them (timestamps as TEXT; slot 0158's columns).
func streamDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		`CREATE TABLE watch_sessions (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, video_id TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT,
			last_heartbeat TEXT NOT NULL DEFAULT (datetime('now')),
			duration_sec INTEGER NOT NULL DEFAULT 0 CHECK (duration_sec >= 0),
			percent_complete REAL NOT NULL DEFAULT 0 CHECK (percent_complete >= 0 AND percent_complete <= 100),
			state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','completed','stopped','interrupted')),
			device_type TEXT, platform TEXT, quality TEXT, ip_addr_hash TEXT,
			stream_session_id TEXT, client_app TEXT, client_version TEXT)`,
		`CREATE UNIQUE INDEX watch_sessions_stream_session_idx ON watch_sessions (stream_session_id) WHERE stream_session_id IS NOT NULL`,
		`CREATE TABLE user_analytics_prefs (user_id TEXT PRIMARY KEY, track_enabled INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`CREATE TABLE playback_state (user_id TEXT NOT NULL, video_id TEXT NOT NULL,
			position_sec REAL NOT NULL DEFAULT 0, completed INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')), PRIMARY KEY (user_id, video_id))`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

type playRow struct {
	userID, videoID, state, device, platform, app, version, ipHash string
	watched                                                        int
	pct                                                            float64
	ended                                                          bool
}

func loadPlay(t *testing.T, db *sql.DB, sid string) (playRow, int) {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM watch_sessions WHERE stream_session_id = $1`, sid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return playRow{}, 0
	}
	var p playRow
	var ended sql.NullString
	if err := db.QueryRow(`SELECT user_id, video_id, state, device_type, platform, client_app, client_version,
		ip_addr_hash, duration_sec, percent_complete, ended_at FROM watch_sessions WHERE stream_session_id = $1`, sid).
		Scan(&p.userID, &p.videoID, &p.state, &p.device, &p.platform, &p.app, &p.version, &p.ipHash,
			&p.watched, &p.pct, &ended); err != nil {
		t.Fatal(err)
	}
	p.ended = ended.Valid
	return p, n
}

// post records a progress post the way the stream handler does: the play
// first, then the position stored as the resume point.
func post(t *testing.T, db *sql.DB, p StreamProgress) {
	t.Helper()
	if err := RecordStreamProgress(context.Background(), db, p); err != nil {
		t.Fatalf("record at %v: %v", p.PositionSec, err)
	}
	if _, err := db.Exec(`INSERT INTO playback_state (user_id, video_id, position_sec) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, video_id) DO UPDATE SET position_sec = excluded.position_sec`,
		p.UserID, p.VideoID, p.PositionSec); err != nil {
		t.Fatal(err)
	}
}

// One stream session is one play: the first progress opens it with the app,
// later ones add the time played, a pause adds nothing, the end completes it.
func TestStreamProgressRecordsOnePlay(t *testing.T) {
	db := streamDB(t)
	t0 := time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC)
	base := StreamProgress{StreamSessionID: "s1", UserID: "u1", VideoID: "v1", VideoDurationSec: 1000,
		Client: client.Parse("player/1.3.0 (ios; phone)"), IPHash: "abcd"}
	at := func(sec int, pos float64) StreamProgress {
		p := base
		p.Now, p.PositionSec = t0.Add(time.Duration(sec)*time.Second), pos
		return p
	}

	post(t, db, at(0, 100))
	got, n := loadPlay(t, db, "s1")
	if n != 1 || got.state != StateActive || got.watched != 0 || got.pct != 10 {
		t.Fatalf("first progress: %+v (%d rows)", got, n)
	}
	if got.userID != "u1" || got.videoID != "v1" || got.app != "player" || got.version != "1.3.0" ||
		got.platform != "ios" || got.device != "phone" || got.ipHash != "abcd" || got.ended {
		t.Fatalf("first progress labels: %+v", got)
	}

	post(t, db, at(10, 110)) // played 10 s
	post(t, db, at(20, 110)) // paused
	post(t, db, at(30, 900)) // seeked forward: the 10 s gap only
	got, n = loadPlay(t, db, "s1")
	if n != 1 || got.watched != 20 || got.pct != 90 || got.state != StateActive {
		t.Fatalf("after playing, pausing and seeking: %+v (%d rows), want 20 s at 90 %%", got, n)
	}

	done := at(40, 960)
	done.Completed = true
	post(t, db, done)
	got, _ = loadPlay(t, db, "s1")
	if got.state != StateCompleted || !got.ended || got.watched != 30 {
		t.Fatalf("completed: %+v", got)
	}
	// a post after the end does not reopen it
	post(t, db, at(50, 970))
	if got, n = loadPlay(t, db, "s1"); n != 1 || got.state != StateCompleted || got.watched != 30 {
		t.Fatalf("post after the end: %+v (%d rows)", got, n)
	}
}

// Closing the stream session stops its play: completed past 95 %, else
// stopped; an unknown session or one already closed is left alone.
func TestStopStreamPlay(t *testing.T) {
	db := streamDB(t)
	t0 := time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC)
	for sid, pos := range map[string]float64{"low": 100, "high": 990} {
		post(t, db, StreamProgress{StreamSessionID: sid, UserID: "u1", VideoID: "v-" + sid,
			VideoDurationSec: 1000, PositionSec: pos, Client: client.None, Now: t0})
	}
	ctx := context.Background()
	for _, sid := range []string{"low", "high", "unknown"} {
		if err := StopStreamPlay(ctx, db, sid, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := loadPlay(t, db, "low"); got.state != StateStopped || !got.ended {
		t.Errorf("low: %+v, want stopped", got)
	}
	if got, _ := loadPlay(t, db, "high"); got.state != StateCompleted || !got.ended {
		t.Errorf("high: %+v, want completed", got)
	}
	if got, n := loadPlay(t, db, "unknown"); n != 0 {
		t.Errorf("unknown session got a play: %+v", got)
	}
}

// A person who turned tracking off has no play recorded.
func TestStreamProgressTrackingOff(t *testing.T) {
	db := streamDB(t)
	if _, err := db.Exec(`INSERT INTO user_analytics_prefs (user_id, track_enabled) VALUES ('u1', 0)`); err != nil {
		t.Fatal(err)
	}
	post(t, db, StreamProgress{StreamSessionID: "s1", UserID: "u1", VideoID: "v1", PositionSec: 10,
		Client: client.None, Now: time.Now().UTC()})
	if got, n := loadPlay(t, db, "s1"); n != 0 {
		t.Fatalf("tracking off, but a play was recorded: %+v", got)
	}
}

// A play left open is interrupted by the reaper like a heartbeat session.
func TestReaperInterruptsAQuietStreamPlay(t *testing.T) {
	db := streamDB(t)
	t0 := time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC)
	post(t, db, StreamProgress{StreamSessionID: "s1", UserID: "u1", VideoID: "v1", PositionSec: 10,
		Client: client.None, Now: t0})
	rp := &Reaper{DB: db, now: func() time.Time { return t0.Add(10 * time.Minute) }}
	reaped, _, err := rp.RunOnce(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := loadPlay(t, db, "s1"); reaped != 1 || got.state != StateInterrupted || !got.ended {
		t.Fatalf("reaped %d, play %+v; want interrupted", reaped, got)
	}
}

func TestFlexTime(t *testing.T) {
	want := time.Date(2026, 10, 26, 20, 0, 1, 0, time.UTC)
	for _, v := range []any{want, want.String(), "2026-10-26 20:00:01", "2026-10-26T20:00:01Z", []byte("2026-10-26 20:00:01")} {
		var f flexTime
		if err := f.Scan(v); err != nil || !f.t.Equal(want) {
			t.Errorf("Scan(%v) = %v, %v", v, f.t, err)
		}
	}
	var f flexTime
	if f.Scan(42) == nil || f.Scan("yesterday") == nil {
		t.Error("an unreadable timestamp must fail")
	}
}
