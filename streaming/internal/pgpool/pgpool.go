// Package pgpool opens the streaming service's Postgres pools with a fixed size.
//
// pgxpool.New sizes a pool by the machine's CPUs (28 on a 28-core Mac) and keeps an idle
// connection for 30 minutes. A home runs the streaming service and two or three TV slots
// beside the API, the pipeline workers and every machine's own pools, all on one Postgres:
// pools sized by CPU filled its connection slots and the API's next query failed with 53300
// ("remaining connection slots are reserved"). Each pool here has a cap of its own and lets
// an idle connection go after a minute.
package pgpool

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Serve is the streaming service's pool: media info, transcripts, the TV loops, two
	// of them holding a LISTEN connection each.
	Serve = 10
	// Slot is a TV slot's pool: its beat, its placement LISTEN and the channel's reads.
	Slot = 6
	// Copy is the pool on this machine's copy of the home's database (its own Postgres).
	Copy = 4
	// IdleTime is how long an unused connection stays open.
	IdleTime = time.Minute
)

// New is pgxpool.New with at most maxConns connections, none kept open while idle for
// longer than IdleTime. It connects lazily, as pgxpool.New does.
func New(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := Config(dsn, maxConns)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// Config is the pool configuration New opens.
func Config(dsn string, maxConns int32) (*pgxpool.Config, error) {
	if maxConns < 1 {
		return nil, fmt.Errorf("pgpool: a pool needs at least one connection (got %d)", maxConns)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = IdleTime
	return cfg, nil
}
