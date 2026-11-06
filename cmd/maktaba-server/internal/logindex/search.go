package logindex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/klauspost/compress/zstd"
)

// Query is a search: filters and words.
type Query struct {
	// Q is words, "quoted phrases", -word and prefix*; no column filters.
	Q         string
	Service   string
	Component string
	Level     string // the lowest level ("": every level but debug)
	Node      string
	Since     time.Time
	Until     time.Time
	RequestID string
	JobID     string
	UserH     string
	// Cursor is where the previous page stopped ("<ts>:<id>"); Limit at most 500 (0: 200).
	Cursor string
	Limit  int
	// AfterID returns only lines indexed after this row (the live tail).
	AfterID int64
}

// Result is a page of lines, newest first.
type Result struct {
	Lines []json.RawMessage `json:"lines"`
	// IDs are the lines' row ids (the tail and "around" take them).
	IDs    []int64 `json:"ids"`
	Cursor string  `json:"cursor,omitempty"`
	// Partial: the query was cut at its budget; what it had is here.
	Partial bool `json:"partial,omitempty"`
}

// ErrQuery is a query the index refuses (a column filter, nothing but exclusions).
var ErrQuery = errors.New("logindex: unusable query")

// ftsQuery turns the person's words into an FTS5 MATCH expression: words and prefixes
// (a*), "phrases", and -word exclusions; anything else (column filters, operators, NEAR)
// is refused.
func ftsQuery(q string) (string, error) {
	q = strings.TrimSpace(foldArabic(q))
	if q == "" {
		return "", nil
	}
	var pos, neg []string
	for len(q) > 0 {
		q = strings.TrimLeftFunc(q, unicode.IsSpace)
		if q == "" {
			break
		}
		exclude := false
		if q[0] == '-' {
			exclude, q = true, q[1:]
		}
		var term string
		if strings.HasPrefix(q, `"`) {
			end := strings.IndexByte(q[1:], '"')
			if end < 0 {
				return "", ErrQuery
			}
			phrase := q[1 : end+1]
			q = q[end+2:]
			if strings.ContainsAny(phrase, `"`) || strings.TrimSpace(phrase) == "" {
				return "", ErrQuery
			}
			term = `"` + phrase + `"`
		} else {
			end := strings.IndexFunc(q, unicode.IsSpace)
			if end < 0 {
				end = len(q)
			}
			word := q[:end]
			q = q[end:]
			prefix := strings.HasSuffix(word, "*")
			word = strings.TrimSuffix(word, "*")
			for _, r := range word {
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' && r != '-' {
					return "", ErrQuery
				}
			}
			if word == "" {
				return "", ErrQuery
			}
			term = `"` + word + `"`
			if prefix {
				term += "*"
			}
		}
		if exclude {
			neg = append(neg, term)
		} else {
			pos = append(pos, term)
		}
	}
	if len(pos) == 0 {
		return "", ErrQuery
	}
	out := strings.Join(pos, " AND ")
	for _, n := range neg {
		out += " NOT " + n
	}
	return out, nil
}

// Budget is how long a search may take before it answers with what it has.
var Budget = 3 * time.Second

// Search answers q, newest first, each line read from its segment.
func (ix *Index) Search(ctx context.Context, q Query) (Result, error) {
	match, err := ftsQuery(q.Q)
	if err != nil {
		return Result{}, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	where := []string{"1=1"}
	var args []any
	add := func(cond string, v any) { where = append(where, cond); args = append(args, v) }
	if q.Service != "" {
		add("service = ?", q.Service)
	}
	if q.Component != "" {
		add("component = ?", q.Component)
	}
	if q.Level != "" {
		add("level >= ?", LevelNum(q.Level))
	} else if q.RequestID == "" && q.JobID == "" {
		add("level >= ?", LevelNum("info"))
	}
	if q.Node != "" {
		add("node = ?", q.Node)
	}
	if !q.Since.IsZero() {
		add("ts >= ?", q.Since.UnixMilli())
	}
	if !q.Until.IsZero() {
		add("ts <= ?", q.Until.UnixMilli())
	}
	if q.RequestID != "" {
		// the short reference (the first 8 characters) is accepted too (story 53.08)
		if len(q.RequestID) < 36 {
			add("request_id LIKE ?", q.RequestID+"%")
		} else {
			add("request_id = ?", q.RequestID)
		}
	}
	if q.JobID != "" {
		add("job_id = ?", q.JobID)
	}
	if q.UserH != "" {
		add("user_h = ?", q.UserH)
	}
	if q.AfterID > 0 {
		add("id > ?", q.AfterID)
	}
	if c := q.Cursor; c != "" {
		ts, id, ok := strings.Cut(c, ":")
		t, err1 := strconv.ParseInt(ts, 10, 64)
		n, err2 := strconv.ParseInt(id, 10, 64)
		if !ok || err1 != nil || err2 != nil {
			return Result{}, ErrQuery
		}
		where = append(where, "(ts < ? OR (ts = ? AND id < ?))")
		args = append(args, t, t, n)
	}
	if match != "" {
		add("id IN (SELECT rowid FROM lines_fts WHERE lines_fts MATCH ?)", match)
	}
	order := "ts DESC, id DESC"
	if q.RequestID != "" || q.JobID != "" || q.AfterID > 0 {
		order = "ts ASC, id ASC" // one request, one job, a tail: in time order
	}
	sqlq := fmt.Sprintf(`SELECT id, ts, seg, off FROM lines WHERE %s ORDER BY %s LIMIT %d`,
		strings.Join(where, " AND "), order, limit+1)
	cctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	rows, err := ix.db.QueryContext(cctx, sqlq, args...)
	if err != nil {
		if cctx.Err() != nil {
			return Result{Partial: true}, nil
		}
		return Result{}, err
	}
	type hit struct {
		id, ts, off int64
		seg         string
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.ts, &h.seg, &h.off); err != nil {
			break
		}
		hits = append(hits, h)
	}
	partial := cctx.Err() != nil
	_ = rows.Close()
	res := Result{Partial: partial}
	if len(hits) > limit {
		last := hits[limit-1]
		res.Cursor = fmt.Sprintf("%d:%d", last.ts, last.id)
		hits = hits[:limit]
	}
	// each line from its segment, one open per segment
	bySeg := map[string][]int{}
	for i, h := range hits {
		bySeg[h.seg] = append(bySeg[h.seg], i)
	}
	lines := make([]json.RawMessage, len(hits))
	for seg, idx := range bySeg {
		offs := make([]int64, len(idx))
		for i, j := range idx {
			offs[i] = hits[j].off
		}
		got := ix.readAt(seg, offs)
		for i, j := range idx {
			lines[j] = got[i]
		}
	}
	for i, h := range hits {
		if lines[i] == nil {
			continue // its segment went (retention) or was rewritten
		}
		res.Lines = append(res.Lines, lines[i])
		res.IDs = append(res.IDs, h.id)
	}
	return res, nil
}

