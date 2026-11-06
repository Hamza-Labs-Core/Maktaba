// Package logindex is the log index (Epic 53 story 53.09): the `logs` role
// (`maktaba-server logs`, catalog component logs.index) tails the log keeper's segments
// (internal/logkeeper) into a SQLite file of its own, <logs>/index/logs.db (FTS5, WAL), and
// answers search, live tail and "what else happened then" on loopback; the API proxies them
// to admins. The index keeps ids and where each line is (its segment and offset), not the
// line: the segment has it, so the file stays small. It is a separate file, not tables in
// the library's Postgres, so log search works with Postgres down (README D2), and a separate
// process, so a corrupt index never takes down the supervisor (D3).
package logindex

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS lines (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  level INTEGER NOT NULL,
  service TEXT NOT NULL,
  component TEXT,
  node TEXT NOT NULL,
  msg TEXT NOT NULL,
  request_id TEXT, job_id TEXT, session_id TEXT, video_id TEXT, user_h TEXT,
  error_fp TEXT,
  seg TEXT NOT NULL,
  off INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS lines_ts ON lines(ts);
CREATE INDEX IF NOT EXISTS lines_svc_lvl_ts ON lines(service, level, ts);
CREATE INDEX IF NOT EXISTS lines_comp_lvl_ts ON lines(component, level, ts) WHERE component IS NOT NULL;
CREATE INDEX IF NOT EXISTS lines_req ON lines(request_id) WHERE request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS lines_job ON lines(job_id) WHERE job_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS lines_seg ON lines(seg);
CREATE VIRTUAL TABLE IF NOT EXISTS lines_fts USING fts5(msg, text, err, content='', contentless_delete=1,
  tokenize='unicode61 remove_diacritics 2');
CREATE TABLE IF NOT EXISTS positions (seg TEXT PRIMARY KEY, off INTEGER NOT NULL, size INTEGER NOT NULL DEFAULT 0);
`

// Levels as the index stores them.
var levelNum = map[string]int{"debug": 10, "info": 20, "warn": 30, "error": 40, "fatal": 50}

// LevelNum is a level's number (info for an unknown one).
func LevelNum(l string) int {
	if n, ok := levelNum[strings.ToLower(l)]; ok {
		return n
	}
	return 20
}

// LevelName is the name of a stored level.
func LevelName(n int) string {
	for k, v := range levelNum {
		if v == n {
			return k
		}
	}
	return "info"
}

// Index is one machine's log index.
type Index struct {
	db   *sql.DB
	root string // the keeper's root: segments/ and index/
	now  func() time.Time

	mu      sync.Mutex
	waiters []chan struct{} // tails waiting for new lines
	// Retention keeps rows as long as the segments are kept (7 days); MaxBytes is
	// logs.db's share of the diagnostics cap (350 MB).
	Retention time.Duration
	MaxBytes  int64
	// Log gets the index's own events (log_index_rebuilt, ...).
	Log func(msg string, kv ...any)
	// rebuilt: why the file was made again at Open ("" when it was not)
	rebuilt string
}

// Rebuilt is why Open made the index again (a corrupt file moved aside), "" when it did not.
func (ix *Index) Rebuilt() string { return ix.rebuilt }

// Path is the index file under a keeper's root.
func Path(root string) string { return filepath.Join(root, "index", "logs.db") }

// Open opens (or makes) the index of the segments under root. A file that cannot be
// opened or read as an index is moved aside and made again from the segments still on
// disk (log_index_rebuilt).
func Open(root string, now func() time.Time) (*Index, error) {
	if now == nil {
		now = time.Now
	}
	ix := &Index{root: root, now: now, Retention: 7 * 24 * time.Hour, MaxBytes: 350 << 20,
		Log: func(string, ...any) {}}
	p := Path(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	db, err := openDB(p)
	if err != nil {
		aside := fmt.Sprintf("%s.corrupt-%s", p, now().UTC().Format("20060102-150405"))
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Rename(p+suffix, aside+suffix)
		}
		db, err2 := openDB(p)
		if err2 != nil {
			return nil, err2
		}
		ix.db = db
		// the positions went with the file: the segments still on disk are indexed again
		ix.rebuilt = err.Error()
		return ix, nil
	}
	ix.db = db
	return ix, nil
}

func openDB(p string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=auto_vacuum(INCREMENTAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	// a file that opens but is not whole (a torn write, a disk error) fails here
	var ok string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&ok); err != nil || ok != "ok" {
		_ = db.Close()
		if err == nil {
			err = errors.New("quick_check: " + ok)
		}
		return nil, err
	}
	return db, nil
}

// Close closes the index.
func (ix *Index) Close() error { return ix.db.Close() }

// notify wakes the live tails (new lines were indexed).
func (ix *Index) notify() {
	ix.mu.Lock()
	ws := ix.waiters
	ix.waiters = nil
	ix.mu.Unlock()
	for _, w := range ws {
		close(w)
	}
}

// wait is a channel closed when lines are next indexed.
func (ix *Index) wait() <-chan struct{} {
	ch := make(chan struct{})
	ix.mu.Lock()
	ix.waiters = append(ix.waiters, ch)
	ix.mu.Unlock()
	return ch
}

// Size is logs.db's size on disk (with its WAL).
func (ix *Index) Size() int64 {
	var n int64
	for _, s := range []string{"", "-wal"} {
		if fi, err := os.Stat(Path(ix.root) + s); err == nil {
			n += fi.Size()
		}
	}
	return n
}
