package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	dir   string // the cluster dir
	ipDir string // the home's folder name
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
	dir := filepath.Join(fw.dir, fw.ipDir, kind, date)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // shared, readable by every machine
		return nil, err
	}
	p := filepath.Join(dir, hubFile)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // readable by every machine
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

// ---------- retention ----------

// Sweeper removes expired day directories (design-agent.md §4): the hub's
// own files under the home's folder, and everything past retention in the
// folders of machines whose processes are all gone (no live session and
// nothing written within the retention window). Live processes clean their
// own files.
type Sweeper struct {
	Days int
	Now  func() time.Time
	// LiveIPs returns the folder names with a connected process.
	LiveIPs func() map[string]bool
}

// Sweep runs once over dir; homeIPDir is the hub's own folder. It returns
// the number of day directories (or hub files) removed.
func (s *Sweeper) Sweep(dir, homeIPDir string) (int, error) {
	if s.Days <= 0 || dir == "" {
		return 0, nil
	}
	now := s.Now().UTC()
	cutoff := now.AddDate(0, 0, -s.Days).Format("2006-01-02")
	horizon := now.AddDate(0, 0, -s.Days)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	live := map[string]bool{}
	if s.LiveIPs != nil {
		live = s.LiveIPs()
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !ValidIPDir(e.Name()) {
			continue
		}
		ipPath := filepath.Join(dir, e.Name())
		if e.Name() == homeIPDir {
			removed += removeOldHubFiles(ipPath, cutoff)
		}
		if live[e.Name()] || newestWrite(ipPath).After(horizon) {
			continue
		}
		for _, kind := range DayKinds {
			removed += removeOldDays(filepath.Join(ipPath, kind), cutoff)
		}
	}
	return removed, nil
}

func removeOldHubFiles(ipPath, cutoff string) int {
	n := 0
	for _, kind := range []string{KindEvents, KindQueues, KindLibrary} {
		days, err := os.ReadDir(filepath.Join(ipPath, kind))
		if err != nil {
			continue
		}
		for _, d := range days {
			if !d.IsDir() || !ValidDate(d.Name()) || d.Name() >= cutoff {
				continue
			}
			dayDir := filepath.Join(ipPath, kind, d.Name())
			if os.Remove(filepath.Join(dayDir, hubFile)) == nil {
				n++
			}
			_ = os.Remove(dayDir) // only when empty
		}
	}
	return n
}

func removeOldDays(kindPath, cutoff string) int {
	days, err := os.ReadDir(kindPath)
	if err != nil {
		return 0
	}
	n := 0
	for _, d := range days {
		if d.IsDir() && ValidDate(d.Name()) && d.Name() < cutoff {
			if os.RemoveAll(filepath.Join(kindPath, d.Name())) == nil {
				n++
			}
		}
	}
	return n
}

// newestWrite is the latest modification time of any file under p.
func newestWrite(p string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}