// readAt reads the lines at offs of one segment (nil for each it cannot).
func (ix *Index) readAt(seg string, offs []int64) []json.RawMessage {
	out := make([]json.RawMessage, len(offs))
	path, compressed := ix.segFile(seg)
	if path == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	if !compressed {
		for i, off := range offs {
			if _, err := f.Seek(off, io.SeekStart); err != nil {
				continue
			}
			line, err := bufio.NewReaderSize(f, 64<<10).ReadBytes('\n')
			if err == nil && json.Valid(line) {
				out[i] = json.RawMessage(trimNL(line))
			}
		}
		return out
	}
	zr, err := zstd.NewReader(f)
	if err != nil {
		return out
	}
	defer zr.Close()
	order := make([]int, len(offs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return offs[order[a]] < offs[order[b]] })
	br := bufio.NewReaderSize(zr, 256<<10)
	var at int64
	for _, i := range order {
		if _, err := io.CopyN(io.Discard, br, offs[i]-at); err != nil {
			break
		}
		at = offs[i]
		line, err := br.ReadBytes('\n')
		at += int64(len(line))
		if err == nil && json.Valid(line) {
			out[i] = json.RawMessage(trimNL(line))
		}
	}
	return out
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return append([]byte(nil), b...)
}

// Around is the lines of every service around one line: before older ones, after newer
// ones, in time order (the "what else happened then" view).
func (ix *Index) Around(ctx context.Context, id int64, before, after int) (Result, error) {
	var ts int64
	if err := ix.db.QueryRowContext(ctx, `SELECT ts FROM lines WHERE id = ?`, id).Scan(&ts); err != nil {
		return Result{}, err
	}
	pick := func(q string, n int) ([]int64, error) {
		rows, err := ix.db.QueryContext(ctx, q, ts, ts, id, n)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var x int64
			if rows.Scan(&x) == nil {
				ids = append(ids, x)
			}
		}
		return ids, nil
	}
	older, err := pick(`SELECT id FROM lines WHERE ts < ? OR (ts = ? AND id < ?) ORDER BY ts DESC, id DESC LIMIT ?`, before)
	if err != nil {
		return Result{}, err
	}
	newer, err := pick(`SELECT id FROM lines WHERE ts > ? OR (ts = ? AND id >= ?) ORDER BY ts ASC, id ASC LIMIT ?`, after+1)
	if err != nil {
		return Result{}, err
	}
	for i, j := 0, len(older)-1; i < j; i, j = i+1, j-1 {
		older[i], older[j] = older[j], older[i]
	}
	return ix.byIDs(ctx, append(older, newer...))
}

// byIDs reads the given rows' lines, in that order.
func (ix *Index) byIDs(ctx context.Context, ids []int64) (Result, error) {
	var res Result
	for _, id := range ids {
		var seg string
		var off int64
		if err := ix.db.QueryRowContext(ctx, `SELECT seg, off FROM lines WHERE id = ?`, id).Scan(&seg, &off); err != nil {
			continue
		}
		if l := ix.readAt(seg, []int64{off})[0]; l != nil {
			res.Lines = append(res.Lines, l)
			res.IDs = append(res.IDs, id)
		}
	}
	return res, nil
}
