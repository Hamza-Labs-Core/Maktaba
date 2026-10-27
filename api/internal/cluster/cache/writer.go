package cache

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// Run writes until ctx ends: logs every 2 s, metrics frames and health
// every 10 s (a final health set at once), the prune and the health fold
// every 10 minutes, and a pass over the files every SweepEvery (when the
// window is on and a cluster directory is known). At the end it flushes
// what is queued, for a few seconds at most.
func (c *Cache) Run(ctx context.Context) {
	// before the channel's first rows land: read positions that outlived
	// their rows are dropped (a failure here is retried by the first pass)
	if err := c.clearStale(ctx); err != nil && ctx.Err() == nil {
		c.fail("check the cache tables", err)
	}
	var wg, sweeping sync.WaitGroup
	loop := func(wg *sync.WaitGroup, every time.Duration, now bool, f func(context.Context), wake <-chan struct{},
		onWake func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer safego.Recover(c.log, "cluster_cache_loop")
			if now {
				f(ctx)
			}
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					f(ctx)
				case <-wake:
					onWake(ctx)
				}
			}
		}()
	}
	loop(&wg, logsEvery, false, c.flushLogs, nil, nil)
	loop(&wg, metricsEvery, false, c.flushMetrics, c.wake, c.flushFinal)
	loop(&wg, pruneEvery, true, c.prune, nil, nil)
	if c.window > 0 {
		loop(&sweeping, c.sweep, false, func(ctx context.Context) {
			if dir := c.clusterDir(); dir != "" && c.passMu.TryLock() {
				defer c.passMu.Unlock()
				if err := c.pass(ctx, dir); err != nil && ctx.Err() == nil {
					c.fail("read the cluster files", err)
				}
			}
		}, nil, nil)
	}
	<-ctx.Done()
	wg.Wait()
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTime)
	defer cancel()
	c.flushLogs(fctx)
	c.flushMetrics(fctx)
	// a pass stuck in a hung share's read cannot be cancelled: wait for it
	// only as long as the flush could have taken
	done := make(chan struct{})
	go func() { sweeping.Wait(); close(done) }()
	select {
	case <-done:
	case <-fctx.Done():
	}
}

func (c *Cache) clusterDir() string {
	if c.dirFn != nil {
		if d := c.dirFn(); d != "" {
			return d
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dir
}

// flushLogs writes the queued log records. A batch that fails is
// dropped and counted, with what was queued behind it.
func (c *Cache) flushLogs(ctx context.Context) {
	c.logsMu.Lock()
	defer c.logsMu.Unlock()
	c.mu.Lock()
	items := c.logs
	c.logs, c.logsBusy = nil, len(items)
	c.mu.Unlock()
	if len(items) == 0 {
		return
	}
	now := c.now()
	low, high := now.Add(-c.window), now.Add(24*time.Hour)
	rows := make([]logRow, 0, len(items))
	invalid := 0
	for _, it := range items {
		ts, ok := parseTime(it.rec.TS)
		if ok && ts.Before(low) {
			continue // older than the window (a resend after a long outage)
		}
		r, ok2 := logRowOf(it.ip, it.worker, it.node, ts, it.rec.Level, it.rec.Event, it.rec.Logger, it.rec.Fields)
		if !ok || !ok2 || ts.After(high) {
			invalid++
			continue
		}
		rows = append(rows, r)
	}
	sortLogs(rows)
	added, done, err := c.insertLogs(ctx, c.db, rows)
	c.mu.Lock()
	k := c.counts[KindLogs]
	k.written += added
	k.dropped += int64(invalid + len(rows) - done)
	c.logsBusy = 0
	for _, r := range rows[:done] {
		c.seen(KindLogs, r.ts)
	}
	c.mu.Unlock()
	if err != nil {
		c.fail("write log records", err)
	}
}

// flushFinal writes at once the health sets of processes that sent their
// last frame; the others wait for the 10 s flush.
func (c *Cache) flushFinal(ctx context.Context) {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	now := c.now()
	c.mu.Lock()
	final := map[string]healthSet{}
	for k, s := range c.health {
		if s.final {
			final[k] = s
			delete(c.health, k)
		}
	}
	c.mu.Unlock()
	if len(final) > 0 {
		c.writeHealth(ctx, now, final)
	}
}

// flushMetrics writes each process's waiting health set and the queued
// metrics frames.
func (c *Cache) flushMetrics(ctx context.Context) {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	now := c.now()
	c.mu.Lock()
	sets := c.health
	c.health = map[string]healthSet{}
	items := c.frames
	c.frames, c.framesBusy = nil, len(items)
	c.mu.Unlock()

	if len(sets) > 0 {
		c.writeHealth(ctx, now, sets)
	}
	if len(items) == 0 {
		return
	}
	low := now.Add(-c.window)
	rows := make([]frameRow, 0, len(items))
	invalid := 0
	for _, it := range items {
		if it.at.Before(low) {
			continue
		}
		frame, ok := canonJSON(it.frame, false)
		if !ok || it.at.After(now.Add(24*time.Hour)) {
			invalid++
			continue
		}
		rows = append(rows, frameRow{worker: it.worker, slot: it.slot, at: it.at, ip: it.ip, node: it.node, frame: frame})
	}
	sortFrames(rows)
	added, done, err := c.insertFrames(ctx, c.db, rows)
	c.mu.Lock()
	k := c.counts[KindMetrics]
	k.written += added
	k.dropped += int64(invalid + len(rows) - done)
	c.framesBusy = 0
	for _, r := range rows[:done] {
		c.seen(KindMetrics, r.at)
	}
	c.mu.Unlock()
	if err != nil {
		c.fail("write metrics frames", err)
	}
}

// readsBusySQL is the pipeline's count of NAS reads in flight
// (engine/health.py sample_once): jobs of the nas_read pool that are
// claimed or running.
const readsBusySQL = `SELECT count(*) FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
 WHERE s.pool = 'nas_read' AND j.state IN ('claimed','running','resuming')`

// hostAt is one health_samples row set's (host, at): its key with the
// metric.
type hostAt struct {
	host string
	at   int64 // Unix milliseconds
}

type usedBy struct {
	key  string
	seen time.Time
}

// writeHealth writes the health sets as the workers wrote health_samples:
// one row per metric, host = the process's host, at = when the hub got the
// set (its clock, to the millisecond). Two processes on one host never
// share an at: the later one moves on by a millisecond, so neither
// overwrites the other (the readers sum some metrics over rows). Plus
// nas.reads_busy, counted once, for every host that reported.
func (c *Cache) writeHealth(ctx context.Context, now time.Time, sets map[string]healthSet) {
	list := make([]healthSet, 0, len(sets))
	for _, s := range sets {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].at.Equal(list[j].at) {
			return list[i].at.Before(list[j].at)
		}
		return list[i].key < list[j].key
	})
	for k, u := range c.usedAt {
		if now.Sub(u.seen) > 2*time.Minute {
			delete(c.usedAt, k)
		}
	}
	var rows []healthRow
	hosts := map[string]bool{}
	for _, s := range list {
		at := s.at.UTC().Truncate(time.Millisecond)
		for {
			u, taken := c.usedAt[hostAt{s.host, at.UnixMilli()}]
			if !taken || u.key == s.key {
				break
			}
			at = at.Add(time.Millisecond)
		}
		c.usedAt[hostAt{s.host, at.UnixMilli()}] = usedBy{key: s.key, seen: now}
		hosts[s.host] = true
		for m, v := range s.vals {
			rows = append(rows, healthRow{at: at, host: s.host, metric: m, value: v})
		}
	}
	var busy int64
	qctx, cancel := context.WithTimeout(ctx, c.timeout)
	err := c.db.QueryRowContext(qctx, readsBusySQL).Scan(&busy)
	cancel()
	if err != nil {
		c.fail("count the NAS reads in flight", err)
	} else {
		at := now.Truncate(time.Millisecond)
		for h := range hosts {
			rows = append(rows, healthRow{at: at, host: h, metric: "nas.reads_busy", value: float64(busy)})
		}
	}
	rows = dedupeHealth(rows)
	done, err := c.upsertHealth(ctx, c.db, rows)
	c.mu.Lock()
	k := c.counts[KindHealth]
	k.written += int64(done)
	k.dropped += int64(len(rows) - done)
	c.mu.Unlock()
	if err != nil {
		c.fail("write health_samples", err)
	}
}

