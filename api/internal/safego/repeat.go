package safego

import (
	"log/slog"
	"sync"
)

// Repeat picks the level of a failure a loop retries every few seconds: Warn
// the first time an error is seen (and when it changes), Debug while the same
// error repeats, so the failure is in the log once without flooding it. A
// success (nil) resets it. The zero value is ready to use.
type Repeat struct {
	mu   sync.Mutex
	last string
}

// Level returns the level for err and records it.
func (r *Repeat) Level(err error) slog.Level {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.last = ""
		return slog.LevelDebug
	}
	if s := err.Error(); s != r.last {
		r.last = s
		return slog.LevelWarn
	}
	return slog.LevelDebug
}
