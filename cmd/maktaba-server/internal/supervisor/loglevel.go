package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// "Detailed logs for one hour" (Epic 53 story 53.11): the supervisor writes a role's level
// file, "<level> <until>", which the role's logger reads every few seconds
// (shared/log/go/levelfile.go, the pipeline's log/levelfile.py); after until, it logs at
// its start level again, with no one acting.

// LevelFile is a role's level file: <data>/logs/level/<role>.
func LevelFile(cfg config.Config, role Role) string {
	return filepath.Join(cfg.Server.DataDir, "logs", "level", string(role))
}

// LevelRoles are the roles whose level an admin can raise.
var LevelRoles = []Role{RoleAPI, RoleStreaming, RolePipeline, RoleLogs}

// ErrLevelRole is a role whose level cannot be raised here.
var ErrLevelRole = errors.New("no such service")

// SetLevelFor makes role log at level until now+d ("info" ends it at once).
func SetLevelFor(cfg config.Config, role Role, level string, d time.Duration, now time.Time) (time.Time, error) {
	ok := false
	for _, r := range LevelRoles {
		ok = ok || r == role
	}
	if !ok {
		return time.Time{}, ErrLevelRole
	}
	level = strings.ToLower(strings.TrimSpace(level))
	if level != "debug" && level != "info" {
		return time.Time{}, fmt.Errorf("level %q: debug or info", level)
	}
	p := LevelFile(cfg, role)
	if level == "info" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return time.Time{}, err
		}
		return time.Time{}, nil
	}
	if d <= 0 || d > 4*time.Hour {
		d = time.Hour
	}
	until := now.Add(d).UTC().Truncate(time.Second)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return time.Time{}, err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(level+" "+until.Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return time.Time{}, err
	}
	return until, os.Rename(tmp, p)
}

// LevelUntil is when role's raised level ends (zero: it is not raised).
func LevelUntil(cfg config.Config, role Role, now time.Time) time.Time {
	b, err := os.ReadFile(LevelFile(cfg, role))
	if err != nil {
		return time.Time{}
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, f[1])
	if err != nil || !now.Before(t) {
		return time.Time{}
	}
	return t
}
