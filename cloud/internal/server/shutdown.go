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
	return srv.Shutdown(shCtx)
}
