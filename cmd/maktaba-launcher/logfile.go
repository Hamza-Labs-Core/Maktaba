package main

import (
	"os"
	"path/filepath"
	"sync"
)

// logRotateBytes is when --log-file is renamed to <file>.1 (one old copy kept).
const logRotateBytes = 50 << 20

// logFile is where `serve --log-file PATH` sends the launcher's and the
// server's output (Epic 39 story 39.5: the Linux app's systemd unit has no
// terminal and no app shell to hold a log): appended to PATH, renamed to
// PATH.1 when it is over the limit at start and as soon as it grows past it.
type logFile struct {
	mu    sync.Mutex
	path  string
	limit int64
	f     *os.File
	size  int64
}

func openLogFile(path string, limit int64) (*logFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &logFile{path: path, limit: limit}
	if info, err := os.Stat(path); err == nil && info.Size() > limit {
		_ = os.Rename(path, path+".1")
	}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *logFile) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.f, l.size = f, info.Size()
	return nil
}

// Write appends p, rotating first when p would take the file past the limit.
// A failed rotation keeps writing to the file it has.
func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size > 0 && l.size+int64(len(p)) > l.limit {
		if err := l.f.Close(); err == nil {
			_ = os.Rename(l.path, l.path+".1")
		}
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

func (l *logFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
