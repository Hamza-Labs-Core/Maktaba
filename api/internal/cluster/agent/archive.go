package agent

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The archive on the home (design-agent.md §7.10): once a UTC day has
// closed, the hub copies every machine's logs, jobs and llm files and its
// own events, gzip-compressed, to its own disk, where they outlive the
// share's two weeks:
//
//	<archive>/<ip>/<kind>/<YYYY-MM-DD>/<worker>.jsonl.gz
//	<archive>/<ip>/<kind>/<YYYY-MM-DD>/.manifest.json   source file → size archived
//
// Only the hub writes it, through an os.Root (directories 0750, files
// 0640, never through a link); it reads the share with the link-refusing
// readers. It never blocks the hub: it runs in its own goroutine, bounded
// per pass, and a failure is logged at most once an hour.

// Defaults of §7.10 / §9.
const (
	DefaultArchiveDays  = 365
	DefaultArchiveMaxGB = 20

	archiveEvery     = time.Hour
	archiveRetention = 24 * time.Hour
	archivePassLimit = 30 * time.Minute
	archiveLogEvery  = time.Hour

	archiveDirMode  os.FileMode = 0o750
	archiveFileMode os.FileMode = 0o640

	manifestName = ".manifest.json"
	gzSuffix     = ".jsonl.gz"
	tmpPrefix    = ".tmp-"
)

// DefaultArchiveKinds are the kinds archived unless MAKTABA_ARCHIVE_KINDS
// says otherwise.
var DefaultArchiveKinds = []string{"logs", "jobs", "llm", "events"}

// ArchiveConfig is the archive's settings.
type ArchiveConfig struct {
	Dir      string   // MAKTABA_ARCHIVE_DIR, default ~/.maktaba/archive
	Kinds    []string // MAKTABA_ARCHIVE_KINDS
	Days     int      // MAKTABA_ARCHIVE_DAYS (0: no age limit)
	MaxBytes int64    // MAKTABA_ARCHIVE_MAX_GB in bytes (0: no size limit)
}

// DefaultArchiveDir is ~/.maktaba/archive.
func DefaultArchiveDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".maktaba", "archive")
	}
	return filepath.Join(home, ".maktaba", "archive")
}

// ArchiveStats is the processes snapshot's `archive`.
type ArchiveStats struct {
	Dir     string     `json:"dir"`
	LastRun *time.Time `json:"last_run"`
	// what the last pass archived
	DaysArchived  int   `json:"days_archived"`
	FilesArchived int   `json:"files_archived"`
	BytesArchived int64 `json:"bytes_archived"` // source bytes
	// what the archive holds
	DaysHeld  int     `json:"days_held"`
	Files     int     `json:"files"`
	SizeBytes int64   `json:"size_bytes"`
	OldestDay *string `json:"oldest_day"`
	LastError *string `json:"last_error"`
}

// Archiver keeps the archive.
type Archiver struct {
	cfg    ArchiveConfig
	now    func() time.Time
	logger *slog.Logger
	// share is the cluster directory now (the hub's location).
	share func() string

	passMu        sync.Mutex // one pass at a time
	mu            sync.Mutex
	stats         ArchiveStats
	lastRetention time.Time
	lastLogged    time.Time
}

// NewArchiver returns an archiver for cfg reading the share at share().
func NewArchiver(cfg ArchiveConfig, share func() string, now func() time.Time, logger *slog.Logger) *Archiver {
	if len(cfg.Kinds) == 0 {
		cfg.Kinds = DefaultArchiveKinds
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Archiver{cfg: cfg, now: now, logger: logger, share: share, stats: ArchiveStats{Dir: cfg.Dir}}
}

// Stats is a copy of the archive's numbers.
func (a *Archiver) Stats() ArchiveStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stats
}

// Run archives at start and every hour until ctx ends.
func (a *Archiver) Run(ctx context.Context) {
	a.Pass(ctx)
	t := time.NewTicker(archiveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.Pass(ctx)
		}
	}
}

