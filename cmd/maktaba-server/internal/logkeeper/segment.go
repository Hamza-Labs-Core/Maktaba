package logkeeper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Zst is a closed hour's extension after .ndjson.
const Zst = ".zst"

// MaxSegment is how large one hour's file grows before it rolls early (-1, -2, ...).
const MaxSegment = 64 << 20

// Index is a segment's .idx: what a reader needs to skip it without opening it.
type Index struct {
	First  time.Time      `json:"first"`
	Last   time.Time      `json:"last"`
	Lines  int            `json:"lines"`
	Bytes  int64          `json:"bytes"`
	Levels map[string]int `json:"levels"`
	// Components counts the lines by Epic 51 component (a reader asking for one skips
	// the hours it never logged in).
	Components map[string]int `json:"components,omitempty"`
}

// stream is one directory's open hour: <service>/, <service>/debug/ or <service>/warn/.
type stream struct {
	dir   string
	hour  string // yyyymmdd-hh of the open file
	part  int
	path  string
	f     *os.File
	w     *bufio.Writer
	idx   Index
	dirty bool
}

// segments writes every service's streams.
type segments struct {
	root    string
	maxSize int64 // MaxSegment (tests make it smaller)
	now     func() time.Time
	// compress closes an hour in the background (zstd, then the .ndjson goes)
	compress func(path string)

	mu      sync.Mutex
	streams map[string]*stream // by dir
	// zipping are the closed hours being compressed (the janitor leaves them)
	zipping map[string]bool
}

func newSegments(root string, now func() time.Time) *segments {
	s := &segments{root: root, maxSize: MaxSegment, now: now, streams: map[string]*stream{}, zipping: map[string]bool{}}
	s.compress = func(p string) {
		s.zipping[p] = true // s.mu is held: closeStream runs under it
		go func() {
			compressFile(p)
			s.mu.Lock()
			delete(s.zipping, p)
			s.mu.Unlock()
		}()
	}
	return s
}

// write appends l to its service's streams: the main one (info and above) or debug/, and
// a copy of warn and above in warn/.
func (s *segments) write(service string, l Line, component string) error {
	svc := safeName(service)
	s.mu.Lock()
	defer s.mu.Unlock()
	main := filepath.Join(s.root, "segments", svc)
	if l.Level == "debug" {
		return s.appendTo(filepath.Join(main, "debug"), l, component)
	}
	if err := s.appendTo(main, l, component); err != nil {
		return err
	}
	if Rank(l.Level) >= Rank("warn") {
		return s.appendTo(filepath.Join(main, "warn"), l, component)
	}
	return nil
}

func (s *segments) appendTo(dir string, l Line, component string) error {
	st := s.streams[dir]
	if st == nil {
		st = &stream{dir: dir}
		s.streams[dir] = st
	}
	hour := s.now().UTC().Format("20060102-15")
	if st.f != nil && (st.hour != hour || st.idx.Bytes+int64(len(l.JSON))+1 > s.maxSize) {
		if st.hour == hour {
			st.part++
		} else {
			st.part = 0
		}
		s.closeStream(st, true)
	}
	if st.f == nil {
		if st.hour != hour {
			st.part = 0
		}
		st.hour = hour
		if err := s.open(st); err != nil {
			return err
		}
	}
	if _, err := st.w.Write(l.JSON); err != nil {
		return err
	}
	if err := st.w.WriteByte('\n'); err != nil {
		return err
	}
	st.dirty = true
	if st.idx.Lines == 0 || l.TS.Before(st.idx.First) {
		st.idx.First = l.TS
	}
	if l.TS.After(st.idx.Last) {
		st.idx.Last = l.TS
	}
	st.idx.Lines++
	st.idx.Bytes += int64(len(l.JSON)) + 1
	st.idx.Levels[l.Level]++
	if component != "" {
		st.idx.Components[component]++
	}
	return nil
}

// open opens the stream's file for its hour and part, picking up an existing one (a
// restart within the hour appends to it; a clock that went back too).
func (s *segments) open(st *stream) error {
	if err := os.MkdirAll(st.dir, 0o700); err != nil {
		return err
	}
	for {
		name := st.hour
		if st.part > 0 {
			name = fmt.Sprintf("%s-%d", st.hour, st.part)
		}
		p := filepath.Join(st.dir, name+".ndjson")
		if _, err := os.Stat(p + Zst); err == nil {
			st.part++ // that hour was closed already: a new part
			continue
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		fi, _ := f.Stat()
		if fi != nil && fi.Size() >= s.maxSize {
			_ = f.Close()
			st.part++
			continue
		}
		st.path, st.f, st.w = p, f, bufio.NewWriterSize(f, 64<<10)
		st.idx = Index{Levels: map[string]int{}, Components: map[string]int{}}
		if old, err := readIndex(idxPath(p)); err == nil {
			st.idx = old
			if st.idx.Levels == nil {
				st.idx.Levels = map[string]int{}
			}
			if st.idx.Components == nil {
				st.idx.Components = map[string]int{}
			}
		}
		if fi != nil {
			st.idx.Bytes = fi.Size()
		}
		return nil
	}
}

// closeStream flushes, syncs and closes the open file; closed hours are compressed (zstd).
func (s *segments) closeStream(st *stream, compress bool) {
	if st.f == nil {
		return
	}
	_ = st.w.Flush()
	_ = st.f.Sync()
	_ = writeIndex(idxPath(st.path), st.idx)
	_ = st.f.Close()
	if compress && s.compress != nil {
		s.compress(st.path)
	}
	st.f, st.w, st.dirty = nil, nil, false
}

// sync flushes every open file to disk (every second: a crash loses at most that), and
// closes the hours that ended.
func (s *segments) sync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	hour := s.now().UTC().Format("20060102-15")
	for _, st := range s.streams {
		if st.f == nil {
			continue
		}
		if st.hour != hour {
			s.closeStream(st, true)
			continue
		}
		if st.dirty {
			_ = st.w.Flush()
			_ = st.f.Sync()
			_ = writeIndex(idxPath(st.path), st.idx)
			st.dirty = false
		}
	}
}

// open reports whether path is a file being written now (the janitor leaves it).
func (s *segments) isOpen(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.zipping[path] {
		return true
	}
	for _, st := range s.streams {
		if st.f != nil && st.path == path {
			return true
		}
	}
	return false
}

func (s *segments) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.streams {
		s.closeStream(st, false)
	}
}

func idxPath(segment string) string {
	return strings.TrimSuffix(strings.TrimSuffix(segment, Zst), ".ndjson") + ".idx"
}

func readIndex(p string) (Index, error) {
	var ix Index
	b, err := os.ReadFile(p)
	if err != nil {
		return ix, err
	}
	err = json.Unmarshal(b, &ix)
	return ix, err
}

func writeIndex(p string, ix Index) error {
	b, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// compressFile writes path.zst (zstd, its default level 3) and removes path (an hour
// that has closed).
func compressFile(path string) {
	in, err := os.Open(path)
	if err != nil {
		return
	}
	defer in.Close()
	tmp := path + Zst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	zw, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return
	}
	_, err = io.Copy(zw, in)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return
	}
	if os.Rename(tmp, path+Zst) == nil {
		_ = in.Close()
		_ = os.Remove(path)
	}
}

// safeName is a service as a directory name (tv-slot/blue → tv-slot.blue).
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '.'
	}, s)
	if s == "" || s == "." || s == ".." {
		return "unknown"
	}
	return s
}
