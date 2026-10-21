package nodemode

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func settingsDB(t *testing.T, value string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if value != "" {
		if _, err := db.Exec(`INSERT INTO app_settings VALUES ('rules.cluster', ?)`, value); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestLoadRules(t *testing.T) {
	ctx := context.Background()
	if got, err := LoadRules(ctx, settingsDB(t, "")); err != nil || got != DefaultRules() {
		t.Fatalf("no row: %+v %v, want the defaults and no error", got, err)
	}
	got, err := LoadRules(ctx, settingsDB(t, `{"offline_read_minutes": 0, "delegate_unreachable_grace_seconds": 2}`))
	if err != nil || got.OfflineReadMinutes != 0 || got.GraceSeconds != DefaultRules().GraceSeconds {
		t.Fatalf("0 minutes is a value, 2 s of grace is out of range: %+v %v", got, err)
	}
	db := settingsDB(t, "")
	_ = db.Close()
	if got, err := LoadRules(ctx, db); err == nil || got != DefaultRules() {
		t.Fatalf("a failed read: %+v %v, want the defaults with the error", got, err)
	}
}

func TestCachedRulesKeepTheLastGoodSection(t *testing.T) {
	db := settingsDB(t, `{"offline_read_minutes": 30}`)
	rules := CachedRules(db, 0) // read every time
	if got := rules().OfflineReadMinutes; got != 30 {
		t.Fatalf("read: %d", got)
	}
	_ = db.Close() // the home's database goes away
	if got := rules().OfflineReadMinutes; got != 30 {
		t.Fatalf("after a failed read: %d, want the last good 30 (not the default)", got)
	}
}