// Pass is one run: archive the closed days, then (once a day) apply the
// retention, then count what the archive holds.
func (a *Archiver) Pass(ctx context.Context) {
	a.passMu.Lock()
	defer a.passMu.Unlock()
	now := a.now().UTC()
	run := ArchiveStats{}
	err := a.archiveAll(ctx, now, &run)
	if a.retentionDue(now) {
		if rerr := a.retain(now); rerr != nil {
			err = errors.Join(err, rerr)
		} else {
			a.mu.Lock()
			a.lastRetention = now
			a.mu.Unlock()
		}
	}
	held, herr := a.held()
	if herr != nil {
		err = errors.Join(err, herr)
	}
	a.mu.Lock()
	a.stats.LastRun = &now
	a.stats.DaysArchived, a.stats.FilesArchived, a.stats.BytesArchived = run.DaysArchived, run.FilesArchived, run.BytesArchived
	a.stats.DaysHeld, a.stats.Files, a.stats.SizeBytes, a.stats.OldestDay = held.DaysHeld, held.Files, held.SizeBytes, held.OldestDay
	a.stats.LastError = nil
	logIt := false
	if err != nil && ctx.Err() == nil {
		msg := err.Error()
		a.stats.LastError = &msg
		if now.Sub(a.lastLogged) >= archiveLogEvery {
			a.lastLogged, logIt = now, true
		}
	}
	a.mu.Unlock()
	if logIt {
		a.logger.Warn("cluster: archive pass failed", "dir", a.cfg.Dir, "err", err, "event", "cluster_archive_failed")
	}
}

func (a *Archiver) retentionDue(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastRetention.IsZero() || now.Sub(a.lastRetention) >= archiveRetention
}

// openArchive makes (0750, only what it creates) and opens the archive.
func (a *Archiver) openArchive() (*os.Root, error) {
	if a.cfg.Dir == "" {
		return nil, errors.New("no archive directory")
	}
	if err := mkdirModeAbs(a.cfg.Dir, archiveDirMode); err != nil {
		return nil, fmt.Errorf("archive directory: %w", err)
	}
	return os.OpenRoot(a.cfg.Dir)
}

// archiveAll archives every closed day of every archived kind the share
// holds; it stops when ctx ends or the pass has run long enough (the next
// pass resumes).
func (a *Archiver) archiveAll(ctx context.Context, now time.Time, run *ArchiveStats) error {
	dir := ""
	if a.share != nil {
		dir = a.share()
	}
	share, err := openCluster(dir)
	if err != nil {
		return fmt.Errorf("share: %w", err)
	}
	if share == nil {
		return nil
	}
	defer func() { _ = share.Close() }()
	arch, err := a.openArchive()
	if err != nil {
		return err
	}
	defer func() { _ = arch.Close() }()
	today := now.Format("2006-01-02")
	deadline := now.Add(archivePassLimit)
	ips, err := readDirIn(share, ".")
	if err != nil {
		return fmt.Errorf("share: %w", err)
	}
	var errs []error
	for _, ip := range ips {
		if !ip.IsDir() || !ValidIPDir(ip.Name()) || plainDirs(share, ip.Name()) != nil {
			continue
		}
		for _, kind := range a.cfg.Kinds {
			for _, day := range listDates(share, filepath.Join(ip.Name(), kind)) {
				if day >= today {
					continue
				}
				if ctx.Err() != nil || a.now().After(deadline) {
					return errors.Join(errs...)
				}
				files, bytes, err := archiveDay(share, arch, ip.Name(), kind, day)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s/%s/%s: %w", ip.Name(), kind, day, err))
				}
				if files > 0 {
					run.DaysArchived++
					run.FilesArchived += files
					run.BytesArchived += bytes
				}
			}
		}
	}
	return errors.Join(errs...)
}

// manifest is a day folder's .manifest.json: what was archived, by source
// file name, with the size it had.
type manifest struct {
	Files     map[string]int64 `json:"files"`
	UpdatedAt time.Time        `json:"updated_at"`
}

func readManifest(arch *os.Root, day string) manifest {
	m := manifest{Files: map[string]int64{}}
	p := filepath.Join(day, manifestName)
	if !plainFile(arch, p) {
		return m
	}
	b, err := readCapped(arch, p, 4<<20)
	if err != nil {
		return m
	}
	// a torn or foreign manifest is no manifest: the day is archived again
	var got manifest
	if json.Unmarshal(b, &got) == nil && got.Files != nil {
		m.Files = got.Files
	}
	return m
}

