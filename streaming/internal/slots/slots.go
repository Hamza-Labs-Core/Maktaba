// Package slots implements Story 8.10 — concurrency caps, the
// per-host transcode quota, and the queue admission decision.
//
// The slot count defaults to (num_cores / 4); operators override with
// `transcode.max_concurrent`. New transcode-required sessions over
// the cap fall back to direct-degraded if possible, otherwise queue
// (when accept_queue=true) or 503-ish RESOURCE_EXHAUSTED.
package slots

import (
	"errors"
	"runtime"
	"sync"
	"time"
)

// Decision is what the allocator returned for an OpenSession.
type Decision string

const (
	// DecisionAdmit means start the FFmpeg now.
	DecisionAdmit Decision = "admit"
	// DecisionDirectCap means open as direct-degraded (capped at 720p).
	DecisionDirectCap Decision = "direct-cap"
	// DecisionQueue means accept_queue=true and we should record
	// state='queued' until a slot frees.
	DecisionQueue Decision = "queue"
	// DecisionExhausted means RESOURCE_EXHAUSTED with no fallback.
	DecisionExhausted Decision = "exhausted"
)

// Errors.
var (
	ErrExhausted = errors.New("transcode slots exhausted; no fallback acceptable")
	ErrNotHeld   = errors.New("slot release without prior acquire")
)

// AllocatorConfig governs slot accounting.
type AllocatorConfig struct {
	// MaxTranscode is the hard cap per host. 0 → auto-derive
	// runtime.NumCPU() / 4 (min 1).
	MaxTranscode int
	// QueueDepth is how many sessions can wait in state='queued'
	// before we surface ErrExhausted. 0 disables the queue
	// entirely (only direct-cap or exhausted are returned).
	QueueDepth int
}

// Effective returns the resolved MaxTranscode (auto-derive if 0).
func (c AllocatorConfig) Effective() int {
	if c.MaxTranscode > 0 {
		return c.MaxTranscode
	}
	n := runtime.NumCPU() / 4
	if n < 1 {
		n = 1
	}
	return n
}

// Request describes the OpenSession ask the allocator is reasoning
// about. Used for direct-cap eligibility.
type Request struct {
	// CanDirectCap is true when the source can be served as
	// direct-degraded at 720p without any FFmpeg work. Story 8.10
	// AC-2: only fall back to direct-cap if the source actually
	// supports it (no transcoding at fallback time).
	CanDirectCap bool
	AcceptQueue  bool
}

// Allocator owns the slot count + queue depth.
type Allocator struct {
	cfg    AllocatorConfig
	mu     sync.Mutex
	used   int
	queued int
	holds  map[string]struct{}
}

// NewAllocator returns a slot allocator.
func NewAllocator(cfg AllocatorConfig) *Allocator {
	return &Allocator{cfg: cfg, holds: map[string]struct{}{}}
}

// MaxConcurrent reports the cap (auto-derived if 0).
func (a *Allocator) MaxConcurrent() int { return a.cfg.Effective() }

// Used reports the in-flight count.
func (a *Allocator) Used() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.used
}

// QueueLength reports how many sessions are sitting in queue state.
func (a *Allocator) QueueLength() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.queued
}

// Decide answers an OpenSession admission. Caller passes the request
// shape; we return the verdict but only update internal counters when
// the verdict is Admit (we hand the caller a holdID to release on
// session close) or Queue (caller must call Dequeue when the session
// either is admitted later or abandons).
func (a *Allocator) Decide(req Request) (Decision, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	limit := a.cfg.Effective()
	if a.used < limit {
		holdID := newHoldID()
		a.holds[holdID] = struct{}{}
		a.used++
		return DecisionAdmit, holdID
	}
	if req.CanDirectCap {
		return DecisionDirectCap, ""
	}
	if req.AcceptQueue && a.queued < a.cfg.QueueDepth {
		a.queued++
		return DecisionQueue, ""
	}
	return DecisionExhausted, ""
}

// Release frees a previously admitted slot. Idempotent on unknown
// holds (returns ErrNotHeld but won't crash).
func (a *Allocator) Release(holdID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.holds[holdID]; !ok {
		return ErrNotHeld
	}
	delete(a.holds, holdID)
	if a.used > 0 {
		a.used--
	}
	return nil
}

// Dequeue marks one queued session done waiting (admitted or aborted).
func (a *Allocator) Dequeue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.queued > 0 {
		a.queued--
	}
}

// PromoteFromQueue tries to grab a slot for a queued session. Returns
// the new hold id and true on success, false when the cap is still
// hit. The caller is expected to retry on the next slot-free event.
func (a *Allocator) PromoteFromQueue() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.used >= a.cfg.Effective() {
		return "", false
	}
	if a.queued > 0 {
		a.queued--
	}
	holdID := newHoldID()
	a.holds[holdID] = struct{}{}
	a.used++
	return holdID, true
}

// holdID generation is just a monotonic counter — we don't need it to
// be unforgeable, only locally unique within this process lifetime.
var (
	holdSeq uint64
	holdMu  sync.Mutex
)

func newHoldID() string {
	holdMu.Lock()
	defer holdMu.Unlock()
	holdSeq++
	return formatHold(holdSeq)
}

func formatHold(n uint64) string {
	return time.Now().UTC().Format("20060102T150405") + "-" + uintToHex(n)
}

func uintToHex(n uint64) string {
	const hex = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 16)
	for n > 0 {
		buf = append(buf, hex[n&0xf])
		n >>= 4
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
