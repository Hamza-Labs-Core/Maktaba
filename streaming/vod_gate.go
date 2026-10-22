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
	// Ready: the front door's slots are up (its Bootstrap succeeded); before,
	// VOD encodes in software.
	Ready() bool
	// Room is the channels' cap of the kind (the settings' NVENCMax, or VTMax
	// for VideoToolbox; 0: none), what the channels need of it to stay as they
	// are (those on the live slot and those the hot standby keeps warm), and the
	// sessions the slots hold (their last reports).
	Room(ctx context.Context, vt bool) (limit, need, used int)
	// Sync sends the slots their budgets now, the channels' caps less what VOD
	// holds or is taking (vodGate.Held), and reads their reports back: a slot
	// applies its budget to its gates as it gets it. True only when the live
	// slot (and the standby, when it was sent its set) took theirs.
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

// vodGate is VOD's share of a hardware encoder's sessions
// (ffmpeg.SessionBatch). The TV's channels come first:
//
//   - VOD holds at most half the channels' cap, and never what the channels
//     on the live slot and those the hot standby keeps warm need (two each): a
//     transcode never turns the standby cold, and a channel that starts during
//     one still finds a session.
//   - With TV slots on (the channels in other processes, each with its own
//     gate): a transcode's sessions are reserved, the slots' budgets drop by
//     them (one Sync, every slot answering), and they are kept only as far as
//     the slots' fresh reports leave room: VOD's sessions and the live and
//     standby slots' then stay within the cap. A TV release's staging slot,
//     during a swap, adds its own (two a staged channel) on top.
//   - With TV slots off this process's gate counts the channels and VOD.
//
// What it cannot have is libx264's. A cap of 0 limits nothing (VideoToolbox's
// default).
type vodGate struct {
	gate *channel.EncoderGate
	vt   bool // VideoToolbox's: the settings' VTMax, not NVENCMax

	mu   sync.Mutex   // reservations, one at a time
	held atomic.Int64 // VOD's sessions, held or reserved
}

// Held is VOD's sessions, held or reserved (the slots' budgets leave them out).
func (g *vodGate) Held() int { return int(g.held.Load()) }

// Acquire is one session (AcquireN's).
func (g *vodGate) Acquire(ctx context.Context, owner uuid.UUID, _ time.Duration) (func(refused bool), bool) {
	if got := g.AcquireN(ctx, owner, 1); len(got) == 1 {
		return got[0], true
	}
	return nil, false
}

// AcquireN gives a transcode up to n sessions (its highest rungs').
func (g *vodGate) AcquireN(ctx context.Context, owner uuid.UUID, n int) []func(refused bool) {
	link := slotsLink()
	if link != nil && !link.Ready() {
		return nil
	}
	// reserve what the rules leave, under the lock: no two reservations look at
	// the same room
	g.mu.Lock()
	limit, need := g.gate.Max(), 0
	if link != nil {
		limit, need, _ = link.Room(ctx, g.vt)
	}
	k := n
	if limit > 0 {
		allowed := limit / 2
		if free := limit - need; free < allowed {
			allowed = free
		}
		if room := allowed - g.Held(); room < k {
			k = room
		}
	}
	if k <= 0 {
		g.mu.Unlock()
		return nil
	}
	g.held.Add(int64(k))
	g.mu.Unlock()

	// one sync for them all, outside the lock: the slots' budgets drop by them,
	// then what the slots hold beside all of VOD's must fit (else fewer, or none)
	if link != nil && limit > 0 {
		if !link.Sync(ctx) {
			g.held.Add(int64(-k))
			return nil
		}
		if _, _, used := link.Room(ctx, g.vt); used+g.Held() > limit {
			over := used + g.Held() - limit
			if over > k {
				over = k
			}
			g.held.Add(int64(-over))
			if k -= over; k == 0 {
				return nil
			}
		}
	}
	// taken from this process's gate (which, with TV slots off, counts the
	// channels too)
	var got []func(refused bool)
	for i := 0; i < k; i++ {
		release, ok := g.gate.Acquire(ctx, owner, 0)
		if !ok {
			break
		}
		var once sync.Once
		got = append(got, func(refused bool) {
			once.Do(func() {
				g.held.Add(-1)
				release(refused)
			})
		})
	}
	g.held.Add(int64(len(got) - k)) // the reservations the gate had no session for
	return got
}

// vodNVENC and vodVT are VOD's gates (buildGRPCServer), whose holds the slots'
// budgets leave out (buildHomeSlots).
var (
	vodNVENC = &vodGate{gate: channel.DefaultEncoderGate()}
	vodVT    = &vodGate{gate: channel.DefaultVTGate(), vt: true}
)

// slotBudget is the channels' caps for the slots: the settings' less what VOD
// holds (0 stays "no cap"). It never drops below what the channels need
// (AcquireN reserves only beyond it), so the floor of 1 is never reached
// with a cap of 2 or more.
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