// archiveDay compresses the day's new or grown files into the archive and
// rewrites its manifest; it returns the files archived and their bytes.
func archiveDay(share, arch *os.Root, ip, kind, day string) (int, int64, error) {
	rel := filepath.Join(ip, kind, day)
	entries, err := readDirIn(share, rel)
	if err != nil {
		return 0, 0, err
	}
	type todo struct {
		name string
		size int64
	}
	var work []todo
	var m manifest
	loaded := false
	for _, e := range entries {
		name := e.Name()
		src := filepath.Join(rel, name)
		if !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, ".") || !plainFile(share, src) {
			continue
		}
		st, err := share.Lstat(src)
		if err != nil {
			continue
		}
		if !loaded {
			m, loaded = readManifest(arch, rel), true
		}
		gz := filepath.Join(rel, strings.TrimSuffix(name, ".jsonl")+gzSuffix)
		if done, ok := m.Files[name]; ok && st.Size() <= done && plainFile(arch, gz) {
			continue
		}
		work = append(work, todo{name, st.Size()})
	}
	if len(work) == 0 {
		return 0, 0, nil
	}
	if err := mkdirMode(arch, rel, archiveDirMode); err != nil {
		return 0, 0, err
	}
	clearTemps(arch, rel)
	n := 0
	var total int64
	var errs []error
	for _, w := range work {
		src := filepath.Join(rel, w.name)
		dst := filepath.Join(rel, strings.TrimSuffix(w.name, ".jsonl")+gzSuffix)
		if err := compressInto(share, src, w.size, arch, dst); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", w.name, err))
			continue
		}
		m.Files[w.name] = w.size
		n++
		total += w.size
	}
	if n > 0 {
		m.UpdatedAt = time.Now().UTC()
		b, err := json.Marshal(m)
		if err == nil {
			err = writeAtomic(arch, filepath.Join(rel, manifestName), func(w io.Writer) error {
				_, werr := w.Write(b)
				return werr
			})
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("manifest: %w", err))
		}
	}
	return n, total, errors.Join(errs...)
}

// compressInto gzips the first size bytes of src (a regular file of the
// share) to dst in the archive, through a temporary file.
func compressInto(share *os.Root, src string, size int64, arch *os.Root, dst string) error {
	if !plainFile(share, src) {
		return errNotPlain
	}
	in, err := share.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return writeAtomic(arch, dst, func(w io.Writer) error {
		zw := gzip.NewWriter(w)
		if _, err := io.Copy(zw, io.LimitReader(in, size)); err != nil {
			return err
		}
		return zw.Close()
	})
}

// writeAtomic writes rel through a temporary file in the same folder,
// mode 0640, then renames it over rel (never through a link: rel must be
// absent or a regular file).
func writeAtomic(arch *os.Root, rel string, fill func(io.Writer) error) error {
	if st, err := arch.Lstat(rel); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", rel, errNotPlain)
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	tmp := filepath.Join(filepath.Dir(rel), tmpPrefix+hex.EncodeToString(suffix))
	f, err := arch.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, archiveFileMode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = arch.Remove(tmp)
		}
	}()
	if err := f.Chmod(archiveFileMode); err != nil {
		_ = f.Close()
		return err
	}
	if err := fill(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := arch.Rename(tmp, rel); err != nil {
		return err
	}
	ok = true
	return nil
}

// clearTemps removes temporary files a crashed pass left in a day folder.
func clearTemps(arch *os.Root, rel string) {
	entries, err := readDirIn(arch, rel)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpPrefix) && plainFile(arch, filepath.Join(rel, e.Name())) {
			_ = arch.Remove(filepath.Join(rel, e.Name()))
		}
	}
}

// archivedDay is one day folder of the archive.
type archivedDay struct {
	rel  string // <ip>/<kind>/<day>
	day  string
	size int64
	n    int
}

