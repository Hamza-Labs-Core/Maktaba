package cache

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

// Reading the cluster files (design-agent.md §7.4–§7.6):
//
//	<dir>/<ip>/{metrics,logs}/<YYYY-MM-DD>/<worker>.jsonl
//
// A pass reads, for every file of the window's day folders, the whole
// lines it gained since the last pass (cluster_cache_files.read_to; a new
// file from its start), in chunks of at most 500 lines, each chunk's rows
// and its new read position committed together. Only records of the window
// are kept, and of metrics the first frame of each 10 s slot. A last line
// without its newline is a write in progress unless it parses; it is read
// again next time. Records older than the window are read past, not kept.
//
// The share is writable by every machine, so nothing in it is trusted:
// the directory is opened as an os.Root (no path leaves it), every name
// must match its strict pattern, and a symbolic link (or anything but a
// plain directory or file) is skipped, never followed.

const (
	maxLine    = 4 << 20 // a longer line is skipped
	chunkBytes = 4 << 20
)

var (
	workerFileRe = regexp.MustCompile(`^[A-Za-z0-9-][A-Za-z0-9._-]{0,127}\.jsonl$`)
	kinds        = []string{KindMetrics, KindLogs}
)

// errNotPlain: a link, or anything but a plain directory or file. It is
// skipped, as the hub's file reader skips it, so it leaves the cache as
// whole as the files.
var errNotPlain = errors.New("not a plain directory or file")

// dbError marks a database failure in a pass (a file error only makes the
// pass incomplete).
type dbError struct{ err error }

func (e dbError) Error() string { return e.err.Error() }
func (e dbError) Unwrap() error { return e.err }

// Backfill reads the cluster files of the cache window in dir: each file
// from where the last pass stopped, a new one from its start — the whole
// window when the tables are empty, else what the files gained since. The
// hub calls it when it starts; Run repeats it every SweepEvery (over
// Options.ClusterDir, else the directory given here). Passes are
// serialised and idempotent. A missing dir is not an error (nothing is
// covered until it exists).
func (c *Cache) Backfill(ctx context.Context, dir string) error {
	if dir != "" {
		c.mu.Lock()
		c.dir = dir
		c.mu.Unlock()
	}
	if c.window <= 0 || dir == "" {
		return nil
	}
	c.passMu.Lock()
	defer c.passMu.Unlock()
	return c.pass(ctx, dir)
}

type fileKey struct{ ip, day, worker string }

type readPos struct {
	root   string
	readTo int64
}

// pass reads dir once. The kinds it read completely are covered from the
// pass's start.
func (c *Cache) pass(ctx context.Context, dir string) error {
	start := c.now()
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := c.clearStale(ctx); err != nil {
		return err
	}
	ips, err := plainEntries(root, ".", true)
	if err != nil {
		return err
	}
	lowDay := start.Add(-c.window).Format("2006-01-02")
	highDay := start.Add(24 * time.Hour).Format("2006-01-02")
	for _, kind := range kinds {
		pos, err := c.readPositions(ctx, kind)
		if err != nil {
			return dbError{err}
		}
		whole := true
		for _, ip := range ips {
			if !agent.ValidIPDir(ip) {
				continue
			}
			days, err := plainEntries(root, filepath.Join(ip, kind), true)
			if err != nil {
				whole = whole && skippable(err)
				continue
			}
			for _, day := range days {
				if !agent.ValidDate(day) || day < lowDay || day > highDay {
					continue
				}
				names, err := plainEntries(root, filepath.Join(ip, kind, day), false)
				if err != nil {
					whole = whole && skippable(err)
					continue
				}
				for _, name := range names {
					if !workerFileRe.MatchString(name) {
						continue
					}
					k := fileKey{ip: ip, day: day, worker: strings.TrimSuffix(name, ".jsonl")}
					err := c.readFile(ctx, root, dir, kind, k, pos[k], start)
					var dbe dbError
					switch {
					case errors.As(err, &dbe), ctx.Err() != nil:
						return err
					case err != nil:
						whole = whole && skippable(err)
					}
				}
			}
		}
		if whole {
			c.mu.Lock()
			c.passAt[kind] = start
			c.mu.Unlock()
		}
	}
	return nil
}

