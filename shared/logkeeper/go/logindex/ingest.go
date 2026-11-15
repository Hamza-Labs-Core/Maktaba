package logindex

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/klauspost/compress/zstd"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// A segment is named by its key: <service>/<kind>/<hour[-part]> (kind "" for the main
// stream, "debug" for debug lines; the warn/ copies are never indexed: their lines are in
// the main stream already). Its file is <key>.ndjson while the hour is open, then
// <key>.ndjson.zst; offsets are into the uncompressed lines either way.

// segFile is the file a segment key reads from now ("" when it is gone).
func (ix *Index) segFile(key string) (path string, compressed bool) {
	base := filepath.Join(ix.root, "segments", filepath.FromSlash(key))
	if _, err := os.Stat(base + ".ndjson"); err == nil {
		return base + ".ndjson", false
	}
	if _, err := os.Stat(base + ".ndjson.zst"); err == nil {
		return base + ".ndjson.zst", true
	}
	return "", false
}

// seg is one indexable segment as a pass lists it: its key, and whether only its closed
// (.zst) file is on disk.
type seg struct {
	key        string
	compressed bool
}

// segments lists every indexable segment, oldest hour first. A directory is listed again
// only when it changed (its mtime or size: a new hour, a compression's rename, the janitor),
// and every relist anyway; an idle pass stats a few dozen directories and opens nothing.
func (ix *Index) segments() []seg {
	root := filepath.Join(ix.root, "segments")
	if ix.dirs == nil || time.Since(ix.listedAt) > relist {
		ix.dirs, ix.listed, ix.listedAt = map[string]dirList{}, nil, time.Now()
	}
	changed := false
	seenDirs := map[string]bool{}
	svcs, _ := os.ReadDir(root)
	for _, s := range svcs {
		if !s.IsDir() {
			continue
		}
		for _, kind := range []string{"", "debug"} {
			dir := filepath.Join(root, s.Name(), kind)
			fi, err := os.Stat(dir)
			if err != nil {
				continue
			}
			seenDirs[dir] = true
			if d, ok := ix.dirs[dir]; ok && d.mod.Equal(fi.ModTime()) && d.size == fi.Size() {
				continue
			}
			changed = true
			ix.dirs[dir] = dirList{mod: fi.ModTime(), size: fi.Size(), segs: listDir(dir, s.Name(), kind)}
		}
	}
	for dir := range ix.dirs {
		if !seenDirs[dir] {
			delete(ix.dirs, dir)
			changed = true
		}
	}
	if !changed && ix.listed != nil {
		return ix.listed
	}
	out := []seg{}
	for _, d := range ix.dirs {
		out = append(out, d.segs...)
	}
	sort.Slice(out, func(i, j int) bool {
		hi, hj := hourOf(out[i].key), hourOf(out[j].key)
		return hi < hj || (hi == hj && out[i].key < out[j].key)
	})
	ix.listed = out
	return out
}

// relist is how often every segments directory is listed again whether it changed or not.
const relist = time.Minute

// dirList is one segments directory as last listed.
type dirList struct {
	mod  time.Time
	size int64
	segs []seg
}

// listDir is one directory's segments (a key with both files on disk reads its .ndjson:
// the hour is open, or being compressed).
func listDir(dir, svc, kind string) []seg {
	open := map[string]bool{}
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		n := e.Name()
		if e.IsDir() {
			continue
		}
		zst := strings.HasSuffix(n, ".ndjson.zst")
		if !zst && !strings.HasSuffix(n, ".ndjson") {
			continue
		}
		n = strings.TrimSuffix(strings.TrimSuffix(n, ".zst"), ".ndjson")
		if len(n) < 11 {
			continue
		}
		k := pathJoin(svc, kind, n)
		open[k] = open[k] || !zst
	}
	out := make([]seg, 0, len(open))
	for k, o := range open {
		out = append(out, seg{key: k, compressed: !o})
	}
	return out
}

func pathJoin(svc, kind, name string) string {
	if kind == "" {
		return svc + "/" + name
	}
	return svc + "/" + kind + "/" + name
}

func hourOf(key string) string { return key[strings.LastIndexByte(key, '/')+1:] }

// segPos is where a segment's indexing stands: off is how far its lines are indexed (into
// the uncompressed lines); done that its closed (.zst) file was read to its end. A closed
// hour never changes, so a done segment is never opened again (positions.size keeps its
// .zst's size; 0 while it is open or not yet read to its end).
type segPos struct {
	off  int64
	done bool
}

