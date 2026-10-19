package channel

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// runtimeWriter writes channel_runtime rows in the order they were queued, on
// its own goroutine: the engine queues them under its lock (a station's row and
// the clear when it ends stay in order against a re-tune) but never waits for
// the database there. A stalled database delays the rows, not the engine; past
// maxRuntimeQueue queued writes the periodic refreshes are dropped (the next
// refresh rewrites them), never a start's row or a clear.
type runtimeWriter struct {
	mu       sync.Mutex
	queue    []rtOp
	inflight bool
	wake     chan struct{}
}

const maxRuntimeQueue = 1000

type rtOp struct {
	repo  Repo
	rt    Runtime
	clear bool
	id    uuid.UUID
}

func (e *Engine) runtimeWrites() *runtimeWriter {
	e.rtwOnce.Do(func() {
		e.rtw = &runtimeWriter{wake: make(chan struct{}, 1)}
		go e.rtw.run()
	})
	return e.rtw
}

func (w *runtimeWriter) push(op rtOp, droppable bool) {
	w.mu.Lock()
	if droppable && len(w.queue) >= maxRuntimeQueue {
		w.mu.Unlock()
		return
	}
	w.queue = append(w.queue, op)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// set queues a station's row (a start).
func (w *runtimeWriter) set(r Repo, rt Runtime) {
	w.push(rtOp{repo: r, rt: rt, id: rt.ChannelID}, false)
}

// sync queues the periodic refresh of a row.
func (w *runtimeWriter) sync(r Repo, rt Runtime) {
	w.push(rtOp{repo: r, rt: rt, id: rt.ChannelID}, true)
}

// clear queues the removal of a channel's row (its station ended).
func (w *runtimeWriter) clear(r Repo, id uuid.UUID) {
	w.push(rtOp{repo: r, clear: true, id: id}, false)
}

// idle reports nothing queued or being written.
func (w *runtimeWriter) idle() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.queue) == 0 && !w.inflight
}

func (w *runtimeWriter) run() {
	for range w.wake {
		for {
			w.mu.Lock()
			if len(w.queue) == 0 {
				w.mu.Unlock()
				break
			}
			op := w.queue[0]
			w.queue = w.queue[1:]
			w.inflight = true
			w.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
			if op.clear {
				_ = op.repo.ClearRuntime(ctx, op.id)
			} else {
				_ = op.repo.SetRuntime(ctx, op.rt)
			}
			cancel()
			w.mu.Lock()
			w.inflight = false
			w.mu.Unlock()
		}
	}
}

// FlushRuntime waits (up to d) until the queued channel_runtime writes are done.
func (e *Engine) FlushRuntime(d time.Duration) bool {
	w := e.runtimeWrites()
	deadline := time.Now().Add(d)
	for !w.idle() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}
