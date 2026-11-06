package supervisor

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Story 53.11: an hour of debug for the pipeline: its level file says so until then, the
// role's env names the file, and info ends it at once.
func TestSetLevelFor(t *testing.T) {
	var cfg config.Config
	cfg.Server.DataDir = t.TempDir()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	until, err := SetLevelFor(cfg, RolePipeline, "debug", time.Hour, now)
	if err != nil || !until.Equal(now.Add(time.Hour)) {
		t.Fatalf("set: %v %v", until, err)
	}
	if got := LevelUntil(cfg, RolePipeline, now); !got.Equal(until) {
		t.Fatalf("until: %v", got)
	}
	if got := LevelUntil(cfg, RolePipeline, now.Add(2*time.Hour)); !got.IsZero() {
		t.Fatalf("after the hour: %v", got)
	}
	if _, err := SetLevelFor(cfg, RolePipeline, "info", 0, now); err != nil {
		t.Fatal(err)
	}
	if got := LevelUntil(cfg, RolePipeline, now); !got.IsZero() {
		t.Fatalf("info ends it: %v", got)
	}
	if _, err := SetLevelFor(cfg, RolePostgres, "debug", time.Hour, now); err == nil {
		t.Error("postgres has no level file")
	}
	if _, err := SetLevelFor(cfg, RoleAPI, "trace", time.Hour, now); err == nil {
		t.Error("an unknown level")
	}
	if !strings.HasSuffix(LevelFile(cfg, RoleAPI), filepath.Join("logs", "level", "api")) {
		t.Errorf("file: %s", LevelFile(cfg, RoleAPI))
	}
}
