package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// waitDelay bounds how long Wait waits for a server's output after the server
// exits: a writer that is not a file is copied through a pipe its children
// inherit (run.go), and they may outlive it.
var waitDelay = 5 * time.Second

// logRotateBytes is when --log-file is renamed to <file>.1 (one old copy kept).
const logRotateBytes = 50 << 20

// logRetry is how long output is dropped after the log could not be written
// (a full disk, a reopen at rotation that failed) before the file is tried
// again.
var logRetry = 10 * time.Second

// logSink is the open log file (tests replace it with one that fails).
type logSink interface {
	io.WriteCloser
	Stat() (os.FileInfo, error)
}

// openLogSink opens the log for appending, the user's only (tests replace it).
var openLogSink = func(path string) (logSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Chmod(0o600) // a log another program created first
	return f, nil
}

// logFile is where `serve --log-file PATH` sends the launcher's and the
// server's output (Epic 39 story 39.5: the Linux app's systemd unit has no
// terminal and no app shell to hold a log): appended to PATH, renamed to
// PATH.1 when it is over the limit at start and as soon as it grows past it.
//
// Writing never fails and never blocks on the disk: output that cannot be
// written (a full disk) is dropped and counted, the file is tried again after
// logRetry, and a line then says how much was lost. The server and the
// launcher write into a pipe that must always be drained (logPipe).
type logFile struct {
	mu    sync.Mutex
	path  string
	limit int64
	now   func() time.Time
	f     logSink // nil: not open (the last open failed)
	size  int64
	// failed is when writing last failed (zero: it works); dropped what was lost
	// since, and why.
	failed  time.Time
	dropped int64
	why     error
}

func openLogFile(path string, limit int64) (*logFile, error) {
	// the server's output can hold paths and names of the library: the user's
	// only, the directory too when setup made it first
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700)
	l := &logFile{path: path, limit: limit, now: time.Now}
	if info, err := os.Stat(path); err == nil && info.Size() > limit {
		_ = os.Rename(path, path+".1")
	}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *logFile) open() error {
	f, err := openLogSink(l.path)
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
// It always reports p written: what the disk refuses is dropped (logFile).
func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.failed.IsZero() {
		if l.now().Sub(l.failed) < logRetry {
			l.dropped += int64(len(p))
			return len(p), nil
		}
		l.failed = time.Time{}
		if l.f == nil {
			if err := l.open(); err != nil {
				return l.drop(p, err)
			}
		}
		if note := fmt.Sprintf("[maktaba-launcher] %d bytes of output were lost: the log could not be written (%v)\n",
			l.dropped, l.why); !l.write([]byte(note)) {
			return l.drop(p, l.why)
		}
		l.dropped, l.why = 0, nil
	}
	if l.size > 0 && l.size+int64(len(p)) > l.limit {
		if err := l.f.Close(); err == nil {
			_ = os.Rename(l.path, l.path+".1")
		}
		l.f = nil
		if err := l.open(); err != nil {
			return l.drop(p, err)
		}
	}
	if !l.write(p) {
		return l.drop(p, l.why)
	}
	return len(p), nil
}

// write writes b whole; false (why set) when the file refused it.
func (l *logFile) write(b []byte) bool {
	n, err := l.f.Write(b)
	l.size += int64(n)
	if err != nil {
		l.why = err
		return false
	}
	return true
}

// drop counts p as lost and starts the wait before the next try.
func (l *logFile) drop(p []byte, why error) (int, error) {
	l.failed, l.why = l.now(), why
	l.dropped += int64(len(p))
	return len(p), nil
}

func (l *logFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}

// logPipe is serve's --log-file: a pipe whose write end the launcher and the
// server write to, copied into the rotating log. The server gets a real file
// descriptor (not a writer exec must copy for it), so its exit is seen at once
// even while children it left behind still hold the pipe. The copy only ends
// when the pipe does (the log never refuses a write), so a full disk can never
// leave the pipe full and the server blocked on its output. done closes the
// write end and waits a little for the copy to finish.
func logPipe(path string) (w *os.File, done func(), err error) {
	lf, err := openLogFile(path, logRotateBytes)
	if err != nil {
		return nil, nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		_ = lf.Close()
		return nil, nil, err
	}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(lf, r)
		close(copied)
	}()
	return w, func() {
		_ = w.Close()
		select {
		case <-copied:
		case <-time.After(2 * time.Second): // a child still holds the pipe
		}
		_ = r.Close()
		_ = lf.Close()
	}, nil
}
