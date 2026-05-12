package perf

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PoolConfig captures the canonical *sql.DB pool sizing. Story 18.7
// owns the numbers; ApplyPool is called once at boot.
type PoolConfig struct {
	MaxOpen     int           // hard cap on concurrent connections
	MaxIdle     int           // idle conns kept alive
	MaxIdleTime time.Duration // recycle after idle this long
	MaxLifetime time.Duration // recycle after this long regardless
	ConnTimeout time.Duration // ping deadline
}

// DefaultPoolConfig is what main.go uses when no env override is set.
// Tuned for a single-host install with ~500 concurrent active sessions.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpen:     50,
		MaxIdle:     25,
		MaxIdleTime: 5 * time.Minute,
		MaxLifetime: 30 * time.Minute,
		ConnTimeout: 5 * time.Second,
	}
}

// ApplyPool sets the *sql.DB tuning knobs. Returns an error if cfg is
// internally inconsistent.
func ApplyPool(db *sql.DB, cfg PoolConfig) error {
	if db == nil {
		return errors.New("nil db")
	}
	if cfg.MaxOpen <= 0 {
		return fmt.Errorf("MaxOpen must be > 0 (got %d)", cfg.MaxOpen)
	}
	if cfg.MaxIdle < 0 || cfg.MaxIdle > cfg.MaxOpen {
		return fmt.Errorf("MaxIdle must be in [0, MaxOpen] (got %d / %d)",
			cfg.MaxIdle, cfg.MaxOpen)
	}
	db.SetMaxOpenConns(cfg.MaxOpen)
	db.SetMaxIdleConns(cfg.MaxIdle)
	db.SetConnMaxIdleTime(cfg.MaxIdleTime)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	return nil
}

// PingDeadline pings db with a fresh context honoring ConnTimeout.
// Used by /api/system/health (Story 21.4).
func PingDeadline(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return db.PingContext(c)
}
