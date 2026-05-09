//go:build !windows

package log

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// installSIGUSR1 starts a goroutine that cycles the active log level on
// each SIGUSR1: info -> debug -> warn -> info. Operators flip to debug
// without restarting the binary; flipping back to warn quiets a noisy
// process during an incident.
func installSIGUSR1() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		levels := []slog.Level{slog.LevelInfo, slog.LevelDebug, slog.LevelWarn}
		i := 0
		for range ch {
			i = (i + 1) % len(levels)
			levelVar.Set(levels[i])
			if Default != nil {
				Default.Info("log level cycled", "new_level", levels[i].String())
			}
		}
	}()
}
