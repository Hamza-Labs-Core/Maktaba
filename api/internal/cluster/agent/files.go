package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The hub's own files (design-agent.md §4): under the home's IP folder,
// <kind>/<YYYY-MM-DD>/hub.jsonl. Every other file belongs to the process
// that writes it.
const (
	KindEvents  = "events"
	KindQueues  = "queues"
	KindLibrary = "library"

	hubFile = "hub.jsonl"
)

// DayKinds are the day-directory kinds a machine folder can hold.
var DayKinds = []string{"logs", "metrics", "llm", "jobs", "events", "queues", "library"}

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// a machine folder: an IP (IPv6 with '-') or a host name
	ipDirRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-]{0,62}$`)
)

// IPDir is the folder name of an address: IPv6 ':' (and a zone's '%')
// become '-'.
func IPDir(ip string) string {
	return strings.NewReplacer(":", "-", "%", "-").Replace(ip)
}

// ValidIPDir reports whether s is a safe machine folder name.
func ValidIPDir(s string) bool {
	return ipDirRe.MatchString(s) && !strings.Contains(s, "..")
}

// ValidDate reports whether s is a real YYYY-MM-DD date.
func ValidDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

type fileRecord struct {
	kind string
	at   time.Time
	line []byte
}

type openFile struct {
	date string
	path string
	f    *os.File
	w    *bufio.Writer
	size int64
}

// FileWriter is the hub's buffered, asynchronous writer: Write never
// blocks (a full buffer counts the record as dropped), one goroutine owns
// the files, flushes at least every second, rotates by UTC day, caps each
// file at MaxBytes (past it records are counted, not written) and logs
// disk errors once a minute.
type FileWriter struct {
	MaxBytes int64
	Now      func() time.Time
	Logger   *slog.Logger

	in chan fileRecord

	mu    sync.Mutex
	dir   string   // the cluster dir
	root  *os.Root // every write goes through it: nothing leaves the dir
	ipDir string   // the home's folder name
	files map[string]*openFile

	dropped atomic.Int64 // buffer full
	capped  atomic.Int64 // past MaxBytes
	errs    atomic.Int64
	lastErr atomic.Value // string
}

// NewFileWriter returns a writer with a buffer of n records.
func NewFileWriter(n int, maxBytes int64, now func() time.Time, logger *slog.Logger) *FileWriter {
	if n <= 0 {
		n = 4096
	}
	return &FileWriter{MaxBytes: maxBytes, Now: now, Logger: logger, in: make(chan fileRecord, n), files: map[string]*openFile{}}
}

// SetTarget points the writer at a cluster dir and the home's folder;
// open files are closed and reopened there on the next record.
func (fw *FileWriter) SetTarget(dir, ipDir string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.dir == dir && fw.ipDir == ipDir {
		return
	}
	fw.closeAllLocked()
	if fw.root != nil {
		_ = fw.root.Close()
		fw.root = nil
	}
	fw.dir, fw.ipDir = dir, ipDir
}

// Write queues one JSON line for kind; it never blocks.
func (fw *FileWriter) Write(kind string, v any) {
	line, err := json.Marshal(v)
	if err != nil {
		fw.fail(fmt.Errorf("encode %s record: %w", kind, err))
		return
	}
	select {
	case fw.in <- fileRecord{kind: kind, at: fw.Now().UTC(), line: line}:
	default:
		fw.dropped.Add(1)
	}
}

// Stats are the writer's counters.
type FileStats struct {
	Dropped int64 `json:"dropped"`
	Capped  int64 `json:"capped"`
	Errors  int64 `json:"errors"`
}

// Stats returns the counters.
func (fw *FileWriter) Stats() FileStats {
	return FileStats{Dropped: fw.dropped.Load(), Capped: fw.capped.Load(), Errors: fw.errs.Load()}
}

func (fw *FileWriter) fail(err error) {
	fw.errs.Add(1)
	fw.lastErr.Store(err.Error())
}

// Run owns the files until ctx ends (then flushes and closes them).
func (fw *FileWriter) Run(ctx context.Context) {
	flush := time.NewTicker(time.Second)
	report := time.NewTicker(time.Minute)
	defer flush.Stop()
	defer report.Stop()
	var reported FileStats
	for {
		select {
		case <-ctx.Done():
			// drain what is queued, then close
			for {
				select {
				case rec := <-fw.in:
					fw.write(rec)
				default:
					fw.mu.Lock()
					fw.closeAllLocked()
					if fw.root != nil {
						_ = fw.root.Close()
						fw.root = nil
					}
					fw.mu.Unlock()
					return
				}
			}
		case rec := <-fw.in:
			fw.write(rec)
		case <-flush.C:
			fw.Flush()
		case <-report.C:
			s := fw.Stats()
			if s != reported {
				last, _ := fw.lastErr.Load().(string)
				fw.log().Warn("cluster: hub file writes failing", "errors", s.Errors-reported.Errors,
					"dropped", s.Dropped-reported.Dropped, "capped", s.Capped-reported.Capped,
					"last_error", last, "event", "cluster_files_failing")
				reported = s
			}
		}
	}
}

func (fw *FileWriter) log() *slog.Logger {
	if fw.Logger != nil {
		return fw.Logger
	}
	return slog.Default()
}

// Flush writes buffered lines out.
func (fw *FileWriter) Flush() {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	for k, of := range fw.files {
		if err := of.w.Flush(); err != nil {
			fw.fail(err)
			_ = of.f.Close()
			delete(fw.files, k)
		}
	}
}

func (fw *FileWriter) closeAllLocked() {
	for k, of := range fw.files {
		if err := of.w.Flush(); err != nil {
			fw.fail(err)
		}
		if err := of.f.Close(); err != nil {
			fw.fail(err)
		}
		delete(fw.files, k)
	}
}

func (fw *FileWriter) write(rec fileRecord) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.dir == "" || fw.ipDir == "" {
		fw.dropped.Add(1)
		return
	}
	date := rec.at.Format("2006-01-02")
	of := fw.files[rec.kind]
	if of != nil && of.date != date {
		if err := of.w.Flush(); err != nil {
			fw.fail(err)
		}
		_ = of.f.Close()
		delete(fw.files, rec.kind)
		of = nil
	}
	if of == nil {
		var err error
		if of, err = fw.open(rec.kind, date); err != nil {
			fw.fail(err)
			return
		}
		fw.files[rec.kind] = of
	}
	n := int64(len(rec.line) + 1)
	if fw.MaxBytes > 0 && of.size+n > fw.MaxBytes {
		fw.capped.Add(1)
		return
	}
	if _, err := of.w.Write(rec.line); err == nil {
		err = of.w.WriteByte('\n')
		if err != nil {
			fw.fail(err)
		}
	} else {
		fw.fail(err)
	}
	of.size += n
}

func (fw *FileWriter) open(kind, date string) (*openFile, error) {
	if fw.root == nil {
		root, err := os.OpenRoot(fw.dir)
		if err != nil {
			return nil, err
		}
		fw.root = root
	}
	rel := filepath.Join(fw.ipDir, kind, date)
	// the share is writable by every machine: a planted link must not
	// steer the hub's writes anywhere
	if err := mkdirShared(fw.root, rel); err != nil {
		return nil, err
	}
	p := filepath.Join(rel, hubFile)
	f, err := fw.root.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, fileMode)
	if err == nil {
		// readable by every machine whatever the umask
		if cerr := f.Chmod(fileMode); cerr != nil {
			_ = f.Close()
			return nil, cerr
		}
	} else if errors.Is(err, fs.ErrExist) {
		st, lerr := fw.root.Lstat(p)
		if lerr != nil {
			return nil, lerr
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: %w", p, errNotPlain)
		}
		f, err = fw.root.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	}
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &openFile{date: date, path: p, f: f, w: bufio.NewWriterSize(f, 64<<10), size: st.Size()}, nil
}

var errNotPlain = errors.New("not a plain directory or file (a link?)")

// Modes on the share: directories the hub creates are group-writable
// (0775), so the home's workers running as another user of the group can
// add their own folders beside the hub's; files are 0644.
const (
	sharedDirMode os.FileMode = 0o775
	fileMode      os.FileMode = 0o644
)

// mkdirShared creates the missing directories of rel under root, each
// 0775 whatever the umask. It only changes the mode of directories it
// creates, and refuses a link (or anything not a directory) on the way.
func mkdirShared(root *os.Root, rel string) error {
	cur := ""
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		st, err := root.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			switch merr := root.Mkdir(cur, sharedDirMode); {
			case merr == nil:
				if err := root.Chmod(cur, sharedDirMode); err != nil {
					return err
				}
			case !errors.Is(merr, fs.ErrExist): // a racing creator is fine
				return merr
			}
			st, err = root.Lstat(cur)
		}
		if err != nil {
			return err
		}
		if !st.Mode().IsDir() {
			return fmt.Errorf("%s: %w", cur, errNotPlain)
		}
	}
	return nil
}

// mkdirSharedAbs is mkdirShared for an absolute path: rooted at its
// deepest existing ancestor.
func mkdirSharedAbs(dir string) error {
	dir = filepath.Clean(dir)
	base := dir
	for {
		if _, err := os.Stat(base); err == nil {
			break
		}
		parent := filepath.Dir(base)
		if parent == base {
			return fmt.Errorf("%s: no existing ancestor", dir)
		}
		base = parent
	}
	rel, err := filepath.Rel(base, dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return mkdirShared(root, rel)
}

// plainDirs checks that every component of rel under root is a real
// directory, not a link.
func plainDirs(root *os.Root, rel string) error {
	cur := ""
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		st, err := root.Lstat(cur)
		if err != nil {
			return err
		}
		if !st.Mode().IsDir() {
			return fmt.Errorf("%s: %w", cur, errNotPlain)
		}
	}
	return nil
}

// plainFile checks rel is a regular file inside plain directories.
func plainFile(root *os.Root, rel string) bool {
	if plainDirs(root, filepath.Dir(rel)) != nil {
		return false
	}
	st, err := root.Lstat(rel)
	return err == nil && st.Mode().IsRegular()
}

// readDirIn lists a plain directory under root.
func readDirIn(root *os.Root, rel string) ([]os.DirEntry, error) {
	if err := plainDirs(root, rel); err != nil {
		return nil, err
	}
	d, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	return d.ReadDir(-1)
}

// ---------- retention ----------

// Sweeper removes what is past retention (design-agent.md §7.7), once a
// day: expired day folders in every IP folder (a day folder is a UTC date,
// so an expired one is never being written), and processes/*.json whose
// last_seen is older than the retention (processes that will not come
// back: worker ids are new at every start). Each process also removes its
// own expired files.
type Sweeper struct {
	Days int
	Now  func() time.Time
}

// Sweep runs once over dir and returns the number of day folders and
// process files removed. Everything goes through an os.Root and refuses
// links: the share is writable by every machine, and the sweep must not
// delete anything outside it.
func (s *Sweeper) Sweep(dir string) (int, error) {
	if s.Days <= 0 || dir == "" {
		return 0, nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = root.Close() }()
	now := s.Now().UTC()
	cutoff := now.AddDate(0, 0, -s.Days).Format("2006-01-02")
	horizon := now.AddDate(0, 0, -s.Days)
	entries, err := readDirIn(root, ".")
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !ValidIPDir(e.Name()) || plainDirs(root, e.Name()) != nil {
			continue
		}
		for _, kind := range DayKinds {
			removed += removeOldDays(root, filepath.Join(e.Name(), kind), cutoff)
		}
		removed += removeOldProcesses(root, filepath.Join(e.Name(), "processes"), horizon)
	}
	return removed, nil
}

// removeOldProcesses removes processes/*.json last seen before horizon
// (last_seen, else stopped_at, else the file's time).
func removeOldProcesses(root *os.Root, rel string, horizon time.Time) int {
	entries, err := readDirIn(root, rel)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		p := filepath.Join(rel, e.Name())
		if !strings.HasSuffix(e.Name(), ".json") || !plainFile(root, p) {
			continue
		}
		st, err := root.Lstat(p)
		if err != nil {
			continue
		}
		seen := st.ModTime()
		if b, err := root.ReadFile(p); err == nil && len(b) <= 256<<10 {
			var f struct {
				LastSeen  string `json:"last_seen"`
				StoppedAt string `json:"stopped_at"`
			}
			if json.Unmarshal(b, &f) == nil {
				for _, v := range []string{f.LastSeen, f.StoppedAt} {
					if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
						seen = t
						break
					}
				}
			}
		}
		if seen.Before(horizon) && root.Remove(p) == nil {
			n++
		}
	}
	return n
}

func removeOldDays(root *os.Root, kindRel, cutoff string) int {
	days, err := readDirIn(root, kindRel)
	if err != nil {
		return 0
	}
	n := 0
	for _, d := range days {
		day := filepath.Join(kindRel, d.Name())
		// a link named like a day is not followed: only real directories go,
		// and RemoveAll inside the root never leaves it
		if ValidDate(d.Name()) && d.Name() < cutoff && plainDirs(root, day) == nil {
			if root.RemoveAll(day) == nil {
				n++
			}
		}
	}
	return n
}
