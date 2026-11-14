package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
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

// RunAll serves every srv (the api's user and admin listeners, Epic 50 story 50.32) until
// ctx ends, SIGINT or SIGTERM arrives, or one of them fails; then shuts them all down
// inside one grace. A listener that fails takes the others down with it: the node's
// systemd unit restarts the role whole, rather than leaving half of it serving.
func RunAll(ctx context.Context, logger *slog.Logger, grace time.Duration, srvs ...*http.Server) error {
	return RunAllDraining(ctx, logger, grace, 0, nil, srvs...)
}

// RunAllDraining is RunAll that, on shutdown, first calls unready (the node's /readyz then
// answers 503) and keeps serving for delay before it closes the listeners (Epic 50 story
// 50.17): the load balancer in front of the api pool takes the node out after its failed
// checks, so no new request meets a closed port, and the requests already in flight finish
// inside grace. A listener that fails skips the delay.
func RunAllDraining(ctx context.Context, logger *slog.Logger, grace, delay time.Duration, unready func(), srvs ...*http.Server) error {
	errs := make(chan error, len(srvs))
	for _, srv := range srvs {
		go func(srv *http.Server) {
			logger.Info("listening", "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- fmt.Errorf("%s: %w", srv.Addr, err)
			}
		}(srv)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	var failed error
	select {
	case failed = <-errs:
		logger.Error("a listener failed; stopping the others", "err", failed)
	case <-ctx.Done():
	case sig := <-sigs:
		logger.Info("shutdown signal", "signal", sig.String())
	}
	if failed == nil && delay > 0 {
		if unready != nil {
			unready()
		}
		logger.Info("draining: /readyz answers 503 while the load balancer takes this node out",
			"delay", delay.String(), "event", "api_draining")
		select {
		case <-time.After(delay):
		case failed = <-errs:
			logger.Error("a listener failed while draining", "err", failed)
		case sig := <-sigs:
			logger.Info("a second shutdown signal: closing now", "signal", sig.String())
		}
	}
	shCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	var wg sync.WaitGroup
	shut := make([]error, len(srvs))
	for i, srv := range srvs {
		wg.Add(1)
		go func(i int, srv *http.Server) {
			defer wg.Done()
			shut[i] = srv.Shutdown(shCtx)
		}(i, srv)
	}
	wg.Wait()
	return errors.Join(append([]error{failed}, shut...)...)
}
