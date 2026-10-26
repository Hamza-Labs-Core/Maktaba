package usage

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
)

const (
	userA = "00000000-0000-4000-8000-0000000000a1"
	userB = "00000000-0000-4000-8000-0000000000b1"
)

// usageDB is SQLite with slot 0158's tables (and users, for the people's key).
func usageDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY)`,
		`INSERT INTO users VALUES ('` + userA + `'), ('` + userB + `')`,
		`CREATE TABLE api_usage_daily (day TEXT NOT NULL, client_app TEXT NOT NULL, platform TEXT NOT NULL,
			requests INTEGER NOT NULL DEFAULT 0, errors INTEGER NOT NULL DEFAULT 0,
			total_ms INTEGER NOT NULL DEFAULT 0, slow INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (day, client_app, platform))`,
		`CREATE TABLE api_usage_people_daily (day TEXT NOT NULL, client_app TEXT NOT NULL,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			client_version TEXT NOT NULL DEFAULT '', PRIMARY KEY (day, client_app, user_id))`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

type dayRow struct {
	requests, errors, totalMS, slow int64
}

func row(t *testing.T, db *sql.DB, day, app, platform string) dayRow {
	t.Helper()
	var r dayRow
	err := db.QueryRow(`SELECT requests, errors, total_ms, slow FROM api_usage_daily
		WHERE day = $1 AND client_app = $2 AND platform = $3`, day, app, platform).
		Scan(&r.requests, &r.errors, &r.totalMS, &r.slow)
	if err == sql.ErrNoRows {
		return dayRow{}
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Requests are counted per day, app and platform, and every flush adds to the
// day's row.
func TestCounterFlushAddsToTheDay(t *testing.T) {
	db := usageDB(t)
	now := time.Date(2026, 10, 26, 23, 59, 0, 0, time.UTC)
	c := &Counter{now: func() time.Time { return now }}
	c.Attach(db)
	player := client.Parse("player/1.3.0 (ios; phone)")
	web := client.Parse("web/0.3.25 (macos; desktop)")

	c.Record(player, userA, 200, 20*time.Millisecond)
	c.Record(player, userA, 503, 30*time.Millisecond)
	c.Record(player, "", 200, 1500*time.Millisecond) // slow, no one signed in
	c.Record(web, userB, 404, 5*time.Millisecond)    // a 4xx is no error
	ctx := context.Background()
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := row(t, db, "2026-10-26", "player", "ios"); got != (dayRow{3, 1, 1550, 1}) {
		t.Errorf("player: %+v", got)
	}
	if got := row(t, db, "2026-10-26", "web", "macos"); got != (dayRow{1, 0, 5, 0}) {
		t.Errorf("web: %+v", got)
	}

	c.Record(player, userA, 200, 10*time.Millisecond)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := row(t, db, "2026-10-26", "player", "ios"); got != (dayRow{4, 1, 1560, 1}) {
		t.Errorf("player after the second flush: %+v", got)
	}

	// past midnight: a new day's row
	now = now.Add(2 * time.Minute)
	c.Record(player, userA, 200, 10*time.Millisecond)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := row(t, db, "2026-10-27", "player", "ios"); got != (dayRow{1, 0, 10, 0}) {
		t.Errorf("the next day: %+v", got)
	}

	// who used which app, with the version they used
	if n := count(t, db, `SELECT count(*) FROM api_usage_people_daily WHERE day = '2026-10-26'`); n != 2 {
		t.Errorf("people on the 26th: %d, want 2", n)
	}
	var v string
	if err := db.QueryRow(`SELECT client_version FROM api_usage_people_daily
		WHERE day = '2026-10-26' AND client_app = 'player' AND user_id = $1`, userA).Scan(&v); err != nil || v != "1.3.0" {
		t.Errorf("player version: %q, %v", v, err)
	}
	if n := count(t, db, `SELECT count(*) FROM api_usage_people_daily WHERE day = '2026-10-27'`); n != 1 {
		t.Errorf("people on the 27th: %d, want 1", n)
	}
}

// A person is written once a day, and again when their version changes; one
// that is no user (deleted, or not a uuid) is dropped without the others.
func TestCounterPeople(t *testing.T) {
	db := usageDB(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	c := &Counter{now: func() time.Time { return now }}
	c.Attach(db)
	ctx := context.Background()

	c.Record(client.Parse("player/1.0 (ios; phone)"), userA, 200, 0)
	c.Record(client.Parse("player/1.0 (ios; phone)"), "00000000-0000-4000-8000-00000000dead", 200, 0)
	c.Record(client.Parse("player/1.0 (ios; phone)"), "not-a-uuid", 200, 0)
	if err := c.Flush(ctx); err == nil {
		t.Error("a person no user should report the failure")
	}
	if n := count(t, db, `SELECT count(*) FROM api_usage_people_daily`); n != 1 {
		t.Fatalf("people: %d, want 1", n)
	}
	// written already today: not again
	c.Record(client.Parse("player/1.0 (ios; phone)"), userA, 200, 0)
	if len(c.people) != 0 {
		t.Errorf("a person written today was held again: %v", c.people)
	}
	// a new version is
	c.Record(client.Parse("player/1.1 (ios; phone)"), userA, 200, 0)
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := db.QueryRow(`SELECT client_version FROM api_usage_people_daily WHERE user_id = $1`, userA).Scan(&v); err != nil || v != "1.1" {
		t.Errorf("version after the update: %q, %v", v, err)
	}
}

// Without a database nothing is counted (a full node), and a nil counter is safe.
func TestCounterWithoutADatabase(t *testing.T) {
	c := &Counter{}
	c.Record(client.None, userA, 200, time.Millisecond)
	if len(c.rows) != 0 {
		t.Error("counted without a database")
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Error(err)
	}
	var nilC *Counter
	nilC.Record(client.None, userA, 200, 0)
	if err := nilC.Flush(context.Background()); err != nil {
		t.Error(err)
	}
}

// A flush that fails keeps its tallies for the next one.
func TestCounterKeepsWhatAFlushCouldNotWrite(t *testing.T) {
	db := usageDB(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	c := &Counter{now: func() time.Time { return now }}
	c.Attach(db)
	ctx := context.Background()
	c.Record(client.Parse("tv/1.0 (androidtv; tv)"), "", 200, time.Millisecond)
	if _, err := db.Exec(`ALTER TABLE api_usage_daily RENAME TO away`); err != nil {
		t.Fatal(err)
	}
	if err := c.Flush(ctx); err == nil {
		t.Fatal("the flush should fail without its table")
	}
	c.Record(client.Parse("tv/1.0 (androidtv; tv)"), "", 200, time.Millisecond)
	if _, err := db.Exec(`ALTER TABLE away RENAME TO api_usage_daily`); err != nil {
		t.Fatal(err)
	}
	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := row(t, db, "2026-10-26", "tv", "androidtv"); got.requests != 2 {
		t.Errorf("tv: %+v, want both requests", got)
	}
}

// The header is the caller's: past maxKeys distinct apps and platforms a day,
// the rest count as `other`.
func TestCounterBoundsItsKeys(t *testing.T) {
	db := usageDB(t)
	c := &Counter{}
	c.Attach(db)
	for i := 0; i < maxKeys+50; i++ {
		c.Record(client.Parse(fmt.Sprintf("app%d/1 (p%d; phone)", i, i)), "", 200, 0)
	}
	if len(c.rows) != maxKeys+1 {
		t.Fatalf("held %d tallies, want %d", len(c.rows), maxKeys+1)
	}
	if t2 := c.rows[key{time.Now().UTC().Format(time.DateOnly), "other", "other"}]; t2 == nil || t2.requests != 50 {
		t.Fatalf("other: %+v", t2)
	}
}

// Retention: days before the cutoff go, the cutoff's day stays.
func TestPurge(t *testing.T) {
	db := usageDB(t)
	for _, d := range []string{"2025-10-25", "2025-10-26", "2026-10-26"} {
		if _, err := db.Exec(`INSERT INTO api_usage_daily (day, client_app, platform, requests) VALUES ($1, 'web', 'web', 1)`, d); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO api_usage_people_daily (day, client_app, user_id) VALUES ($1, 'web', $2)`, d, userA); err != nil {
			t.Fatal(err)
		}
	}
	n, err := Purge(context.Background(), db, time.Date(2025, 10, 26, 12, 0, 0, 0, time.UTC))
	if err != nil || n != 2 {
		t.Fatalf("purged %d, %v; want 2", n, err)
	}
	if left := count(t, db, `SELECT count(*) FROM api_usage_daily`) + count(t, db, `SELECT count(*) FROM api_usage_people_daily`); left != 4 {
		t.Fatalf("%d rows left, want 4", left)
	}
}

// Run flushes on its tick and once more when it stops.
func TestRunFlushesAtTheEnd(t *testing.T) {
	db := usageDB(t)
	c := &Counter{}
	c.Attach(db)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx, time.Hour); close(done) }()
	c.Record(client.Parse("remote/1.0 (android; tablet)"), userB, 200, time.Millisecond)
	cancel()
	<-done
	if n := count(t, db, `SELECT count(*) FROM api_usage_daily WHERE client_app = 'remote'`); n != 1 {
		t.Fatalf("remote rows after the end: %d, want 1", n)
	}
}
