package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The SQL is shared by both dialects: $n placeholders (modernc's SQLite
// binds $n by ordinal too), ON CONFLICT, DELETE ... RETURNING. Times are
// time.Time on Postgres and fixed-width ISO-8601 UTC text on SQLite, so
// they compare as times there too.

const (
	// tsLayout: the cache tables on SQLite.
	tsLayout = "2006-01-02T15:04:05.000000Z"
	// healthLayout: health_samples on SQLite, as the pipeline's to_ts
	// writes it.
	healthLayout = "2006-01-02T15:04:05.000Z"
)

// ts is a time argument for the cache tables.
func (c *Cache) ts(t time.Time) any {
	if c.sqlite {
		return t.UTC().Format(tsLayout)
	}
	return t.UTC()
}

// healthTS is a time argument for health_samples.
func (c *Cache) healthTS(t time.Time) any {
	if c.sqlite {
		return t.UTC().Format(healthLayout)
	}
	return t.UTC()
}

// batchDelete deletes at most 5000 rows of table whose col is before $1:
// by ctid on Postgres (a TID scan), by rowid on SQLite.
func (c *Cache) batchDelete(table, col string) string {
	if c.sqlite {
		return `DELETE FROM ` + table + ` WHERE rowid IN (SELECT rowid FROM ` + table + ` WHERE ` + col + ` < $1 LIMIT 5000)`
	}
	return `DELETE FROM ` + table + ` WHERE ctid = ANY (ARRAY(SELECT ctid FROM ` + table + ` WHERE ` + col + ` < $1 LIMIT 5000))`
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// dbTime scans a timestamp of either dialect (NULL leaves Valid false).
type dbTime struct {
	T     time.Time
	Valid bool
}

func (d *dbTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		d.Valid = false
		return nil
	case time.Time:
		d.T, d.Valid = x.UTC(), true
		return nil
	case string:
		return d.parse(x)
	case []byte:
		return d.parse(string(x))
	}
	return fmt.Errorf("cache: cannot scan %T as a time", v)
}

func (d *dbTime) parse(s string) error {
	t, ok := parseTime(s)
	if !ok {
		if t2, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", s); err == nil {
			t, ok = t2.UTC(), true
		}
	}
	if !ok {
		return fmt.Errorf("cache: cannot parse time %q", s)
	}
	d.T, d.Valid = t, true
	return nil
}

// values is "($1,…,$k),($k+1,…)" for n rows of k columns.
func values(n, k int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		for j := 0; j < k; j++ {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(i*k + j + 1))
		}
		b.WriteByte(')')
	}
	return b.String()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---------- rows ----------

// logRow is one cluster_log_cache row.
type logRow struct {
	worker string
	ts     time.Time
	h      int64
	ip     string
	node   string
	level  string
	rank   int
	event  string
	logger string
	fields []byte
}

// logRowOf builds the row of a log record, from the channel or a file:
// the same record gives the same key either way.
func logRowOf(ip, worker, node string, ts time.Time, level, event, logger string, fields json.RawMessage) (logRow, bool) {
	canon, ok := canonJSON(fields, true)
	if !ok {
		return logRow{}, false
	}
	level = clean(strings.ToLower(strings.TrimSpace(level)))
	event, logger = clean(event), clean(logger)
	rank := levelRank(level)
	if rank == 0 {
		rank = levelRank("info")
	}
	return logRow{worker: worker, ts: ts.UTC(), h: recordHash(level, event, logger, canon), ip: ip, node: node,
		level: level, rank: rank, event: event, logger: logger, fields: canon}, true
}

// recordHash keys a log record within its worker and time: the first 8
// bytes of SHA-256 over its level, event, logger and canonical fields.
func recordHash(level, event, logger string, fields []byte) int64 {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(level), []byte(event), []byte(logger), fields} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		h.Write(n[:])
		h.Write(part)
	}
	return int64(binary.BigEndian.Uint64(h.Sum(nil)[:8])) //nolint:gosec // a key, wrapping is fine
}