// positions is every segment's position, read from logs.db on the first pass and kept in
// memory after: a pass looks up thousands of segments, and a query each was most of an idle
// pass (v0.3.135 ran two cores flat re-reading a week of closed hours every 200 ms).
func (ix *Index) positions() (map[string]segPos, error) {
	if ix.pos != nil {
		return ix.pos, nil
	}
	rows, err := ix.db.Query(`SELECT seg, off, size FROM positions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]segPos{}
	for rows.Next() {
		var k string
		var off, size int64
		if err := rows.Scan(&k, &off, &size); err != nil {
			return nil, err
		}
		m[k] = segPos{off: off, done: size > 0}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ix.pos = m
	return m, nil
}

// forget drops segments' positions from memory (the sweep deleted their rows).
func (ix *Index) forget(keys []string) {
	ix.posMu.Lock()
	defer ix.posMu.Unlock()
	for _, k := range keys {
		delete(ix.pos, k)
	}
}

// Ingest indexes what the segments gained since the last pass, each line exactly once:
// the position of every segment is kept in the same transaction as its lines, so a role
// killed mid-batch starts again where its last commit stopped. An idle pass opens no
// file: it lists the directories and stats the open hours' files only.
func (ix *Index) Ingest(ctx context.Context) (int, error) {
	ix.posMu.Lock()
	defer ix.posMu.Unlock()
	pos, err := ix.positions()
	if err != nil {
		return 0, err
	}
	var total int
	for _, s := range ix.segments() {
		if ctx.Err() != nil {
			break
		}
		p := pos[s.key]
		if s.compressed && p.done {
			continue // a closed hour read to its end
		}
		n, err := ix.ingestSegment(s.key, p.off)
		total += n
		if err != nil {
			return total, err
		}
	}
	if total > 0 {
		ix.notify()
	}
	return total, nil
}

// ingestSegment indexes one segment from its position, in batches of 1,000 lines.
func (ix *Index) ingestSegment(key string, from int64) (int, error) {
	path, compressed := ix.segFile(key)
	if path == "" {
		return 0, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, nil
	}
	if !compressed && fi.Size() <= from {
		return 0, nil // nothing new
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer f.Close()
	var r io.Reader = f
	if compressed {
		// one decoder goroutine and a bounded window: the default starts one per core
		zr, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			return 0, nil
		}
		defer zr.Close()
		if _, err := io.CopyN(io.Discard, zr, from); err != nil {
			// it ends before the position (or does not decode): a closed hour is written
			// whole and renamed into place, so reading it again would never get further
			ix.Log("log_index_segment_unreadable", "seg", key, "err", err)
			return 0, ix.flushSegment(key, from, nil, fi.Size())
		}
		r = zr
	} else if _, err := f.Seek(from, io.SeekStart); err != nil {
		return 0, err
	}
	br := bufio.NewReaderSize(r, 64<<10)
	off := from
	total := 0
	batch := make([]row, 0, 1000)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			batch = append(batch, row{off, bytes.TrimRight(line, "\r\n")})
			off += int64(len(line))
			if len(batch) >= 1000 {
				if err := ix.flushSegment(key, off, batch, 0); err != nil {
					return total, err
				}
				total += len(batch)
				batch = batch[:0]
				from = off
			}
		}
		if err != nil {
			if compressed && err != io.EOF {
				// a closed hour that does not decode to its end: what it had is indexed,
				// and it is not read again every pass
				ix.Log("log_index_segment_unreadable", "seg", key, "err", err)
			}
			// a line still being written (no newline yet) waits for the next pass
			break
		}
	}
	var size int64
	if compressed {
		size = fi.Size() // a closed hour read to its end: never opened again
	}
	if len(batch) == 0 && off == from && size == 0 {
		return total, nil
	}
	if err := ix.flushSegment(key, off, batch, size); err != nil {
		return total, err
	}
	return total + len(batch), nil
}

// row is one line read from a segment, at its offset.
type row struct {
	off  int64
	line []byte
}

// flushSegment indexes a batch and moves the segment's position to end in one
// transaction; size > 0 marks a closed segment read to its end.
func (ix *Index) flushSegment(key string, end int64, batch []row, size int64) error {
	err := ix.write(func() error {
		tx, err := ix.db.Begin()
		if err != nil {
			return err
		}
		for _, b := range batch {
			if err := ix.insert(tx, key, b.off, b.line); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO positions(seg, off, size) VALUES(?, ?, ?)
			ON CONFLICT(seg) DO UPDATE SET off = excluded.off, size = excluded.size`, key, end, size); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return err
	}
	ix.pos[key] = segPos{off: end, done: size > 0}
	return nil
}

