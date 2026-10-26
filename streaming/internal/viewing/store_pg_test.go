package viewing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

// The tracker's writes on a migrated database: the app and tuner viewings, the
// tuner leases, the tracking switch, the viewer counts, what a previous run left
// open, and a channel or person that is gone.
func TestPGStore(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	ch, ch2, gone := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'One', 'one', 'shuffle'),
		($2, 102, 'Two', 'two', 'shuffle')`, ch, ch2)
	alice, bob := uuid.New(), uuid.New()
	exec(`INSERT INTO users (id, username, pw_hash) VALUES ($1, 'alice', 'x'), ($2, 'bob', 'x')`, alice, bob)
	exec(`INSERT INTO user_analytics_prefs (user_id, track_enabled) VALUES ($1, false)`, bob)
	exec(`INSERT INTO tv_cluster_secret (id, stream_key) VALUES (1, '\x0102030405060708090a0b0c0d0e0f10')`)
	// a previous run's open viewing and lease, and a remote viewing that is not ours
	exec(`INSERT INTO tv_watch_sessions (channel_id, user_id, surface, started_at, last_seen_at)
		VALUES ($1, $2, 'app', now() - interval '10 minutes', now() - interval '5 minutes')`, ch, alice)
	exec(`INSERT INTO tv_watch_sessions (channel_id, user_id, surface) VALUES ($1, $2, 'remote')`, ch, alice)
	exec(`INSERT INTO hdhr_tuner_leases (channel_id, client_addr) VALUES ($1, '10.0.0.1')`, ch)
	exec(`INSERT INTO channel_runtime (channel_id, host, state, viewer_count) VALUES ($1, 'h', 'live', 0),
		($2, 'h', 'live', 7)`, ch, ch2)

	st := &PGStore{Pool: pool}
	c := newClock()
	c.t = time.Now().UTC().Truncate(time.Second)
	tr := NewTracker(st)
	tr.Now = c.now
	tr.Viewers = tr.AppViewers
	web := Client{App: "web", Version: "0.3.25", Platform: "macos", Device: "desktop"}
	tr.Segment(ch, Viewer{User: alice, Client: web})
	tr.Segment(ch, Viewer{User: bob, Client: web})                      // tracking off
	tr.Segment(ch, Viewer{Client: web, Addr: "192.168.1.4"})            // no person
	tr.Segment(gone, Viewer{User: alice, Client: web})                  // no such channel
	tr.Segment(ch2, Viewer{User: uuid.New(), Client: Client{App: "x"}}) // no such person
	closeTuner := tr.OpenTuner(ch, "192.168.1.20")
	c.add(20 * time.Second)
	tr.Segment(ch, Viewer{User: alice, Client: web})
	if err := tr.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	// the previous run's open viewing ended (idle, at its last segment, 300 s); the remote one untouched
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE surface = 'app' AND end_reason = 'idle'
		AND ended_at = last_seen_at AND seconds = 300`); n != 1 {
		t.Fatalf("leftover viewings ended: %d", n)
	}
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE surface = 'remote' AND ended_at IS NULL`); n != 1 {
		t.Fatalf("remote viewing touched: %d", n)
	}
	// alice's viewing: open, 20 s, her app; bob's and the anonymous one not written
	var (
		app, ver, plat, dev string
		secs                int
		ended               *time.Time
	)
	if err := pool.QueryRow(ctx, `SELECT client_app, client_version, platform, device_type, seconds, ended_at
		FROM tv_watch_sessions WHERE user_id = $1 AND surface = 'app' AND end_reason IS NULL`, alice).
		Scan(&app, &ver, &plat, &dev, &secs, &ended); err != nil {
		t.Fatal(err)
	}
	if app != "web" || ver != "0.3.25" || plat != "macos" || dev != "desktop" || secs != 20 || ended != nil {
		t.Fatalf("alice: %s %s %s %s %d %v", app, ver, plat, dev, secs, ended)
	}
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE user_id = $1`, bob); n != 0 {
		t.Fatalf("bob (tracking off) written: %d", n)
	}
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE surface = 'app' AND user_id IS NULL`); n != 0 {
		t.Fatalf("a viewer with no person written: %d", n)
	}
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE channel_id = $1`, ch2); n != 0 {
		t.Fatalf("an unknown person written: %d", n)
	}
	// the tuner: a viewing with the address hashed, never raw, and a lease with it
	var hash string
	if err := pool.QueryRow(ctx, `SELECT client_addr_hash, seconds FROM tv_watch_sessions WHERE surface = 'tuner'`).
		Scan(&hash, &secs); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 || hash == "192.168.1.20" || secs != 20 {
		t.Fatalf("tuner hash %q seconds %d", hash, secs)
	}
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE client_addr_hash LIKE '%192.168%'`); n != 0 {
		t.Fatal("a raw address in tv_watch_sessions")
	}
	if n := count(`SELECT count(*) FROM hdhr_tuner_leases WHERE channel_id = $1 AND client_addr = '192.168.1.20'`, ch); n != 1 {
		t.Fatalf("leases: %d (the previous run's gone, ours there)", n)
	}
	if n := count(`SELECT count(*) FROM hdhr_tuner_leases`); n != 1 {
		t.Fatalf("leases: %d, want 1", n)
	}
	// viewer counts: every app viewer of the channel (3: alice, bob, the anonymous one); ch2's 1
	if n := count(`SELECT viewer_count FROM channel_runtime WHERE channel_id = $1`, ch); n != 3 {
		t.Fatalf("viewer_count %d, want 3", n)
	}
	if n := count(`SELECT viewer_count FROM channel_runtime WHERE channel_id = $1`, ch2); n != 1 {
		t.Fatalf("ch2 viewer_count %d, want 1", n)
	}

	// the same address hashes the same (one way to tell a tuner client apart)
	second := tr.OpenTuner(ch, "192.168.1.20")
	c.add(30 * time.Second)
	closeTuner()
	if err := tr.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(DISTINCT client_addr_hash) FROM tv_watch_sessions WHERE surface = 'tuner'`); n != 1 {
		t.Fatalf("hashes of one address: %d", n)
	}
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE surface = 'tuner' AND end_reason = 'closed'
		AND seconds = 50`); n != 1 {
		t.Fatal("the closed tuner viewing")
	}
	if n := count(`SELECT count(*) FROM hdhr_tuner_leases`); n != 1 {
		t.Fatalf("leases after one closed: %d, want 1", n)
	}
	// 60 s quiet: alice's viewing ends at her last segment; the viewers go to none
	c.add(DefaultIdle)
	second()
	if err := tr.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE user_id = $1 AND surface = 'app'
		AND end_reason = 'idle' AND seconds = 20 AND ended_at = last_seen_at`, alice); n != 1 {
		t.Fatalf("alice's ended viewing: %d", n)
	}
	if n := count(`SELECT COALESCE(SUM(viewer_count), 0) FROM channel_runtime`); n != 0 {
		t.Fatalf("viewers after the quiet: %d", n)
	}
	if n := count(`SELECT count(*) FROM hdhr_tuner_leases`); n != 0 {
		t.Fatalf("leases after both closed: %d", n)
	}
	if n := count(`SELECT count(*) FROM tv_watch_sessions WHERE ended_at IS NULL AND surface <> 'remote'`); n != 0 {
		t.Fatalf("open viewings left: %d", n)
	}
}
