package catalog

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Watch tells whether the home's database answers this node: a ping every Every, each
// bounded to Timeout, and any call that failed on it (Failed). While it is down a
// request reads the catalog at once rather than first waiting on a dead connection (a
// powered-off home leaves a call hanging, it does not fail).
type Watch struct {
	DB      *sql.DB
	Every   time.Duration // 0: 5 s
	Timeout time.Duration // 0: 2 s
	Logger  *slog.Logger

	down atomic.Bool
	mu   sync.Mutex
	back chan struct{} // closed when the database answers again
}

// Down reports whether the home's database did not answer the last ping or call.
func (w *Watch) Down() bool { return w != nil && w.down.Load() }

// Failed records a call that failed on the home's database: down until the next ping
// answers.
func (w *Watch) Failed(err error) {
	if w == nil || err == nil {
		return
	}
	w.set(true, err)
}

func (w *Watch) set(down bool, err error) {
	if w.down.Swap(down) == down {
		return
	}
	if down {
		if w.Logger != nil {
			w.Logger.Warn("node: the home's database does not answer; playback reads this machine's catalog",
				"err", err, "event", "node_home_db_down")
		}
		return
	}
	if w.Logger != nil {
		w.Logger.Info("node: the home's database answers again", "event", "node_home_db_back")
	}
	w.mu.Lock()
	if w.back != nil {
		close(w.back)
		w.back = nil
	}
	w.mu.Unlock()
}

// backCh is closed the next time the database answers after being down.
func (w *Watch) backCh() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.back == nil {
		w.back = make(chan struct{})
	}
	return w.back
}

// Check pings once.
func (w *Watch) Check(ctx context.Context) {
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	err := w.DB.PingContext(pctx)
	cancel()
	if ctx.Err() != nil {
		return
	}
	w.set(err != nil, err)
}

// Run pings until ctx ends.
func (w *Watch) Run(ctx context.Context) {
	every := w.Every
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		w.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
