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

// segments lists every indexable segment key, oldest hour first.
func (ix *Index) segments() []string {
	root := filepath.Join(ix.root, "segments")
	seen := map[string]bool{}
	svcs, _ := os.ReadDir(root)
	for _, s := range svcs {
		if !s.IsDir() {
			continue
		}
		for _, kind := range []string{"", "debug"} {
			es, _ := os.ReadDir(filepath.Join(root, s.Name(), kind))
			for _, e := range es {
				n := e.Name()
				if e.IsDir() {
					continue
				}
				n = strings.TrimSuffix(strings.TrimSuffix(n, ".zst"), ".ndjson")
				if n == e.Name() || len(n) < 11 {
					continue
				}
				seen[pathJoin(s.Name(), kind, n)] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return hourOf(out[i]) < hourOf(out[j]) || (hourOf(out[i]) == hourOf(out[j]) && out[i] < out[j])
	})
	return out
}

func pathJoin(svc, kind, name string) string {
	if kind == "" {
		return svc + "/" + name
	}
	return svc + "/" + kind + "/" + name
}

func hourOf(key string) string { return key[strings.LastIndexByte(key, '/')+1:] }

// Ingest indexes what the segments gained since the last pass, each line exactly once:
// the position of every segment is kept in the same transaction as its lines, so a role
// killed mid-batch starts again where its last commit stopped.
func (ix *Index) Ingest(ctx context.Context) (int, error) {
	var total int
	for _, key := range ix.segments() {
		if ctx.Err() != nil {
			break
		}
		n, err := ix.ingestSegment(key)
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

func (ix *Index) position(key string) int64 {
	var off int64
	_ = ix.db.QueryRow(`SELECT off FROM positions WHERE seg = ?`, key).Scan(&off)
	return off
}

// ingestSegment indexes one segment from its position, in batches of 1,000 lines.
func (ix *Index) ingestSegment(key string) (int, error) {
	path, compressed := ix.segFile(key)
	if path == "" {
		return 0, nil
	}
	from := ix.position(key)
	if !compressed {
		if fi, err := os.Stat(path); err == nil && fi.Size() <= from {
			return 0, nil // nothing new
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer f.Close()
	var r io.Reader = f
	if compressed {
		zr, err := zstd.NewReader(f)
		if err != nil {
			return 0, nil
		}
		defer zr.Close()
		if _, err := io.CopyN(io.Discard, zr, from); err != nil {
			return 0, nil // indexed to its end already
		}
		r = zr
	} else if _, err := f.Seek(from, io.SeekStart); err != nil {
		return 0, err
	}
	br := bufio.NewReaderSize(r, 256<<10)
	off := from
	total := 0
	type row struct {
		off  int64
		line []byte
	}
	batch := make([]row, 0, 1000)
	flush := func(end int64) error {
		if len(batch) == 0 && end == from {
			return nil
		}
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
		if _, err := tx.Exec(`INSERT INTO positions(seg, off) VALUES(?, ?) ON CONFLICT(seg) DO UPDATE SET off = excluded.off`,
			key, end); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		total += len(batch)
		batch = batch[:0]
		from = end
		return nil
	}
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			batch = append(batch, row{off, bytes.TrimRight(line, "\r\n")})
			off += int64(len(line))
			if len(batch) >= 1000 {
				if err := flush(off); err != nil {
					return total, err
				}
			}
		}
		if err != nil {
			// a line still being written (no newline yet) waits for the next pass
			break
		}
	}
	return total, flush(off)
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
	_, err = tx.Exec(`INSERT INTO lines_fts(rowid, msg, text, err) VALUES(?, ?, ?, ?)`, id, p.msg, p.text, p.errText)
	return err
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
	switch e := m["err"].(type) {
	case string:
		p.errText = e
	case map[string]any:
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
