package logkeeper

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// MaxRead bounds what one query reads from disk (the files' sizes, compressed).
const MaxRead = 50 << 20

// Query asks the segments for lines without the index (the recovery console, 53.04).
type Query struct {
	Services  []string // segment directories (api, pipeline.stt-0); empty: all
	Component string   // an Epic 51 catalog id; "": any
	MinLevel  string   // "": info (debug is read only when asked for)
	Since     time.Time
	Until     time.Time // zero: now
	Text      string    // a case-insensitive substring of the line; "": any
	Limit     int       // 0: 500
}

// Result is a query's lines, newest first.
type Result struct {
	Lines []json.RawMessage `json:"lines"`
	// Truncated: MaxRead was reached before every file in the window was read.
	Truncated bool `json:"truncated"`
}

// Read answers q from the segment files.
func (k *Keeper) Read(q Query) Result {
	k.segs.sync() // what is buffered is readable
	return ReadDir(k.cfg.Root, q, k.cfg.Now())
}

// ReadDir answers q from the segments under root (a keeper's Root).
func ReadDir(root string, q Query, now time.Time) Result {
	if q.Limit <= 0 {
		q.Limit = 500
	}
	if q.Until.IsZero() {
		q.Until = now
	}
	floor := Rank(q.MinLevel)
	if q.MinLevel == "" {
		floor = Rank("info")
	}
	text := strings.ToLower(q.Text)
	want := map[string]bool{}
	for _, s := range q.Services {
		want[safeName(s)] = true
	}
	k := &Keeper{cfg: Config{Root: root}}
	files := k.files()
	// the main files' hours, by service: a warn/ copy is read only for an hour whose main
	// file is gone (the janitor keeps the floor longest)
	mainHours := map[string]bool{}
	for _, f := range files {
		if f.kind == "" {
			mainHours[f.service+"|"+hourKey(f.path)] = true
		}
	}
	var picked []segmentFile
	for _, f := range files {
		if len(want) > 0 && !want[f.service] {
			continue
		}
		warnOnly := floor >= Rank("warn") // warn/ holds every warn+ line: it alone answers
		switch {
		case f.kind == "debug" && floor > Rank("debug"):
			continue
		case f.kind == "warn" && !warnOnly && mainHours[f.service+"|"+hourKey(f.path)]:
			continue
		case f.kind != "warn" && warnOnly:
			continue
		}
		if f.hour.Add(time.Hour).Before(q.Since) || f.hour.After(q.Until) {
			continue
		}
		if ix, err := readIndex(idxPath(f.path)); err == nil {
			if ix.Lines > 0 && (ix.Last.Before(q.Since) || ix.First.After(q.Until)) {
				continue
			}
			if q.Component != "" && len(ix.Components) > 0 && ix.Components[q.Component] == 0 {
				continue
			}
		}
		picked = append(picked, f)
	}
	// newest first, so the budget goes to the recent hours
	sort.Slice(picked, func(i, j int) bool { return picked[i].hour.After(picked[j].hour) })
	type hit struct {
		ts  time.Time
		raw json.RawMessage
	}
	var hits []hit
	var read int64
	res := Result{}
	for _, f := range picked {
		if read+f.size > MaxRead && read > 0 {
			res.Truncated = true
			break
		}
		read += f.size
		_ = scanFile(f.path, func(b []byte) {
			var m struct {
				TS        string `json:"ts"`
				Level     string `json:"level"`
				Component string `json:"component"`
			}
			if json.Unmarshal(b, &m) != nil {
				return
			}
			if Rank(m.Level) < floor || (q.Component != "" && m.Component != q.Component) {
				return
			}
			ts := parseTS(m.TS)
			if ts.Before(q.Since) || ts.After(q.Until) {
				return
			}
			if text != "" && !strings.Contains(strings.ToLower(string(b)), text) {
				return
			}
			hits = append(hits, hit{ts, append(json.RawMessage(nil), b...)})
		})
		if len(hits) >= q.Limit*4 && len(picked) > 1 {
			// enough of the newest hours read; older files can only add older lines
			sort.SliceStable(hits, func(i, j int) bool { return hits[i].ts.After(hits[j].ts) })
			if hits[q.Limit-1].ts.After(f.hour) {
				break
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].ts.After(hits[j].ts) })
	if len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	for _, h := range hits {
		res.Lines = append(res.Lines, h.raw)
	}
	return res
}

func hourKey(path string) string {
	base := path[strings.LastIndexAny(path, `/\`)+1:]
	return strings.TrimSuffix(strings.TrimSuffix(base, Zst), ".ndjson")
}

// scanFile calls fn with each line of a segment (compressed or not).
func scanFile(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, Zst) {
		zr, err := zstd.NewReader(f)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), MaxLine+4096)
	for sc.Scan() {
		fn(sc.Bytes())
	}
	return sc.Err()
}