// dedupeHealth keeps the last row per (metric, host, at), in key order:
// one statement may not upsert a key twice.
func dedupeHealth(rows []healthRow) []healthRow {
	type key struct {
		metric, host string
		at           int64
	}
	last := map[key]int{}
	for i, r := range rows {
		last[key{r.metric, r.host, r.at.UnixMicro()}] = i
	}
	out := make([]healthRow, 0, len(last))
	for i, r := range rows {
		if last[key{r.metric, r.host, r.at.UnixMicro()}] == i {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.metric != b.metric {
			return a.metric < b.metric
		}
		if a.host != b.host {
			return a.host < b.host
		}
		return a.at.Before(b.at)
	})
	return out
}

// validMetric: a health number health_samples can hold and the readers
// can render (no NaN or infinity).
func validMetric(name string, v float64) bool {
	return name != "" && len(name) <= 200 && strings.IndexByte(name, 0) < 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// sortLogs orders rows by their key, so concurrent writers (the channel's
// flush and a pass over the files) take row locks in one order.
func sortLogs(rows []logRow) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.worker != b.worker {
			return a.worker < b.worker
		}
		if !a.ts.Equal(b.ts) {
			return a.ts.Before(b.ts)
		}
		return a.h < b.h
	})
}

func sortFrames(rows []frameRow) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.worker != b.worker {
			return a.worker < b.worker
		}
		return a.slot.Before(b.slot)
	})
}

// seen moves a kind's newest-row edge (c.mu held).
func (c *Cache) seen(kind string, t time.Time) {
	e := c.edges[kind]
	if e[1].Before(t) {
		e[1] = t
	}
	if e[0].IsZero() || t.Before(e[0]) {
		e[0] = t
	}
	c.edges[kind] = e
}

// fail records an error and logs it at most once a minute (with how many
// were not logged since).
func (c *Cache) fail(what string, err error) {
	now := c.now()
	c.mu.Lock()
	c.lastErr, c.lastErrAt = what+": "+err.Error(), now
	logIt := c.loggedAt.IsZero() || now.Sub(c.loggedAt) >= errLogEvery
	muted := c.muted
	if logIt {
		c.loggedAt, c.muted = now, 0
	} else {
		c.muted++
	}
	c.mu.Unlock()
	if logIt {
		c.log.Warn("cluster cache: database or files unavailable; records dropped and counted",
			"what", what, "err", err, "not_logged", muted, "event", "cluster_cache_failed")
	}
}
