package pgbundle

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// A dump's chunked tables (Chunked): a session of the dump's role exports a snapshot and
// stays open; pg_dump takes it (--snapshot) and leaves those tables' rows out; as many
// psql sessions as the dump has workers each set the same snapshot and COPY one range of
// the table's key out in binary, into chunk.<table>.<n>.bin files beside pg_dump's (the
// folder's own: pg_restore reads only what its toc.dat names). They go back between the
// data and the indexes (LoadChunks): pre-data and data first, then the chunks, eight at
// once, then post-data.

// chunksSuffix names the folder the chunks are written in while pg_dump writes its own
// (beside path.partial; it is moved in once both are whole).
const chunksSuffix = ".partial-chunks"

// chunkPrefix and chunkExt make a chunk's file name: chunk.<schema.table>.<n>.bin.
const (
	chunkPrefix = "chunk."
	chunkExt    = ".bin"
)

// PartialBytes is how much of the dump to path is written so far (pg_dump's files and the
// chunks): a dump's progress.
func PartialBytes(path string) int64 {
	return DumpBytes(path+".partial") + DumpBytes(path+chunksSuffix)
}

// snapshot is the dump's exported snapshot, held open while the dump reads it.
type snapshot struct {
	id     string
	tables []ChunkedTable
	ranges map[string][2]int64 // the key's min and max, by table
	db     *sql.DB
	tx     *sql.Tx
}

func (s *snapshot) close() {
	if s.tx != nil {
		_ = s.tx.Rollback()
	}
	if s.db != nil {
		_ = s.db.Close()
	}
}

// exportSnapshot opens the dump's snapshot and finds the chunked tables in it (with their
// key's range); none (no Open, no psql): an empty snapshot, pg_dump takes its own.
func exportSnapshot(ctx context.Context, opts DumpOptions) (*snapshot, error) {
	s := &snapshot{ranges: map[string][2]int64{}}
	if opts.Open == nil || opts.PSQL == "" || len(Chunked) == 0 {
		return s, nil
	}
	db, err := opts.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("the dump's snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	s.db = db
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		s.close()
		return nil, fmt.Errorf("the dump's snapshot: %w", err)
	}
	s.tx = tx
	if err := tx.QueryRowContext(ctx, `SELECT pg_export_snapshot()`).Scan(&s.id); err != nil {
		s.close()
		return nil, fmt.Errorf("the dump's snapshot: %w", err)
	}
	for _, t := range Chunked {
		var there bool
		if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, t.Name).Scan(&there); err != nil {
			s.close()
			return nil, err
		}
		if !there {
			continue // no pgvector here: nothing of it to dump
		}
		var lo, hi sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT min(`+quoteIdent(t.Key)+`), max(`+quoteIdent(t.Key)+`) FROM `+t.Name).
			Scan(&lo, &hi); err != nil {
			s.close()
			return nil, err
		}
		s.tables = append(s.tables, t)
		s.ranges[t.Name] = [2]int64{lo.Int64, hi.Int64}
	}
	return s, nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// dumpChunks writes each chunked table in jobs ranges of its key, all at once, into dir.
func dumpChunks(ctx context.Context, psql string, conn, env []string, dir string, s *snapshot, jobs int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	type piece struct {
		file, sql string
	}
	var pieces []piece
	for _, t := range s.tables {
		r := s.ranges[t.Name]
		span := (r[1] - r[0] + 1 + int64(jobs) - 1) / int64(jobs)
		span = max(span, 1)
		for i := 0; i < jobs; i++ {
			lo := r[0] + int64(i)*span
			where := fmt.Sprintf("%s >= %d AND %s < %d", quoteIdent(t.Key), lo, quoteIdent(t.Key), lo+span)
			switch i {
			case 0:
				where = fmt.Sprintf("%s < %d", quoteIdent(t.Key), lo+span) // NULL keys cannot be: a primary key
			case jobs - 1:
				where = fmt.Sprintf("%s >= %d", quoteIdent(t.Key), lo)
			}
			pieces = append(pieces, piece{file: filepath.Join(dir, fmt.Sprintf("%s%s.%03d%s", chunkPrefix, t.Name, i, chunkExt)),
				sql: "COPY (SELECT * FROM " + t.Name + " WHERE " + where + ") TO STDOUT (FORMAT binary)"})
		}
	}
	errs := make(chan error, len(pieces))
	var wg sync.WaitGroup
	for _, p := range pieces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- copyOut(ctx, psql, conn, env, s.id, p.sql, p.file)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// copyOut runs one COPY ... TO STDOUT in the snapshot id into file.
func copyOut(ctx context.Context, psql string, conn, env []string, id, copySQL, file string) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // the dump's own folder
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, psql, append(append([]string{}, conn...), "-X", "-q", "-v", "ON_ERROR_STOP=1",
		"-c", "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY",
		"-c", "SET TRANSACTION SNAPSHOT '"+id+"'",
		"-c", copySQL, "-c", "COMMIT")...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = f, &stderr
	err = cmd.Run()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("%s: %w: %s", copySQL, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Chunks are a dump's chunk files, by table (none for a dump file, or a folder without).
func Chunks(dump string) map[string][]string {
	ents, err := os.ReadDir(dump)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, e := range ents {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(n, chunkPrefix) || !strings.HasSuffix(n, chunkExt) {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(n, chunkPrefix), chunkExt) // schema.table.n
		dot := strings.LastIndexByte(mid, '.')
		if dot < 0 {
			continue
		}
		if _, err := strconv.Atoi(mid[dot+1:]); err != nil {
			continue
		}
		out[mid[:dot]] = append(out[mid[:dot]], filepath.Join(dump, n))
	}
	for _, fs := range out {
		sort.Strings(fs)
	}
	return out
}

// LoadChunks copies a dump's chunks back into their tables (made by the dump's pre-data,
// empty), jobs psql sessions at once, with psql's connection arguments conn.
func LoadChunks(ctx context.Context, psql string, conn, env []string, chunks map[string][]string, jobs int) error {
	type piece struct{ table, file string }
	var pieces []piece
	for t, files := range chunks {
		for _, f := range files {
			pieces = append(pieces, piece{t, f})
		}
	}
	jobs = max(jobs, 1)
	sem := make(chan struct{}, jobs)
	errs := make(chan error, len(pieces))
	var wg sync.WaitGroup
	for _, p := range pieces {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			errs <- copyIn(ctx, psql, conn, env, p.table, p.file)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func copyIn(ctx context.Context, psql string, conn, env []string, table, file string) error {
	f, err := os.Open(file) //nolint:gosec // a dump's own chunk
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	cmd := exec.CommandContext(ctx, psql, append(append([]string{}, conn...), "-X", "-q", "-v", "ON_ERROR_STOP=1",
		"-c", "COPY "+table+" FROM STDIN (FORMAT binary)")...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = f, io.Discard, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("COPY %s FROM %s: %w: %s", table, filepath.Base(file), err, strings.TrimSpace(out.String()))
	}
	return nil
}

// CopyScript is the psql script that loads a dump's chunks inside a script of its own (an
// in-place restore in one transaction): one \copy per chunk.
func CopyScript(chunks map[string][]string) string {
	var tables []string
	for t := range chunks {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	var sb strings.Builder
	for _, t := range tables {
		for _, f := range chunks[t] {
			sb.WriteString(`\copy ` + t + ` FROM '` + strings.ReplaceAll(f, `'`, `''`) + "' WITH (FORMAT binary)\n")
		}
	}
	return sb.String()
}
