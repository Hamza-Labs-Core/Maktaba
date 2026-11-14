// Package cache tells each node's caches about changes made on another node (Epic 50 story
// 50.17 §4). Each node keeps one LISTEN connection (Bus); a writer sends a notification in the
// same transaction as its change, and every node drops the entry. When the LISTEN connection
// drops, notifications may have been missed, so every cache is emptied on the reconnect. The
// caches' own lifetimes (60 s) stay as the backstop.
package cache

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Channel is the notification channel for writers with no table trigger: payload "<kind>:<id>".
const Channel = "cloud_cache"

// Kinds of entries. The triggers of cloud migration 00500003 notify on their own channels
// (stores.ChannelTierChanged, stores.ChannelServerChanged), which the Bus reads as these kinds.
const (
	KindTier   = "tier"   // id: a home's slug whose plan changed
	KindServer = "server" // id: a home's slug that changed, went or whose owner changed status
)

// Notify sends kind:id on Channel through ex (a transaction: Postgres delivers it when the
// change commits, and not at all if it rolls back).
func Notify(ctx context.Context, ex interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, kind, id string) error {
	_, err := ex.ExecContext(ctx, `SELECT pg_notify($1, $2)`, Channel, kind+":"+id)
	return err
}

// Invalidator is a cache the Bus drives.
type Invalidator interface {
	// Invalidate drops id's entry (kind is one of the Kind constants).
	Invalidate(kind, id string)
	// Reset empties the cache: notifications may have been missed.
	Reset()
}

// Funcs adapts two functions to an Invalidator.
type Funcs struct {
	OnInvalidate func(kind, id string)
	OnReset      func()
}

func (f Funcs) Invalidate(kind, id string) {
	if f.OnInvalidate != nil {
		f.OnInvalidate(kind, id)
	}
}

func (f Funcs) Reset() {
	if f.OnReset != nil {
		f.OnReset()
	}
}

// Bus is a node's one LISTEN connection, fanning each notification out to every cache.
type Bus struct {
	Logger *slog.Logger

	mu     sync.RWMutex
	caches []Invalidator

	invalidations atomic.Int64
	resets        atomic.Int64
}

// Add registers a cache. Add before Start, or at any time: a cache added late hears from then on.
func (b *Bus) Add(c Invalidator) {
	b.mu.Lock()
	b.caches = append(b.caches, c)
	b.mu.Unlock()
}

// Start listens on Channel and the trigger channels through dsn, a direct connection (never
// PgBouncer's transaction pool, which can't hold a LISTEN), until ctx ends. It returns once
// the first LISTEN is in place; reconnecting is the listener's own.
func (b *Bus) Start(ctx context.Context, dsn string) error {
	return stores.Listen(ctx, dsn, []string{Channel, stores.ChannelTierChanged, stores.ChannelServerChanged},
		b.Logger, b.Dispatch, b.ResetAll)
}

// Dispatch hands one notification to every cache. A payload on Channel without a kind is
// ignored.
func (b *Bus) Dispatch(channel, payload string) {
	kind, id := "", payload
	switch channel {
	case stores.ChannelTierChanged:
		kind = KindTier
	case stores.ChannelServerChanged:
		kind = KindServer
	case Channel:
		var ok bool
		if kind, id, ok = strings.Cut(payload, ":"); !ok || kind == "" {
			return
		}
	default:
		return
	}
	b.invalidations.Add(1)
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, c := range b.caches {
		c.Invalidate(kind, id)
	}
}

// ResetAll empties every cache (the LISTEN connection came back: notifications may be lost).
func (b *Bus) ResetAll() {
	b.resets.Add(1)
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, c := range b.caches {
		c.Reset()
	}
	if b.Logger != nil {
		b.Logger.Info("cache: the change listener came back; every cache emptied", "event", "cache_reset")
	}
}

// Counts are the notifications handed out and the resets since start (cache_invalidations_total
// and cache_resets_total on /metrics).
func (b *Bus) Counts() (invalidations, resets int64) {
	return b.invalidations.Load(), b.resets.Load()
}

// WriteMetrics writes the Bus's counters in Prometheus text format.
func (b *Bus) WriteMetrics(w io.Writer) error {
	inv, res := b.Counts()
	_, err := fmt.Fprintf(w, "# TYPE cache_invalidations_total counter\ncache_invalidations_total %d\n"+
		"# TYPE cache_resets_total counter\ncache_resets_total %d\n", inv, res)
	return err
}
