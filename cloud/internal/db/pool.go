// Package db wires the Postgres pool and migration runner. We use
// database/sql with lib/pq because the cloud service has the same
// throughput profile as the on-prem api server — the extra control of
// pgx is not needed for v1 and lib/pq keeps the dependency footprint
// identical to the local api binary.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq" // registers the "postgres" database/sql driver
	"github.com/pressly/goose/v3"
)

// Pool wraps *sql.DB so we can attach helpers without exposing the
// underlying handle to packages that should be going through stores.
type Pool struct {
	DB *sql.DB
}

// Open dials the configured Postgres instance, applies pool sizing, and
// returns a Pool. It does NOT verify connectivity — call Ping at the
// readyz layer so transient DNS hiccups do not crash boot.
func Open(url string, maxOpen, maxIdle int, lifetime time.Duration) (*Pool, error) {
	if url == "" {
		return nil, errors.New("db: empty url")
	}
	d, err := sql.Open("postgres", url)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	d.SetMaxOpenConns(maxOpen)
	d.SetMaxIdleConns(maxIdle)
	d.SetConnMaxLifetime(lifetime)
	return &Pool{DB: d}, nil
}

// Ping verifies that the configured database is reachable. Used by the
// /readyz probe and by the boot sequence to fail fast on misconfig.
func (p *Pool) Ping(ctx context.Context) error {
	if p == nil || p.DB == nil {
		return errors.New("db: not initialised")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return p.DB.PingContext(ctx)
}

func (p *Pool) Close() error {
	if p == nil || p.DB == nil {
		return nil
	}
	return p.DB.Close()
}

// Migrator wraps goose with the embedded migrations FS. The
// pg_advisory_lock id is documented in cloud/migrations/README.md so
// two pods racing `up` serialize cleanly.
type Migrator struct {
	pool *Pool
	dir  embed.FS
	root string
	mu   sync.Mutex
}

// NewMigrator wires the pool, the embedded FS, and the root path within
// that FS where SQL files live (use "." when migrations are at the
// embed root).
func NewMigrator(p *Pool, dir embed.FS, root string) *Migrator {
	if root == "" {
		root = "."
	}
	return &Migrator{pool: p, dir: dir, root: root}
}

// Up applies all pending migrations.
func (m *Migrator) Up(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	goose.SetBaseFS(m.dir)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrator: dialect: %w", err)
	}
	return goose.UpContext(ctx, m.pool.DB, m.root)
}

// Down rolls back the most recent N migrations. Mainly used in dev /
// CI to verify migrations are reversible.
func (m *Migrator) Down(ctx context.Context, steps int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	goose.SetBaseFS(m.dir)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	for i := 0; i < steps; i++ {
		if err := goose.DownContext(ctx, m.pool.DB, m.root); err != nil {
			return err
		}
	}
	return nil
}

// AtHead reports whether the database has every embedded migration
// applied. Used by /readyz so the LB only routes traffic to a pod whose
// schema matches its binary.
func (m *Migrator) AtHead(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	goose.SetBaseFS(m.dir)
	if err := goose.SetDialect("postgres"); err != nil {
		return false, err
	}
	cur, err := goose.GetDBVersionContext(ctx, m.pool.DB)
	if err != nil {
		return false, err
	}
	migs, err := goose.CollectMigrations(m.root, 0, goose.MaxVersion)
	if err != nil {
		return false, err
	}
	if len(migs) == 0 {
		return true, nil
	}
	return cur >= migs[len(migs)-1].Version, nil
}
