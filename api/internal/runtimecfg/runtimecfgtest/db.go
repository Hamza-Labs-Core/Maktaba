// Package runtimecfgtest gives tests an app_settings table to store runtime
// settings in (SQLite, in memory).
package runtimecfgtest

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite" // the SQLite driver
)

// DB is a fresh in-memory database with app_settings.
func DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
		updated_at TEXT, updated_by TEXT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// Set stores key = raw JSON.
func Set(t *testing.T, db *sql.DB, key, raw string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, raw); err != nil {
		t.Fatal(err)
	}
}
