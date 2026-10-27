// Package safego starts background goroutines that cannot take the API down.
//
// A panic in a goroutine the HTTP server did not start (a scheduler, a
// reaper, a fire-and-forget job a handler launched) is not caught by the
// request Recoverer: it ends the whole process. Go and Recover catch it,
// log one `goroutine_panic` line at Error level with the goroutine's name
// and stack, and let the rest of the API keep serving. The goroutine
// itself ends; it is not restarted.
package safego

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Go runs fn in a new goroutine under Recover. name says which goroutine
// it was in the log line ("updates_scheduler", "pairing_sweep").
func Go(logger *slog.Logger, name string, fn func()) {
	go func() {
		defer Recover(logger, name)
		fn()
	}()
}

// Recover must be deferred directly (`defer safego.Recover(logger, name)`)
// at the top of a goroutine the caller starts itself, for example one that
// also has to call wg.Done. It stops a panic and logs it.
func Recover(logger *slog.Logger, name string) {
	if v := recover(); v != nil {
		Log(logger, name, v)
	}
}

// Log writes the goroutine_panic line for a panic value the caller
// recovered itself (when it must also clean up after the panic).
func Log(logger *slog.Logger, name string, v any) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error("goroutine panic", "goroutine", name, "panic", fmt.Sprint(v),
		"stack", string(debug.Stack()), "event", "goroutine_panic")
}
