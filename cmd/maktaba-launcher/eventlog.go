package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// eventLog appends one JSON object per line to updates/launcher.log and
// echoes a short line to stderr (the service's log).
type eventLog struct {
	mu     sync.Mutex
	f      *os.File
	echo   io.Writer
	now    func() time.Time
	closed bool
}

// Entry is one launcher.log line.
type Entry struct {
	TS      time.Time      `json:"ts"`
	Event   string         `json:"event"`
	Version string         `json:"version,omitempty"`
	Detail  map[string]any `json:"detail,omitempty"`
}

func newEventLog(path string, echo io.Writer, now func() time.Time) *eventLog {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(echo, "maktaba-launcher: cannot open %s: %v (logging to stderr only)\n", path, err)
		f = nil
	}
	return &eventLog{f: f, echo: echo, now: now}
}

func (e *eventLog) Event(event, version string, detail map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ent := Entry{TS: e.now().UTC(), Event: event, Version: version, Detail: detail}
	b, _ := json.Marshal(ent)
	if e.f != nil && !e.closed {
		_, _ = e.f.Write(append(b, '\n'))
		_ = e.f.Sync()
	}
	fmt.Fprintf(e.echo, "[launcher] %s\n", b)
}

func (e *eventLog) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.f != nil && !e.closed {
		_ = e.f.Close()
	}
	e.closed = true
}

// readLog returns the launcher.log entries (unparseable lines skipped).
func readLog(path string) []Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Event != "" {
			out = append(out, e)
		}
	}
	return out
}
