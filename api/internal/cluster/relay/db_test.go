package relay

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// appSettingsDB is a SQLite file with the app_settings table (as the rules package's tests make it).
func appSettingsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE app_settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_by TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// DBArtBase (issue #827): the dizqueTV plugin's logo host is rules.planner.dz_art_url,
// trimmed; with nothing stored it is the schema's default, empty (logos uploaded to dizqueTV).
func TestDBArtBaseReadsThePlannersLogoHost(t *testing.T) {
	ctx := context.Background()

	got, err := DBArtBase(nil)(ctx)
	if err != nil || got != "" {
		t.Fatalf("no database: %q, %v; want the default, empty", got, err)
	}

	db := appSettingsDB(t)
	if got, err := DBArtBase(db)(ctx); err != nil || got != "" {
		t.Fatalf("nothing stored: %q, %v; want the default, empty", got, err)
	}

	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.planner', ?)`,
		`{"dz_art_url": "  https://art.example.com/logos  "}`); err != nil {
		t.Fatal(err)
	}
	if got, err := DBArtBase(db)(ctx); err != nil || got != "https://art.example.com/logos" {
		t.Fatalf("stored: %q, %v; want the address, trimmed", got, err)
	}
}

// DBPublicBase: the address the TVs reach dizqueTV at is rules.planner.dz_public_url,
// trimmed; with nothing stored it is empty (dizqueTV's own address).
func TestDBPublicBaseReadsThePlannersPublicAddress(t *testing.T) {
	ctx := context.Background()
	db := appSettingsDB(t)
	if got, err := DBPublicBase(db)(ctx); err != nil || got != "" {
		t.Fatalf("nothing stored: %q, %v; want empty", got, err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.planner', ?)`,
		`{"dz_public_url": " http://tv.lan:8000 ", "dz_art_url": "https://art.example.com"}`); err != nil {
		t.Fatal(err)
	}
	if got, err := DBPublicBase(db)(ctx); err != nil || got != "http://tv.lan:8000" {
		t.Fatalf("stored: %q, %v; want the address, trimmed", got, err)
	}
}
