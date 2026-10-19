package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

// staleAfter: without a complete pass over the files for this long (the
// database or the share failing), the cache claims nothing.
func (c *Cache) staleAfter() time.Duration { return 3 * c.sweep }

// Covers reports whether the cache holds kind ("metrics" | "logs") for
// the whole of [from, to): the range starts inside the window, and a pass
// over the files read every file of the window recently. What the files
// held when that pass started is in the cache; what came since (at most a
// few minutes) is there as far as the channel delivered it, and the next
// pass completes it.
func (c *Cache) Covers(kind string, from, to time.Time) bool {
	if c.window <= 0 || (kind != KindMetrics && kind != KindLogs) {
		return false
	}
	now := c.now()
	c.mu.Lock()
	_, ok := c.passAt[kind]
	end := c.passEnd[kind]
	c.mu.Unlock()
	// stale counts from the pass's end: a pass longer than staleAfter (a
	// first read of a busy window, a slow share) still covers when it ends
	if !ok || now.Sub(end) > c.staleAfter() {
		return false
	}
	if !to.After(from) {
		return true // an empty range
	}
	return !from.Before(now.Add(-c.window))
}

// ReadLogs answers a logs history read: the newest q.Limit records (all
// when 0) of q.IP in [q.From, q.To), of q.WorkerID ("" for all), at or
// above q.Level, whose line contains q.Q (any case), newest first. Each
// Line is the record as the files hold it: ts, level, event, logger,
// fields. It reads in pages (newest first, by key), so a page of the
// answer never pulls the whole day through the connection.
func (c *Cache) ReadLogs(ctx context.Context, q agent.CacheQuery) ([]agent.CacheRecord, error) {
	if c.window <= 0 {
		return []agent.CacheRecord{}, nil
	}
	// a slow or hung database: an error, and the hub reads the files
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	where, args := c.where("ts", q)
	if rk := levelRank(q.Level); rk > 0 {
		args = append(args, rk)
		where += ` AND rank >= $` + strconv.Itoa(len(args))
	}
	needle := bytes.ToLower([]byte(q.Q))
	page := logsPage
	if q.Limit > 0 && len(needle) == 0 {
		page = min(page, q.Limit)
	}
	out := []agent.CacheRecord{}
	var after *logKey
	for {
		pageArgs, cond := args, where
		if after != nil {
			n := len(pageArgs)
			pageArgs = append(append([]any{}, pageArgs...), c.ts(after.ts), after.worker, after.h)
			cond += ` AND (ts, worker_id, h) < ($` + strconv.Itoa(n+1) + `, $` + strconv.Itoa(n+2) + `, $` + strconv.Itoa(n+3) + `)`
		}
		got, last, err := c.readLogsPage(ctx, cond, pageArgs, page, needle, q.Limit, &out)
		if err != nil {
			return nil, err
		}
		if got < page || (q.Limit > 0 && len(out) >= q.Limit) {
			return out, nil
		}
		after = last
	}
}

// logsPage is how many log rows one statement of ReadLogs reads.
const logsPage = 1000

// logKey is a cluster_log_cache row's key, the cursor of ReadLogs' pages.
type logKey struct {
	ts     time.Time
	worker string
	h      int64
}

