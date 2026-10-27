package safego

import (
	"errors"
	"log/slog"
	"testing"
)

func TestRepeatWarnsOncePerError(t *testing.T) {
	var r Repeat
	a, b := errors.New("a"), errors.New("b")
	steps := []struct {
		err  error
		want slog.Level
	}{
		{a, slog.LevelWarn},
		{a, slog.LevelDebug},
		{a, slog.LevelDebug},
		{b, slog.LevelWarn},
		{nil, slog.LevelDebug},
		{b, slog.LevelWarn},
	}
	for i, s := range steps {
		if got := r.Level(s.err); got != s.want {
			t.Fatalf("step %d (%v): %v, want %v", i, s.err, got, s.want)
		}
	}
}
