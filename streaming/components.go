package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pgxlisten"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

// componentHost is streaming's name for its loops in the catalog (`streaming:<name>`).
const componentHost = "streaming"

// Loop runs fn as the catalog loop name (Epic 51 story 51.05): on the home's component
// runner when this process has one (it stops and starts with its component), else
// as a plain background goroutine (a node, no database), as before.
func (b *background) Loop(name string, fn func(ctx context.Context)) {
	if r := b.runner; r != nil {
		r.Declare(name, runner.Func(fn))
		return
	}
	b.Go(fn)
}

// On reports whether component id is on here (always, with no runner).
func (b *background) On(id string) bool { return b.runner.On(id) }

// startComponents gives the home's streaming its component runner: every loop declared
// after this follows the home's desired state (a LISTEN on components_changed, a
// revision poll every 30 s) and reports its status with part "streaming". Until the
// database answers every component is on, as before. bg waits for it at shutdown.
func startComponents(bg *background, pool *pgxpool.Pool, logger *slog.Logger) {
	if pool == nil {
		return
	}
	r := runner.New(runner.Options{Host: componentHost, Logger: logger, NoReconcile: runner.ReconcileOff(),
		Context: bg.ctx})
	// database/sql over the pool (it keeps no idle connection, so pool.Close never
	// waits on it; closing it leaves the pool open)
	db := stdlib.OpenDBFromPool(pool)
	st := store.New(db)
	st.Version = version.Version
	src := runner.Source{Store: st}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		src.Listener = pgxlisten.New(pgxlisten.Dial(dsn), pgxlisten.Options{})
	}
	bg.runner = r
	bg.Go(func(ctx context.Context) {
		defer func() { _ = db.Close() }()
		r.Run(ctx, src)
	})
}
