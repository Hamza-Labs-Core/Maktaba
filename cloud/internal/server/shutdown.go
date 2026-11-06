package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Run binds the HTTP listener and blocks until SIGTERM/SIGINT, then
// drains in-flight requests up to `grace` before forcing close.
func Run(ctx context.Context, logger *slog.Logger, srv *http.Server, grace time.Duration) error {
	return RunDraining(ctx, logger, srv, grace, nil)
}

// RunDraining is Run with drain called first on shutdown, inside the same
// grace: the relay moves its homes' tunnels (story 50.03), which
// http.Server.Shutdown cannot do (it never closes a hijacked websocket).
// The listener keeps serving while drain runs, so in-flight requests on
// draining tunnels finish; Shutdown then gets what is left of the grace.
func RunDraining(ctx context.Context, logger *slog.Logger, srv *http.Server, grace time.Duration, drain func(context.Context)) error {
	errs := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	case sig := <-sigs:
		logger.Info("shutdown signal", "signal", sig.String())
	}

	shCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if drain != nil {
		start := time.Now()
		drain(shCtx)
		logger.Info("tunnels drained", "took", time.Since(start).String())
	}
	return srv.Shutdown(shCtx)
}