// days lists the archive's day folders with their sizes.
func archiveDays(arch *os.Root) ([]archivedDay, error) {
	ips, err := readDirIn(arch, ".")
	if err != nil {
		return nil, err
	}
	var out []archivedDay
	for _, ip := range ips {
		if !ip.IsDir() || !ValidIPDir(ip.Name()) || plainDirs(arch, ip.Name()) != nil {
			continue
		}
		kinds, err := readDirIn(arch, ip.Name())
		if err != nil {
			continue
		}
		for _, k := range kinds {
			kindRel := filepath.Join(ip.Name(), k.Name())
			if !k.IsDir() || plainDirs(arch, kindRel) != nil {
				continue
			}
			for _, day := range listDates(arch, kindRel) {
				d := archivedDay{rel: filepath.Join(kindRel, day), day: day}
				files, _ := readDirIn(arch, d.rel)
				for _, f := range files {
					if st, err := arch.Lstat(filepath.Join(d.rel, f.Name())); err == nil && st.Mode().IsRegular() {
						d.size += st.Size()
						if strings.HasSuffix(f.Name(), gzSuffix) {
							d.n++
						}
					}
				}
				out = append(out, d)
			}
		}
	}
	return out, nil
}

// retain removes whole days past the age limit, then the oldest days
// until the archive is under its size cap.
func (a *Archiver) retain(now time.Time) error {
	if a.cfg.Days <= 0 && a.cfg.MaxBytes <= 0 {
		return nil
	}
	arch, err := a.openArchive()
	if err != nil {
		return err
	}
	defer func() { _ = arch.Close() }()
	days, err := archiveDays(arch)
	if err != nil {
		return err
	}
	var errs []error
	remove := func(d archivedDay) {
		if err := arch.RemoveAll(d.rel); err != nil {
			errs = append(errs, err)
		}
	}
	var kept []archivedDay
	cutoff := ""
	if a.cfg.Days > 0 {
		cutoff = now.AddDate(0, 0, -a.cfg.Days).Format("2006-01-02")
	}
	var total int64
	for _, d := range days {
		if cutoff != "" && d.day < cutoff {
			remove(d)
			continue
		}
		kept = append(kept, d)
		total += d.size
	}
	if a.cfg.MaxBytes > 0 && total > a.cfg.MaxBytes {
		// whole days, oldest first: every folder of a date goes together
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].day < kept[j].day })
		for i := 0; i < len(kept) && total > a.cfg.MaxBytes; {
			day := kept[i].day
			for ; i < len(kept) && kept[i].day == day; i++ {
				remove(kept[i])
				total -= kept[i].size
			}
		}
	}
	return errors.Join(errs...)
}

// held counts what the archive holds.
func (a *Archiver) held() (ArchiveStats, error) {
	var s ArchiveStats
	if a.cfg.Dir == "" {
		return s, nil
	}
	if _, err := os.Stat(a.cfg.Dir); errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	arch, err := os.OpenRoot(a.cfg.Dir)
	if err != nil {
		return s, err
	}
	defer func() { _ = arch.Close() }()
	days, err := archiveDays(arch)
	if err != nil {
		return s, err
	}
	dates := map[string]bool{}
	for _, d := range days {
		dates[d.day] = true
		s.Files += d.n
		s.SizeBytes += d.size
		if s.OldestDay == nil || d.day < *s.OldestDay {
			day := d.day
			s.OldestDay = &day
		}
	}
	s.DaysHeld = len(dates)
	return s, nil
}

// openArchiveRead opens the archive for reading; nil when there is none.
func openArchiveRead(dir string) *os.Root {
	if dir == "" {
		return nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil
	}
	return root
}

// archivedDayFiles lists <ip>/<kind>/<date>/*.jsonl.gz in the archive.
func archivedDayFiles(arch *os.Root, ip, kind, date string) []dayFile {
	if arch == nil {
		return nil
	}
	day := filepath.Join(ip, kind, date)
	entries, err := readDirIn(arch, day)
	if err != nil {
		return nil
	}
	var out []dayFile
	for _, e := range entries {
		name := e.Name()
		p := filepath.Join(day, name)
		if !strings.HasSuffix(name, gzSuffix) || strings.HasPrefix(name, ".") || !plainFile(arch, p) {
			continue
		}
		out = append(out, dayFile{root: arch, path: p, workerID: strings.TrimSuffix(name, gzSuffix), gz: true})
	}
	return out
}