// insert adds one line's row and its words; a line that does not parse is skipped.
func (ix *Index) insert(tx *sql.Tx, key string, off int64, raw []byte) error {
	p, ok := parseLine(raw)
	if !ok {
		return nil
	}
	res, err := tx.Exec(`INSERT INTO lines(ts, level, service, component, node, msg, request_id, job_id, session_id,
		video_id, user_h, seg, off) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ts, p.level, p.service, nullable(p.component), p.node, p.msg, nullable(p.requestID), nullable(p.jobID),
		nullable(p.sessionID), nullable(p.videoID), nullable(p.userH), key, off)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO lines_fts(rowid, msg, text, err) VALUES(?, ?, ?, ?)`, id, p.msg, p.text, p.errText); err != nil {
		return err
	}
	if p.level >= levelNum["error"] {
		fp, err := ix.group(tx, p, id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE lines SET error_fp = ? WHERE id = ?`, fp, id)
		return err
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// parsed is what the index keeps of a line.
type parsed struct {
	ts                                          int64
	level                                       int
	service, component, node, msg               string
	requestID, jobID, sessionID, videoID, userH string
	text, errText                               string
	// for error groups (53.10): the message before folding, the error's own message and
	// code, its first own frame (fn) and the line's version
	rawMsg, errMsg, errCode, frame, version string
}

// parseLine reads a shared-schema line (story 53.01). A user id on an old line is hashed
// on the way in: the index never keeps one.
func parseLine(raw []byte) (parsed, bool) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return parsed{}, false
	}
	str := func(k string) string {
		switch v := m[k].(type) {
		case string:
			return v
		case float64:
			return fmt.Sprintf("%.0f", v)
		}
		return ""
	}
	p := parsed{level: LevelNum(str("level")), service: str("service"), component: str("component"),
		node: str("node"), msg: str("msg"), requestID: str("request_id"), jobID: str("job_id"),
		sessionID: str("session_id"), videoID: str("video_id"), userH: str("user_h")}
	if p.requestID == "" {
		// a pipeline job's lines name the request that queued it (story 53.08): searching
		// that request's id finds the job's lines too
		p.requestID = str("origin_request_id")
	}
	if p.userH == "" {
		if id := str("user_id"); id != "" {
			p.userH = mlog.HashUser(id)
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, str("ts")); err == nil {
		p.ts = t.UnixMilli()
	}
	if p.node == "" {
		p.node = "home"
	}
	if p.service == "" {
		p.service = "unknown"
	}
	p.rawMsg, p.version = p.msg, str("version")
	switch e := m["err"].(type) {
	case string:
		p.errText, p.errMsg = e, e
	case map[string]any:
		p.errMsg, _ = e["msg"].(string)
		p.errCode, _ = e["code"].(string)
		if st, ok := e["stack"].([]any); ok {
			for _, f := range st {
				if fr, ok := f.(map[string]any); ok {
					if own, _ := fr["own"].(bool); own {
						p.frame, _ = fr["fn"].(string)
						break
					}
				}
			}
		}
		for _, k := range []string{"msg", "code", "kind"} {
			if s, ok := e[k].(string); ok && s != "" {
				p.errText += s + " "
			}
		}
	}
	// the searchable text: attrs' values and every other field's string values
	var b strings.Builder
	var add func(v any)
	add = func(v any) {
		switch t := v.(type) {
		case string:
			if b.Len() < 8<<10 {
				b.WriteString(t)
				b.WriteByte(' ')
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				add(t[k])
			}
		case []any:
			for _, x := range t {
				add(x)
			}
		}
	}
	for k, v := range m {
		switch k {
		case "ts", "level", "service", "node", "msg", "err", "version", "env", "user_h", "user_id":
			continue
		}
		add(v)
	}
	p.text = b.String()
	if len(p.text) > 8<<10 {
		p.text = p.text[:8<<10]
	}
	p.msg = foldArabic(p.msg)
	p.text = foldArabic(p.text)
	p.errText = foldArabic(p.errText)
	return p, true
}

// foldArabic drops the Arabic marks a search ignores (harakat, the dagger alif, tatweel):
// unicode61's remove_diacritics folds Latin marks only. The query is folded the same way.
func foldArabic(s string) string {
	if !strings.ContainsFunc(s, isArabicMark) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isArabicMark(r) {
			return -1
		}
		return r
	}, s)
}

func isArabicMark(r rune) bool {
	return (r >= 0x064B && r <= 0x065F) || r == 0x0670 || r == 0x0640 || (unicode.Is(unicode.Mn, r) && r >= 0x0610 && r <= 0x061A)
}
