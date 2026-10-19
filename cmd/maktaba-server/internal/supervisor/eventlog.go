package supervisor

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// EventSink persists supervisor events so a crash stays visible after
// the fact, including across a supervisor restart.
type EventSink interface {
	Append(Event) error
	// Load returns up to n of the most recent events, oldest first.
	Load(n int) ([]Event, error)
}

// FileSink appends events as JSON lines to a file, rotating it to
// "<path>.1" once it passes maxBytes.
type FileSink struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
}

// NewFileSink returns a sink writing to path (its directory is created
// on the first append). maxBytes <= 0 uses 1 MiB.
func NewFileSink(path string, maxBytes int64) *FileSink {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	return &FileSink{path: path, maxBytes: maxBytes}
}

// Append writes one event.
func (f *FileSink) Append(e Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(f.path); err == nil && info.Size() >= f.maxBytes {
		_ = os.Remove(f.path + ".1")
		if err := os.Rename(f.path, f.path+".1"); err != nil {
			return err
		}
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	fh, err := os.OpenFile(f.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fh.Write(append(line, '\n')); err != nil {
		_ = fh.Close()
		return err
	}
	return fh.Close()
}

// Load reads the newest n events from the rotated and current files.
// A missing file is not an error; an unparseable line is skipped.
func (f *FileSink) Load(n int) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Event
	for _, p := range []string{f.path + ".1", f.path} {
		fh, err := os.Open(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				out = append(out, e)
			}
		}
		_ = fh.Close()
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}
