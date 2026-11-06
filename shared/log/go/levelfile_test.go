package log

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Story 53.11: the level file's level lasts until its time, then the start level is back;
// a missing or unreadable file is the start level.
func TestLevelFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "api")
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	if l := levelFrom(p, slog.LevelInfo, now); l != slog.LevelInfo {
		t.Fatalf("no file: %v", l)
	}
	_ = os.WriteFile(p, []byte("debug "+now.Add(time.Hour).Format(time.RFC3339)+"\n"), 0o600)
	if l := levelFrom(p, slog.LevelInfo, now); l != slog.LevelDebug {
		t.Fatalf("within the hour: %v", l)
	}
	if l := levelFrom(p, slog.LevelInfo, now.Add(time.Hour)); l != slog.LevelInfo {
		t.Fatalf("after the hour: %v", l)
	}
	for _, bad := range []string{"debug", "loud 2026-11-06T11:00:00Z", "debug tomorrow"} {
		_ = os.WriteFile(p, []byte(bad), 0o600)
		if l := levelFrom(p, slog.LevelWarn, now); l != slog.LevelWarn {
			t.Errorf("%q: %v", bad, l)
		}
	}
}