// clearStale forgets the read positions of a kind whose table is empty
// (once, before the first pass and, from Run, before the first write):
// the rows they stand for are gone, so the files are read again from
// their start. A crash empties both (UNLOGGED); this covers a table
// emptied by hand.
func (c *Cache) clearStale(ctx context.Context) error {
	c.staleMu.Lock()
	defer c.staleMu.Unlock()
	c.mu.Lock()
	done := c.checked
	c.mu.Unlock()
	if done || c.window <= 0 {
		return nil
	}
	for _, t := range []struct{ kind, table string }{
		{KindMetrics, "cluster_metrics_cache"}, {KindLogs, "cluster_log_cache"},
	} {
		var n int
		qctx, cancel := context.WithTimeout(ctx, c.timeout)
		err := c.db.QueryRowContext(qctx, `SELECT count(*) FROM (SELECT 1 FROM `+t.table+` LIMIT 1) s`).Scan(&n)
		cancel()
		if err != nil {
			return dbError{err}
		}
		if n == 0 {
			if _, err := c.exec(ctx, c.db, `DELETE FROM cluster_cache_files WHERE kind = $1`, t.kind); err != nil {
				return dbError{err}
			}
		}
	}
	c.mu.Lock()
	c.checked = true
	c.mu.Unlock()
	return nil
}

func (c *Cache) readPositions(ctx context.Context, kind string) (map[fileKey]readPos, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	rows, err := c.db.QueryContext(ctx, `SELECT ip, day, worker, root, read_to FROM cluster_cache_files WHERE kind = $1`, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[fileKey]readPos{}
	for rows.Next() {
		var k fileKey
		var p readPos
		if err := rows.Scan(&k.ip, &k.day, &k.worker, &p.root, &p.readTo); err != nil {
			return nil, err
		}
		out[k] = p
	}
	return out, rows.Err()
}

// skippable: an error that leaves the cache as whole as the files (the
// entry is gone, or is a link the hub's reader skips too).
func skippable(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNotPlain)
}

// plainDirs checks every component of rel is a plain directory (os.Root
// would follow a link that stays inside the root; the cache follows none).
func plainDirs(root *os.Root, rel string) (os.FileInfo, error) {
	var st os.FileInfo
	cur := ""
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		var err error
		if st, err = root.Lstat(cur); err != nil {
			return nil, err
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("%s: %w", cur, errNotPlain)
		}
	}
	if st == nil {
		return root.Lstat(".")
	}
	return st, nil
}

