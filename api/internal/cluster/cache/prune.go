package cache

import (
	"context"
	"sort"
	"time"
)

// The health_samples retention moved here from the workers' pruner
// (pipeline engine/prune.py, plan 06-11 §6.4), unchanged: raw samples are
// kept 48 hours, then folded into 10-minute rollups (host *rollup*: the
// mean of every host's samples of a metric in the bucket), and rollups are
// kept 30 days.
const (
	rawKeep    = 48 * time.Hour
	rollupKeep = 30 * 24 * time.Hour
	rollupStep = 10 * time.Minute
	pruneBatch = 5000
)

// FoldStats is the last health fold and prune.
type FoldStats struct {
	At      *time.Time `json:"at,omitempty"`
	Buckets int64      `json:"buckets"`
	Folded  int64      `json:"folded"`
	Pruned  int64      `json:"pruned"`
}

// prune runs every 10 minutes: the cache tables past the window, the read
// positions of day folders past it, the health fold and the old rollups.
func (c *Cache) prune(ctx context.Context) {
	c.pruneMu.Lock()
	defer c.pruneMu.Unlock()
	now := c.now()
	if c.window > 0 {
		cut := now.Add(-c.window)
		for _, t := range []struct{ table, col string }{
			{"cluster_metrics_cache", "at"}, {"cluster_log_cache", "ts"},
		} {
			if _, err := c.deleteBatches(ctx, `DELETE FROM `+t.table+` WHERE `+c.rowRef()+` IN (SELECT `+c.rowRef()+
				` FROM `+t.table+` WHERE `+t.col+` < $1 LIMIT 5000)`, c.ts(cut)); err != nil {
				c.fail("prune "+t.table, err)
			}
		}
		// a day folder older than the window's first day is never read again
		if _, err := c.exec(ctx, c.db, `DELETE FROM cluster_cache_files WHERE day < $1`,
			cut.AddDate(0, 0, -1).Format("2006-01-02")); err != nil {
			c.fail("prune cluster_cache_files", err)
		}
		c.mu.Lock()
		for w, s := range c.slots {
			if now.Sub(s.seen) > time.Hour {
				delete(c.slots, w)
			}
		}
		c.mu.Unlock()
	}
	buckets, folded, err := c.foldHealth(ctx, now)
	if err != nil {
		c.fail("fold health_samples", err)
	}
	pruned, err := c.deleteBatches(ctx, `DELETE FROM health_samples WHERE (metric, host, at) IN (SELECT metric, host, at
  FROM health_samples WHERE host = '`+RollupHost+`' AND at < $1 LIMIT 5000)`, c.healthTS(now.Add(-rollupKeep)))
	if err != nil {
		c.fail("prune health rollups", err)
	}
	at := now
	c.mu.Lock()
	c.fold = FoldStats{At: &at, Buckets: buckets, Folded: folded, Pruned: pruned}
	c.mu.Unlock()
	if c.window > 0 {
		c.refreshEdges(ctx)
	}
}

// deleteBatches runs a DELETE of at most pruneBatch rows until one
// deletes fewer (each its own transaction).
func (c *Cache) deleteBatches(ctx context.Context, query string, args ...any) (int64, error) {
	var total int64
	for {
		n, err := c.exec(ctx, c.db, query, args...)
		total += n
		if err != nil || n < pruneBatch {
			return total, err
		}
	}
}

// foldHealth is engine/prune.py's _rollup_health: raw samples older than
// 48 h, the edge floored to 10 minutes so every folded bucket is
// complete, become one rollup row per (metric, 10-minute bucket) — the
// mean of the bucket's values over every host, upserted (a later fold of
// the same bucket replaces it) — and the raw rows go. One transaction per
// bucket, the raw rows taken with DELETE … RETURNING: two folds at once,
// or a fold cut short by a restart, never fold a row twice or lose one.
// It returns the rollup rows written and the raw rows folded.
func (c *Cache) foldHealth(ctx context.Context, t time.Time) (buckets, folded int64, err error) {
	edge := floor10(t.UTC().Add(-rawKeep))
	for {
		lo, ok, err := c.oldestRaw(ctx, edge)
		if err != nil || !ok {
			return buckets, folded, err
		}
		for first := true; lo.Before(edge); first = false {
			nb, nf, err := c.foldBucket(ctx, lo)
			if err != nil {
				return buckets, folded, err
			}
			buckets, folded = buckets+nb, folded+nf
			if nf == 0 {
				if first {
					// the oldest raw row did not fold (another fold took it, or
					// its time is not in the canonical form): the next prune
					return buckets, folded, nil
				}
				break // a gap: look for the next raw row
			}
			lo = lo.Add(rollupStep)
		}
		if !lo.Before(edge) {
			return buckets, folded, nil
		}
	}
}

// oldestRaw is the bucket of the oldest raw sample before edge.
func (c *Cache) oldestRaw(ctx context.Context, edge time.Time) (time.Time, bool, error) {
	qctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var at dbTime
	err := c.db.QueryRowContext(qctx, `SELECT min(at) FROM health_samples WHERE host <> '`+RollupHost+`' AND at < $1`,
		c.healthTS(edge)).Scan(&at)
	if err != nil || !at.Valid {
		return time.Time{}, false, err
	}
	return floor10(at.T), true, nil
}

// foldBucket folds the raw samples of [lo, lo+10 min).
func (c *Cache) foldBucket(ctx context.Context, lo time.Time) (buckets, folded int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `DELETE FROM health_samples
 WHERE host <> '`+RollupHost+`' AND at >= $1 AND at < $2 RETURNING metric, value`,
		c.healthTS(lo), c.healthTS(lo.Add(rollupStep)))
	if err != nil {
		return 0, 0, err
	}
	type acc struct {
		sum float64
		n   int
	}
	by := map[string]*acc{}
	for rows.Next() {
		var m string
		var v float64
		if err := rows.Scan(&m, &v); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		a := by[m]
		if a == nil {
			a = &acc{}
			by[m] = a
		}
		a.sum += v
		a.n++
		folded++
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	metrics := make([]string, 0, len(by))
	for m := range by {
		metrics = append(metrics, m)
	}
	sort.Strings(metrics)
	out := make([]healthRow, 0, len(metrics))
	for _, m := range metrics {
		out = append(out, healthRow{at: lo, host: RollupHost, metric: m, value: by[m].sum / float64(by[m].n)})
	}
	if _, err := c.upsertHealth(ctx, tx, out); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return int64(len(out)), folded, nil
}

// floor10 is t down to its 10-minute bucket (UTC).
func floor10(t time.Time) time.Time {
	return t.UTC().Truncate(rollupStep)
}

// refreshEdges reads the oldest and newest row of each cache table.
func (c *Cache) refreshEdges(ctx context.Context) {
	for _, t := range []struct{ kind, table, col string }{
		{KindMetrics, "cluster_metrics_cache", "at"}, {KindLogs, "cluster_log_cache", "ts"},
	} {
		qctx, cancel := context.WithTimeout(ctx, c.timeout)
		var lo, hi dbTime
		err := c.db.QueryRowContext(qctx, `SELECT min(`+t.col+`), max(`+t.col+`) FROM `+t.table).Scan(&lo, &hi)
		cancel()
		if err != nil {
			c.fail("read the cache's oldest and newest rows", err)
			continue
		}
		c.mu.Lock()
		c.edges[t.kind] = [2]time.Time{lo.T, hi.T}
		c.mu.Unlock()
	}
}