// canonJSON re-encodes a JSON value with sorted keys and its numbers as
// written, and NUL characters replaced (Postgres text and jsonb refuse
// them). Empty or null is {} when emptyObject.
func canonJSON(raw json.RawMessage, emptyObject bool) ([]byte, bool) {
	t := bytes.TrimSpace(raw)
	if emptyObject && (len(t) == 0 || string(t) == "null") {
		return []byte("{}"), true
	}
	dec := json.NewDecoder(bytes.NewReader(t))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil, false
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(scrub(v)) != nil {
		return nil, false
	}
	return bytes.TrimRight(b.Bytes(), "\n"), true
}

// scrub replaces NUL in every string and key of a decoded value.
func scrub(v any) any {
	switch x := v.(type) {
	case string:
		return clean(x)
	case []any:
		for i := range x {
			x[i] = scrub(x[i])
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[clean(k)] = scrub(e)
		}
		return out
	}
	return v
}

func clean(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "�")
}

// frameRow is one cluster_metrics_cache row.
type frameRow struct {
	worker string
	slot   time.Time
	at     time.Time
	ip     string
	node   string
	frame  []byte
}

// healthRow is one health_samples row.
type healthRow struct {
	at     time.Time
	host   string
	metric string
	value  float64
}

// ---------- batched writes ----------

// insertLogs writes rows in batches; it returns how many rows were new
// and how many it got through before an error.
func (c *Cache) insertLogs(ctx context.Context, ex execer, rows []logRow) (added int64, done int, err error) {
	const cols = 10
	for done < len(rows) {
		part := rows[done:min(done+batchRows, len(rows))]
		args := make([]any, 0, len(part)*cols)
		for _, r := range part {
			args = append(args, r.worker, c.ts(r.ts), r.h, r.ip, nullStr(r.node), r.level, r.rank, r.event, r.logger,
				string(r.fields))
		}
		n, err := c.exec(ctx, ex, `INSERT INTO cluster_log_cache
    (worker_id, ts, h, ip, node_id, level, rank, event, logger, fields)
VALUES `+values(len(part), cols)+` ON CONFLICT DO NOTHING`, args...)
		if err != nil {
			return added, done, err
		}
		added += n
		done += len(part)
	}
	return added, done, nil
}

// insertFrames writes rows in batches, as insertLogs.
func (c *Cache) insertFrames(ctx context.Context, ex execer, rows []frameRow) (added int64, done int, err error) {
	const cols = 6
	for done < len(rows) {
		part := rows[done:min(done+batchRows, len(rows))]
		args := make([]any, 0, len(part)*cols)
		for _, r := range part {
			args = append(args, r.worker, c.ts(r.slot), c.ts(r.at), r.ip, nullStr(r.node), string(r.frame))
		}
		n, err := c.exec(ctx, ex, `INSERT INTO cluster_metrics_cache (worker_id, slot, at, ip, node_id, frame)
VALUES `+values(len(part), cols)+` ON CONFLICT DO NOTHING`, args...)
		if err != nil {
			return added, done, err
		}
		added += n
		done += len(part)
	}
	return added, done, nil
}

// upsertHealth writes health_samples rows as the workers did: the
// newest value wins on (metric, host, at).
func (c *Cache) upsertHealth(ctx context.Context, ex execer, rows []healthRow) (done int, err error) {
	const cols = 4
	for done < len(rows) {
		part := rows[done:min(done+batchRows, len(rows))]
		args := make([]any, 0, len(part)*cols)
		for _, r := range part {
			args = append(args, c.healthTS(r.at), r.host, r.metric, r.value)
		}
		if _, err := c.exec(ctx, ex, `INSERT INTO health_samples (at, host, metric, value)
VALUES `+values(len(part), cols)+` ON CONFLICT (metric, host, at) DO UPDATE SET value = EXCLUDED.value`,
			args...); err != nil {
			return done, err
		}
		done += len(part)
	}
	return done, nil
}

// exec runs one statement with the write timeout and returns the rows
// it changed.
func (c *Cache) exec(ctx context.Context, ex execer, query string, args ...any) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	res, err := ex.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}
