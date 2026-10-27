package handlers

import (
	"errors"
	"sync"
	"time"
)

// defaultReadStall is how long one read of a direct-play source may take before the
// copy gives up on it: a NAS share that stopped answering (a sleeping disk wakes in
// seconds; a dropped SMB session never answers) holds a read forever, and the player
// would wait on a response that never ends.
const defaultReadStall = 30 * time.Second

// errReadStalled: a read of the source took longer than the stall limit.
var errReadStalled = errors.New("the read did not return within the stall limit")

// stallFile is a FileReader whose reads give up after limit (errReadStalled). The
// stalled read runs on: an os.File read cannot be interrupted, and closing the file
// under it would wait for it, so Close then leaves the file to that read, which
// closes it when (if ever) it returns.
type stallFile struct {
	FileReader
	limit time.Duration

	mu          sync.Mutex
	inflight    bool // a read runs (one that stalled, once the copy gave up)
	closeWanted bool // Close came during it: the read closes the file
	stalled     bool
}

func watchStalls(f FileReader, limit time.Duration) *stallFile {
	if limit <= 0 {
		limit = defaultReadStall
	}
	return &stallFile{FileReader: f, limit: limit}
}

type readResult struct {
	n   int
	err error
}

func (s *stallFile) Read(p []byte) (int, error) {
	s.mu.Lock()
	if s.stalled {
		s.mu.Unlock()
		return 0, errReadStalled
	}
	s.inflight = true
	s.mu.Unlock()
	done := make(chan readResult, 1)
	go func() {
		n, err := s.FileReader.Read(p)
		s.mu.Lock()
		s.inflight = false
		closeNow := s.closeWanted
		s.mu.Unlock()
		if closeNow {
			_ = s.FileReader.Close()
		}
		done <- readResult{n, err}
	}()
	t := time.NewTimer(s.limit)
	defer t.Stop()
	select {
	case r := <-done:
		return r.n, r.err
	case <-t.C:
		s.mu.Lock()
		s.stalled = true
		s.mu.Unlock()
		return 0, errReadStalled
	}
}

// Close closes the file now, or (a read still running) once that read returns.
func (s *stallFile) Close() error {
	s.mu.Lock()
	if s.inflight {
		s.closeWanted = true
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.FileReader.Close()
}
