package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// tvSessions reports the hardware encoder sessions the TV slots hold (the
// front door's last view of them) and the channels' NVENC cap from the
// settings; nil until the slots are wired (buildHomeSlots), and with TV slots
// off, when the channels run in this process and its gate counts them.
var tvSessions atomic.Value // func(ctx context.Context) (used, nvencMax int)

// vodGate is VOD's share of a hardware encoder's sessions (ffmpeg.SessionGate).
// The TV's channels come first: VOD's sessions and the channels' together stay
// within the channels' cap (this process's gate, which holds the channels'
// sessions with TV slots off, plus what the TV slots report), and VOD holds at
// most half of that cap, so a channel that starts while a video transcodes
// still finds a session. A gate without a cap (VideoToolbox by default) limits
// nothing.
type vodGate struct {
	gate *channel.EncoderGate
	mu   sync.Mutex
	held int
}

func (g *vodGate) Acquire(ctx context.Context, owner uuid.UUID, wait time.Duration) (func(refused bool), bool) {
	limit, slots := g.gate.Max(), 0
	if f, ok := tvSessions.Load().(func(context.Context) (int, int)); ok {
		used, nvencMax := f(ctx)
		slots = used
		if nvencMax > 0 {
			limit = nvencMax
		}
	}
	if limit > 0 {
		g.mu.Lock()
		held := g.held
		g.mu.Unlock()
		if held >= limit/2 || g.gate.Active()+slots >= limit {
			return nil, false
		}
	}
	release, ok := g.gate.Acquire(ctx, owner, wait)
	if !ok {
		return nil, false
	}
	g.mu.Lock()
	g.held++
	g.mu.Unlock()
	var once sync.Once
	return func(refused bool) {
		once.Do(func() {
			g.mu.Lock()
			g.held--
			g.mu.Unlock()
			release(refused)
		})
	}, true
}
