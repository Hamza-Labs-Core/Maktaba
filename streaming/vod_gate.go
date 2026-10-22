package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// tvSlotsLink is the front door's view of the TV slots that VOD's gates count
// with (buildHomeSlots wires frontdoorSlotsLink).
type tvSlotsLink interface {
	// Sessions are the hardware encoder sessions the slots hold (their last
	// reports) and the channels' NVENC and VideoToolbox caps from the settings.
	Sessions(ctx context.Context) (used, nvencMax, vtMax int)
	// Sync sends the slots their budgets now, the channels' caps less what VOD
	// holds or is taking (vodGate.Held), and reads their reports back: a slot
	// applies its budget to its gate as it gets it. False: not done in time.
	Sync(ctx context.Context) bool
}

// tvSlots holds the link once the slots are wired; with TV slots off there is
// none: the channels run in this process and its gate counts them.
var tvSlots atomic.Value // linkBox

type linkBox struct{ link tvSlotsLink }

func slotsLink() tvSlotsLink {
	b, _ := tvSlots.Load().(linkBox)
	return b.link
}

// vodGate is VOD's share of a hardware encoder's sessions (ffmpeg.SessionGate).
// The TV's channels come first: VOD's sessions and the channels' together stay
// within the channels' cap (the settings' NVENC sessions, or VideoToolbox's
// for that gate), and VOD holds at most half of it, so a channel that starts
// while a video transcodes still finds a session. With TV slots on, the
// channels run in other processes, each with its own gate: VOD reserves a
// session, the slots' budgets drop by it at once (Sync), and only when their
// fresh reports and this process's gate leave room is the session taken. One
// Acquire at a time, so the check and the take are one step. A cap of 0 limits
// nothing (VideoToolbox's default).
type vodGate struct {
	gate *channel.EncoderGate
	vt   bool // VideoToolbox's: capped by the settings' VTMax, not NVENCMax

	mu   sync.Mutex
	held atomic.Int64 // VOD's sessions, held or being taken
}

// Held is VOD's sessions, held or being taken (the slots' budgets leave them out).
func (g *vodGate) Held() int { return int(g.held.Load()) }

func (g *vodGate) Acquire(ctx context.Context, owner uuid.UUID, wait time.Duration) (func(refused bool), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	link := slotsLink()
	limit := g.gate.Max()
	if link != nil {
		_, nvencMax, vtMax := link.Sessions(ctx)
		limit = nvencMax
		if g.vt {
			limit = vtMax
		}
	}
	if limit > 0 && g.Held() >= limit/2 {
		return nil, false
	}
	g.held.Add(1) // reserved: the slots' budgets leave it out from the next Sync
	if link != nil && limit > 0 {
		// no fresh word from the slots, or no room beside them: software
		if !link.Sync(ctx) {
			g.held.Add(-1)
			return nil, false
		}
		if used, _, _ := link.Sessions(ctx); g.gate.Active()+used+1 > limit {
			g.held.Add(-1)
			return nil, false
		}
	}
	release, ok := g.gate.Acquire(ctx, owner, wait)
	if !ok {
		g.held.Add(-1)
		return nil, false
	}
	var once sync.Once
	return func(refused bool) {
		once.Do(func() {
			g.held.Add(-1)
			release(refused)
		})
	}, true
}

// vodNVENC and vodVT are VOD's gates (buildGRPCServer), whose holds the slots'
// budgets leave out (buildHomeSlots).
var (
	vodNVENC = &vodGate{gate: channel.DefaultEncoderGate()}
	vodVT    = &vodGate{gate: channel.DefaultVTGate(), vt: true}
)

// slotBudget is the channels' caps for the slots: the settings' less what VOD
// holds (0 stays "no cap").
func slotBudget(nvencMax, vtMax int) (int, int) {
	if nvencMax > 0 {
		nvencMax -= vodNVENC.Held()
		if nvencMax < 1 {
			nvencMax = 1
		}
	}
	if vtMax > 0 {
		vtMax -= vodVT.Held()
		if vtMax < 1 {
			vtMax = 1
		}
	}
	return nvencMax, vtMax
}
