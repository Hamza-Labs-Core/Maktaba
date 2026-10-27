package analytics

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteSchema is the part of the SQLite twins (slots 0001, 0029, 0034,
// 0081, 0086, 0125, 0126, 0158) the analytics read, with the columns the
// seed needs.
var sqliteSchema = []string{
	`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL, pw_hash TEXT NOT NULL,
		is_admin INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, roots TEXT NOT NULL)`,
	`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id),
		content_hash TEXT NOT NULL UNIQUE, path TEXT NOT NULL, filename TEXT NOT NULL,
		size_bytes INTEGER NOT NULL, mtime TEXT NOT NULL, title TEXT)`,
	`CREATE TABLE tags (id TEXT PRIMARY KEY, name TEXT NOT NULL, name_norm TEXT NOT NULL UNIQUE)`,
	`CREATE TABLE video_tags (video_id TEXT NOT NULL, tag_id TEXT NOT NULL, PRIMARY KEY (video_id, tag_id))`,
	`CREATE TABLE channels (id TEXT PRIMARY KEY, number INTEGER NOT NULL, name TEXT NOT NULL,
		slug TEXT NOT NULL, mode TEXT NOT NULL)`,
	`CREATE TABLE watch_sessions (
		id TEXT PRIMARY KEY, user_id TEXT NOT NULL, video_id TEXT NOT NULL,
		started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT,
		last_heartbeat TEXT NOT NULL DEFAULT (datetime('now')),
		duration_sec INTEGER NOT NULL DEFAULT 0, percent_complete REAL NOT NULL DEFAULT 0,
		state TEXT NOT NULL DEFAULT 'active', device_type TEXT, platform TEXT, quality TEXT,
		ip_addr_hash TEXT, stream_session_id TEXT, client_app TEXT, client_version TEXT)`,
	`CREATE TABLE tv_watch_sessions (
		id TEXT PRIMARY KEY, channel_id TEXT NOT NULL REFERENCES channels(id), user_id TEXT,
		surface TEXT NOT NULL CHECK (surface IN ('app', 'tuner', 'remote')),
		client_app TEXT, client_version TEXT, platform TEXT, device_type TEXT, client_addr_hash TEXT,
		started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		last_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		ended_at TEXT, seconds INTEGER NOT NULL DEFAULT 0, end_reason TEXT)`,
	`CREATE TABLE tv_remote_sessions (
		id TEXT PRIMARY KEY, user_id TEXT NOT NULL, channel_id TEXT NOT NULL, push_id TEXT,
		device TEXT NOT NULL DEFAULT '',
		started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		last_beat_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		ended_at TEXT, end_reason TEXT)`,
	`CREATE TABLE tv_remote_usage_daily (
		day TEXT PRIMARY KEY, minutes_delivered REAL NOT NULL DEFAULT 0,
		peak_recording_minutes REAL NOT NULL DEFAULT 0, cost_usd REAL NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT 'sessions', simulcast_minutes REAL NOT NULL DEFAULT 0)`,
	`CREATE TABLE api_usage_daily (
		day TEXT NOT NULL, client_app TEXT NOT NULL, platform TEXT NOT NULL,
		requests INTEGER NOT NULL DEFAULT 0, errors INTEGER NOT NULL DEFAULT 0,
		total_ms INTEGER NOT NULL DEFAULT 0, slow INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (day, client_app, platform))`,
	`CREATE TABLE api_usage_people_daily (
		day TEXT NOT NULL, client_app TEXT NOT NULL, user_id TEXT NOT NULL,
		client_version TEXT NOT NULL DEFAULT '', PRIMARY KEY (day, client_app, user_id))`,
}

// sqliteAt writes a time the three ways SQLite rows hold one: datetime()'s,
// strftime's ISO form, and the Go driver's own (a time.Time bound as is).
func sqliteAt(t time.Time, f int) any {
	switch f % 3 {
	case 0:
		return t.UTC().Format("2006-01-02 15:04:05")
	case 1:
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	default:
		return t
	}
}

func TestAnalytics_SQLite(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "analytics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1) // as a SQLite home runs: the export must not hold two cursors
	for _, q := range sqliteSchema {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	seed(t, db, sqliteAt)
	runScenario(t, &Handler{DB: db, Driver: "sqlite"})
}
