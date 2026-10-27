package pgbundle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// How a dump is written (every dump of the library database: the nightly ones, the ones
// before an update, a restore's set-aside): a directory-format dump (pg_dump -Fd), one
// file per table, written by several pg_dump workers in one snapshot and read back by as
// many pg_restore workers, each file compressed fast (zstd at level 1, else lz4, else gzip
// at level 1: what the pg_dump at hand was built with). The folder is still named *.dump,
// so its kind and name read as before; a dump of before (one custom-format file) is read
// back as it always was (pg_restore tells the two apart).

// Regenerable are the tables whose rows the dumps taken before an update leave out (their
// definitions and indexes stay): rows Maktaba makes again by itself once such a dump is put
// back. segment_embeddings holds semantic search's vectors (Epic 43): on the home that
// measured it, 31 of the database's 47 GB, and as COPY text the bulk of a 35 GB dump that one
// worker wrote for ten minutes. A dump put back without them leaves the table empty; the
// pipeline's reconciler queues an index job for every video whose last one succeeded and
// that has no vector there (engine/reconcile.py, _semantic_sweep, 1000 a minute), and the
// index stage embeds its transcript again. Full-text search works meanwhile; semantic
// search fills back in as the jobs run.
var Regenerable = []string{"public.segment_embeddings"}

// DumpOptions shape a dump.
type DumpOptions struct {
	// Jobs is pg_dump's parallelism (0: DumpJobs).
	Jobs int
	// SkipData are tables whose rows are left out (Regenerable).
	SkipData []string
	// Check, when set, is run on the finished dump before it takes its name (it reads
	// back); its error fails the dump.
	Check func(part string) error
}

// DumpJobs is how many pg_dump workers a dump runs: half the machine's cores, 2 to 8 (the
// largest table bounds a dump past that, and the server goes on serving meanwhile).
func DumpJobs() int { return min(max(runtime.NumCPU()/2, 2), 8) }

// compressions are pg_dump's --compress values, the fastest first: a pg_dump built without
// zstd or lz4 (the bundled one has zlib only) refuses those, and one older than 16 knows a
// plain level only.
var compressions = []string{"zstd:1", "lz4", "gzip:1", "1"}

// compressionOf remembers, per pg_dump, the first of compressions it took.
var compressionOf sync.Map

// PgDump runs bin (a pg_dump) with conn (its connection arguments, the database included)
// and env, writing a directory-format dump to path: into path.partial, which is moved into
// place once whole (a dump of that name there before is replaced), so a dump cut short is
// never taken for one.
func PgDump(ctx context.Context, bin string, conn, env []string, path string, opts DumpOptions) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = DumpJobs()
	}
	part := path + ".partial"
	first := 0
	if v, ok := compressionOf.Load(bin); ok {
		first = v.(int)
	}
	for i, locked := first, 0; ; {
		_ = os.RemoveAll(part) // pg_dump -Fd makes the folder: none may be there
		args := append(append([]string{}, conn...), "--format=directory", "--jobs="+strconv.Itoa(jobs),
			"--compress="+compressions[i], "--file="+part)
		for _, t := range opts.SkipData {
			args = append(args, "--exclude-table-data="+t)
		}
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err == nil {
			compressionOf.Store(bin, i)
			break
		}
		_ = os.RemoveAll(part)
		switch {
		case ctx.Err() != nil:
		case i+1 < len(compressions) && refusedCompression(out):
			i++
			continue
		case locked < lockedTries && lockedOut(out):
			// the server goes on serving: a session asked for a table whole (a machine's
			// grant, a cache's TRUNCATE) between the leader's lock and a worker's
			locked++
			continue
		}
		return fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if opts.Check != nil {
		if err := opts.Check(part); err != nil {
			_ = os.RemoveAll(part)
			return err
		}
	}
	if err := RemoveDump(path); err != nil {
		_ = os.RemoveAll(part)
		return err
	}
	return os.Rename(part, path)
}

// refusedCompression says pg_dump refused the --compress value (a method its build lacks,
// a syntax an older one does not read) before it dumped anything.
func refusedCompression(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "compress")
}

// lockedTries is how often a dump is begun again when a worker could not lock its table.
const lockedTries = 2

// lockedOut says a pg_dump worker could not take its table's lock (NOWAIT): another
// session asked for the table whole after the leader locked it, which a dump in one
// process would have waited out.
func lockedOut(out []byte) bool {
	return strings.Contains(string(out), "could not obtain lock")
}

// RemoveDump removes a dump: a file, or a directory-format dump's folder (none there: nil).
func RemoveDump(path string) error {
	err := os.RemoveAll(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// DumpBytes is a dump's size: its file's, or the files of a directory-format dump
// together (0 when it is not there).
func DumpBytes(path string) int64 {
	info, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	var n int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // gone meanwhile (a dump being written)
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// isDump reports whether a folder's entry is a finished dump: *.dump, a file or a
// directory-format dump's folder (never a link).
func isDump(e fs.DirEntry) bool {
	return strings.HasSuffix(e.Name(), backupSuffix) && (e.Type().IsRegular() || e.IsDir())
}

// IsDump is isDump for a dump's FileInfo (os.Lstat's).
func IsDump(info fs.FileInfo) bool {
	return strings.HasSuffix(info.Name(), backupSuffix) && (info.Mode().IsRegular() || info.IsDir())
}
