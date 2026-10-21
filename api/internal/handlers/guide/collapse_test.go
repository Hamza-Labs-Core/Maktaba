package guide

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// The web guide follows what was changed in rules tv.guide_* (app_settings
// rules.tv, written when a TV change goes live); a value out of range keeps
// its default.
func TestLoadGuideRules_ReadsTheTVRules(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "guide.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL,
		updated_at TEXT NOT NULL DEFAULT (datetime('now')), updated_by TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv', $1)`,
		`{"guide_max_padding_s":300,"guide_filler_title":"Breaks","guide_min_program_s":99999}`); err != nil {
		t.Fatal(err)
	}
	gr := loadGuideRules(context.Background(), db)
	if gr.MaxPadding != 300*time.Second || gr.FillerTitle != "Breaks" || gr.MinProgram != 60*time.Second {
		t.Errorf("rules: %+v", gr)
	}
}