// plainEntries lists the plain directories (dirs) or plain files of rel,
// sorted; links and other entries are skipped.
func plainEntries(root *os.Root, rel string, dirs bool) ([]string, error) {
	st, err := plainDirs(root, rel)
	if err != nil {
		return nil, err
	}
	d, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	if fi, err := d.Stat(); err != nil || !os.SameFile(fi, st) {
		return nil, fmt.Errorf("%s: changed while opened: %w", rel, errNotPlain)
	}
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		t := e.Type()
		if (dirs && t.IsDir()) || (!dirs && t.IsRegular()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// openPlain opens a plain file under root: no link, and the file opened
// is the file checked.
func openPlain(root *os.Root, rel string) (*os.File, os.FileInfo, error) {
	if _, err := plainDirs(root, filepath.Dir(rel)); err != nil {
		return nil, nil, err
	}
	st, err := root.Lstat(rel)
	if err != nil {
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s: %w", rel, errNotPlain)
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil || !os.SameFile(fi, st) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s: changed while opened: %w", rel, errNotPlain)
	}
	return f, fi, nil
}

// readFile reads what one file gained since pos.
func (c *Cache) readFile(ctx context.Context, root *os.Root, dir, kind string, k fileKey, pos readPos, start time.Time) error {
	rel := filepath.Join(k.ip, kind, k.day, k.worker+".jsonl")
	f, fi, err := openPlain(root, rel)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	size := fi.Size()
	from := int64(0)
	if pos.root == dir && pos.readTo <= size {
		from = pos.readTo // a shorter file was replaced: from its start
	}
	if from == size {
		return nil
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(io.LimitReader(f, size-from), 64<<10)
	low := start.Add(-c.window)
	ch := chunk{kind: kind, key: k, root: dir, at: from, committed: from}
	var lastSlot time.Time
	for {
		line, n, whole, rerr := readLine(br)
		if n == 0 {
			break
		}
		if !whole && !json.Valid(line) {
			break // a write in progress: read again next time
		}
		ch.at += int64(n)
		ch.lines++
		ch.bytes += n
		c.parseLine(&ch, kind, k, line, low, &lastSlot)
		if ch.lines >= batchRows || ch.bytes >= chunkBytes {
			if err := c.commit(ctx, &ch); err != nil {
				return err
			}
		}
		if rerr != nil {
			break
		}
	}
	if ch.at > ch.committed || ch.lines > 0 {
		return c.commit(ctx, &ch)
	}
	return nil
}

// chunk is a file's lines read but not yet committed.
type chunk struct {
	kind      string
	key       fileKey
	root      string
	at        int64 // the read position after the lines read
	committed int64
	lines     int
	bytes     int
	logs      []logRow
	frames    []frameRow
}

// parseLine adds a line's row to the chunk when it is a record of the
// window (of metrics, the first of its 10 s slot).
func (c *Cache) parseLine(ch *chunk, kind string, k fileKey, line []byte, low time.Time, lastSlot *time.Time) {
	if len(line) == 0 {
		return
	}
	switch kind {
	case KindLogs:
		var r struct {
			TS     json.RawMessage `json:"ts"`
			Level  string          `json:"level"`
			Event  string          `json:"event"`
			Logger string          `json:"logger"`
			Fields json.RawMessage `json:"fields"`
		}
		if json.Unmarshal(line, &r) != nil {
			return
		}
		ts, ok := parseTS(r.TS)
		if !ok || ts.Before(low) {
			return
		}
		if row, ok := logRowOf(k.ip, k.worker, "", ts, r.Level, r.Event, r.Logger, r.Fields); ok {
			ch.logs = append(ch.logs, row)
		}
	case KindMetrics:
		at, ok := frameTS(line)
		if !ok || at.Before(low) {
			return
		}
		slot := slotOf(at)
		if slot.Equal(*lastSlot) {
			return
		}
		*lastSlot = slot
		if frame, ok := canonJSON(line, false); ok {
			ch.frames = append(ch.frames, frameRow{worker: k.worker, slot: slot, at: at, ip: k.ip, frame: frame})
		}
	}
}

// commit writes a chunk's rows and its read position in one transaction,
// after waiting for the read rate.
func (c *Cache) commit(ctx context.Context, ch *chunk) error {
	for n := ch.lines; n > 0; {
		k := min(n, c.limiter.Burst())
		if err := c.limiter.WaitN(ctx, k); err != nil {
			return err
		}
		n -= k
	}
	ctx, cancel := context.WithTimeout(ctx, 3*c.timeout) // the transaction's; each statement has its own
	defer cancel()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return dbError{err}
	}
	defer func() { _ = tx.Rollback() }()
	var added int64
	var newest time.Time
	switch ch.kind {
	case KindLogs:
		sortLogs(ch.logs)
		n, _, err := c.insertLogs(ctx, tx, ch.logs)
		if err != nil {
			return dbError{err}
		}
		added = n
		for _, r := range ch.logs {
			newest = maxTime(newest, r.ts)
		}
	case KindMetrics:
		sortFrames(ch.frames)
		n, _, err := c.insertFrames(ctx, tx, ch.frames)
		if err != nil {
			return dbError{err}
		}
		added = n
		for _, r := range ch.frames {
			newest = maxTime(newest, r.at)
		}
	}
	if _, err := c.exec(ctx, tx, `INSERT INTO cluster_cache_files (kind, ip, day, worker, root, read_to, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (kind, ip, day, worker) DO UPDATE SET root = EXCLUDED.root, read_to = EXCLUDED.read_to,
    updated_at = EXCLUDED.updated_at`,
		ch.kind, ch.key.ip, ch.key.day, ch.key.worker, ch.root, ch.at, c.ts(c.now())); err != nil {
		return dbError{err}
	}
	if err := tx.Commit(); err != nil {
		return dbError{err}
	}
	c.mu.Lock()
	c.counts[ch.kind].fromFiles += added
	if added > 0 {
		c.seen(ch.kind, newest)
	}
	c.mu.Unlock()
	ch.committed, ch.lines, ch.bytes, ch.logs, ch.frames = ch.at, 0, 0, nil, nil
	return nil
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// readLine reads one line: its bytes without the newline (nil when longer
// than maxLine), how many bytes it took, whether it ended with a newline,
// and the reader's error (io.EOF at the end).
func readLine(r *bufio.Reader) (line []byte, n int, whole bool, err error) {
	var buf []byte
	over := false
	for {
		part, e := r.ReadSlice('\n')
		n += len(part)
		if !over {
			if len(buf)+len(part) > maxLine+1 {
				over, buf = true, nil
			} else {
				buf = append(buf, part...)
			}
		}
		if errors.Is(e, bufio.ErrBufferFull) {
			continue
		}
		whole = e == nil
		if over {
			buf = nil
		}
		return bytes.TrimSpace(buf), n, whole, e
	}
}
