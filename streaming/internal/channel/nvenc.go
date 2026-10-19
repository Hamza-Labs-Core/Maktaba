package channel

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DefaultNVENCMaxSessions is how many NVENC sessions all channels may hold
// at once (a GeForce card opens only a few; the dizqueTV patch used 6).
const DefaultNVENCMaxSessions = 6

// nvencRefusedPause: after the driver refused a session outright, no new
// NVENC block is tried for this long (the card most likely has none left,
// e.g. another program holds them).
const nvencRefusedPause = 30 * time.Second

// EncoderGate is a process-wide hardware-encoder session semaphore (one
// for NVENC, one for VideoToolbox). Every block
// ffmpeg that encodes with h264_nvenc holds one slot from start to exit, so
// the next block that a station pre-starts ahead of a boundary counts too
// (for those few seconds the channel really has two sessions open). A
// block that finds the gate full is encoded by libx264 — that block only;
// the station's next block asks again.
type EncoderGate struct {
	mu           sync.Mutex
	max          int
	active       int
	held         map[uuid.UUID]int // station → slots it holds
	refusedUntil time.Time
	wake         chan struct{} // closed and replaced on every release
	now          func() time.Time
}

// NewEncoderGate builds a gate with max slots (<= 0: no cap).
func NewEncoderGate(max int) *EncoderGate {
	return &EncoderGate{max: max, held: map[uuid.UUID]int{}, wake: make(chan struct{}), now: time.Now}
}

// SetMax changes the cap (a settings change). Slots already held over a
// lower cap are kept until released.
// max <= 0 removes the cap.
func (g *EncoderGate) SetMax(max int) {
	g.mu.Lock()
	g.max = max
	g.signalLocked()
	g.mu.Unlock()
}

// Max reports the cap.
func (g *EncoderGate) Max() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}

// Active reports the slots held.
func (g *EncoderGate) Active() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
}

// Acquire takes a slot for station owner. When the gate is full and the
// owner already holds a slot (its current block, which ends at the
// boundary the new block was pre-started for), it waits up to `wait` for a
// slot to free — normally that very block's. A station holding none gets
// an answer at once. ok=false: encode this block in software. The release
// func must be called once the block's ffmpeg has exited; refused=true
// when the driver refused the session (pauses NVENC for everyone).
func (g *EncoderGate) Acquire(ctx context.Context, owner uuid.UUID, wait time.Duration) (release func(refused bool), ok bool) {
	var deadline <-chan time.Time
	for {
		g.mu.Lock()
		if g.now().Before(g.refusedUntil) {
			g.mu.Unlock()
			return nil, false
		}
		if g.max <= 0 || g.active < g.max {
			g.active++
			g.held[owner]++
			g.mu.Unlock()
			return g.releaser(owner), true
		}
		if g.held[owner] == 0 || wait <= 0 {
			g.mu.Unlock()
			return nil, false
		}
		wake := g.wake
		g.mu.Unlock()
		if deadline == nil {
			t := time.NewTimer(wait)
			defer t.Stop()
			deadline = t.C
		}
		select {
		case <-wake:
		case <-deadline:
			return nil, false
		case <-ctx.Done():
			return nil, false
		}
	}
}

func (g *EncoderGate) releaser(owner uuid.UUID) func(bool) {
	var once sync.Once
	return func(refused bool) {
		once.Do(func() {
			g.mu.Lock()
			g.active--
			if g.held[owner]--; g.held[owner] <= 0 {
				delete(g.held, owner)
			}
			if refused {
				g.refusedUntil = g.now().Add(nvencRefusedPause)
			}
			g.signalLocked()
			g.mu.Unlock()
		})
	}
}

func (g *EncoderGate) signalLocked() {
	close(g.wake)
	g.wake = make(chan struct{})
}

// defaultGate / defaultVTGate are the gates stations share when their
// config names none: NVENC capped at DefaultNVENCMaxSessions, VideoToolbox
// uncapped unless the settings say otherwise.
var (
	defaultGate   = NewEncoderGate(DefaultNVENCMaxSessions)
	defaultVTGate = NewEncoderGate(0)
)

// DefaultEncoderGate is the process-wide NVENC gate.
func DefaultEncoderGate() *EncoderGate { return defaultGate }

// DefaultVTGate is the process-wide VideoToolbox gate.
func DefaultVTGate() *EncoderGate { return defaultVTGate }

// Free reports the slots left (-1: no cap).
func (g *EncoderGate) Free() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.max <= 0 {
		return -1
	}
	if f := g.max - g.active; f > 0 {
		return f
	}
	return 0
}