// readLogsPage reads one page into out (until limit) and returns how many
// rows it read and the last row's key.
func (c *Cache) readLogsPage(ctx context.Context, where string, args []any, page int, needle []byte, limit int,
	out *[]agent.CacheRecord) (int, *logKey, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT worker_id, ts, h, level, event, logger, fields
  FROM cluster_log_cache WHERE `+where+` ORDER BY ts DESC, worker_id DESC, h DESC LIMIT `+strconv.Itoa(page), args...)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	var last logKey
	for rows.Next() {
		var worker, level, event, logger string
		var ts dbTime
		var h int64
		var fields []byte
		if err := rows.Scan(&worker, &ts, &h, &level, &event, &logger, &fields); err != nil {
			return 0, nil, err
		}
		n++
		last = logKey{ts: ts.T, worker: worker, h: h}
		if limit > 0 && len(*out) >= limit {
			continue // the page's rows are read to its end (LIMIT bounds them)
		}
		line, err := logLine(ts.T, level, event, logger, fields)
		if err != nil {
			return 0, nil, err
		}
		if len(needle) > 0 && !bytes.Contains(bytes.ToLower(line), needle) {
			continue
		}
		*out = append(*out, agent.CacheRecord{WorkerID: worker, TS: ts.T, Line: line})
	}
	return n, &last, rows.Err()
}

// ReadMetrics answers a metrics history read: q.IP's frames in
// [q.From, q.To) (of q.WorkerID, "" for all), oldest first, one per
// process per 10 s; past q.Limit (when > 0) evenly thinned to at most
// that many. Each Line is the frame as the metrics file holds it.
func (c *Cache) ReadMetrics(ctx context.Context, q agent.CacheQuery) ([]agent.CacheRecord, error) {
	if c.window <= 0 {
		return []agent.CacheRecord{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	where, args := c.where("at", q)
	query := `SELECT worker_id, at, frame FROM cluster_metrics_cache WHERE ` + where + ` ORDER BY at, worker_id`
	if q.Limit > 0 {
		var n int
		if err := c.db.QueryRowContext(ctx, `SELECT count(*) FROM cluster_metrics_cache WHERE `+where, args...).Scan(&n); err != nil {
			return nil, err
		}
		if stride := (n + q.Limit - 1) / q.Limit; stride > 1 {
			args = append(args, stride)
			query = `SELECT worker_id, at, frame FROM (SELECT worker_id, at, frame,
       row_number() OVER (ORDER BY at, worker_id) AS rn
  FROM cluster_metrics_cache WHERE ` + where + `) s
 WHERE (rn - 1) % $` + strconv.Itoa(len(args)) + ` = 0 ORDER BY at, worker_id`
		}
	}
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []agent.CacheRecord{}
	for rows.Next() {
		var worker string
		var at dbTime
		var frame []byte
		if err := rows.Scan(&worker, &at, &frame); err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := json.Compact(&b, frame); err != nil {
			return nil, err
		}
		out = append(out, agent.CacheRecord{WorkerID: worker, TS: at.T, Line: b.Bytes()})
	}
	return out, rows.Err()
}

// where is the IP, time range and worker filter of a read.
func (c *Cache) where(col string, q agent.CacheQuery) (string, []any) {
	args := []any{agent.IPDir(q.IP)}
	where := `ip = $1`
	if !q.From.IsZero() {
		args = append(args, c.ts(q.From))
		where += ` AND ` + col + ` >= $` + strconv.Itoa(len(args))
	}
	if !q.To.IsZero() {
		args = append(args, c.ts(q.To))
		where += ` AND ` + col + ` < $` + strconv.Itoa(len(args))
	}
	if q.WorkerID != "" {
		args = append(args, FileName(q.WorkerID))
		where += ` AND worker_id = $` + strconv.Itoa(len(args))
	}
	return where, args
}

// logLine is a log record as the files hold it.
func logLine(ts time.Time, level, event, logger string, fields []byte) (json.RawMessage, error) {
	var f bytes.Buffer
	if err := json.Compact(&f, fields); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	err := enc.Encode(struct {
		TS     string          `json:"ts"`
		Level  string          `json:"level"`
		Event  string          `json:"event"`
		Logger string          `json:"logger"`
		Fields json.RawMessage `json:"fields"`
	}{ts.UTC().Format(time.RFC3339Nano), level, event, logger, f.Bytes()})
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// ---------- stats ----------

// KindStats is one kind's counters.
type KindStats struct {
	// Queued: records waiting for the database (health: processes with a
	// waiting set).
	Queued int `json:"queued"`
	// Written: rows written from the channel (health: rows upserted).
	Written int64 `json:"written"`
	// Dropped: records not written (queue full, database failing).
	Dropped int64 `json:"dropped"`
	// FromFiles: rows the passes over the files added (what the channel
	// had not delivered).
	FromFiles int64      `json:"from_files,omitempty"`
	Oldest    *time.Time `json:"oldest,omitempty"`
	Newest    *time.Time `json:"newest,omitempty"`
	// WarmFrom is where the covered range starts (nil: not covered).
	WarmFrom *time.Time `json:"warm_from,omitempty"`
	// PassAt is the start of the last complete pass over the files.
	PassAt *time.Time `json:"pass_at,omitempty"`
}

// Stats is the cache's state, shown as `cache` in the hub's processes
// snapshot.
type Stats struct {
	WindowHours float64    `json:"window_hours"`
	Metrics     KindStats  `json:"metrics"`
	Logs        KindStats  `json:"logs"`
	Health      KindStats  `json:"health"`
	Rollups     FoldStats  `json:"rollups"`
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
}

// Stats implements agent.Cache (the value is a Stats).
func (c *Cache) Stats() any { return c.Snapshot() }

// Snapshot is the cache's state now.
func (c *Cache) Snapshot() Stats {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	tp := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	kind := func(k string, queued int) KindStats {
		n := c.counts[k]
		s := KindStats{Queued: queued, Written: n.written, Dropped: n.dropped, FromFiles: n.fromFiles}
		if e, ok := c.edges[k]; ok {
			s.Oldest, s.Newest = tp(e[0]), tp(e[1])
		}
		if at, ok := c.passAt[k]; ok {
			s.PassAt = tp(at)
			if now.Sub(c.passEnd[k]) <= c.staleAfter() {
				s.WarmFrom = tp(now.Add(-c.window))
			}
		}
		return s
	}
	s := Stats{
		WindowHours: c.window.Hours(),
		Metrics:     kind(KindMetrics, len(c.frames)+c.framesBusy),
		Logs:        kind(KindLogs, len(c.logs)+c.logsBusy),
		Health:      kind(KindHealth, len(c.health)),
		Rollups:     c.fold,
		LastError:   c.lastErr,
		LastErrorAt: tp(c.lastErrAt),
	}
	return s
}
