package log

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// The level file (Epic 53 story 53.11): "Detailed logs for one hour" on the admin's page.
// The supervisor writes "<level> <until RFC3339>" into a file per role
// (MAKTABA_LOG_LEVEL_FILE), and each process reads it every few seconds: before until it
// logs at that level, after it (or with no file) at the level it started with. A file works
// on every platform (SIGUSR1 has no Windows) and reaches a process started later too.

// EnvLevelFile names the role's level file.
const EnvLevelFile = "MAKTABA_LOG_LEVEL_FILE"

// LevelEvery is how often the level file is read.
var LevelEvery = 5 * time.Second

// ReadLevelFile is the level the file asks for at now (ok false: none, or its time passed).
func ReadLevelFile(path string, now time.Time) (slog.Level, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return 0, false
	}
	until, err := time.Parse(time.RFC3339, f[1])
	if err != nil || !now.Before(until) {
		return 0, false
	}
	var l slog.Level
	if l.UnmarshalText([]byte(f[0])) != nil {
		return 0, false
	}
	return l, true
}

// levelFrom is the level to log at: the file's while it lasts, else base.
func levelFrom(path string, base slog.Level, now time.Time) slog.Level {
	if l, ok := ReadLevelFile(path, now); ok {
		return l
	}
	return base
}

// watchLevelFile applies the file every LevelEvery (Init starts it when EnvLevelFile is set).
func watchLevelFile(path string, base slog.Level) {
	go func() {
		t := time.NewTicker(LevelEvery)
		defer t.Stop()
		cur := base
		for range t.C {
			if l := levelFrom(path, base, time.Now()); l != cur {
				cur = l
				levelVar.Set(l)
			}
		}
	}()
}
