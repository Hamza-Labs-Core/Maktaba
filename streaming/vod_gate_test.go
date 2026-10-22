package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// take asks g for n sessions and returns the releases of those it got.
func take(g *vodGate, n int) []func(bool) {
	var got []func(bool)
	for i := 0; i < n; i++ {
		if r, ok := g.Acquire(context.Background(), uuid.New(), 0); ok {
			got = append(got, r)
		}
	}
	return got
}

func giveAll(rs []func(bool)) {
	for _, r := range rs {
		r(false)
	}
}

// VOD's NVENC sessions and the channels' stay within the channels' cap, and VOD
// holds at most half of it: a channel that starts while a video transcodes still
// finds a session.
func TestVODGateLeavesTheChannelsTheirSessions(t *testing.T) {
	prev := tvSessions.Load()
	t.Cleanup(func() {
		if prev != nil {
			tvSessions.Store(prev)
		} else {
			tvSessions.Store(func(context.Context) (int, int) { return 0, 0 })
		}
	})
	slots, nvencMax := 0, 6
	tvSessions.Store(func(context.Context) (int, int) { return slots, nvencMax })
	gate := channel.NewEncoderGate(6)
	g := &vodGate{gate: gate}

	got := take(g, 4)
	if len(got) != 3 || gate.Active() != 3 {
		t.Fatalf("VOD alone got %d sessions (gate %d), want 3: half the cap", len(got), gate.Active())
	}
	giveAll(got)

	// the TV slots hold 4 of the 6
	slots = 4
	if got = take(g, 3); len(got) != 2 {
		t.Errorf("beside 4 slot sessions VOD got %d, want 2", len(got))
	}
	giveAll(got)

	// the channels in this process (TV slots off) hold 5 of the gate's 6
	slots = 0
	ch := uuid.New()
	var channels []func(bool)
	for i := 0; i < 5; i++ {
		r, ok := gate.Acquire(context.Background(), ch, 0)
		if !ok {
			t.Fatal("the channels' gate refused")
		}
		channels = append(channels, r)
	}
	if got = take(g, 3); len(got) != 1 {
		t.Errorf("beside 5 channel sessions VOD got %d, want 1", len(got))
	}
	giveAll(got)
	giveAll(channels)

	// the settings' cap (2) wins over the gate's default
	nvencMax = 2
	if got = take(g, 3); len(got) != 1 {
		t.Errorf("with a cap of 2 VOD got %d, want 1", len(got))
	}
	giveAll(got)
	if gate.Active() != 0 || g.held != 0 {
		t.Errorf("after the releases the gate holds %d, VOD %d", gate.Active(), g.held)
	}

	// no cap at all (VideoToolbox's default): nothing limited
	nvencMax = 0
	free := &vodGate{gate: channel.NewEncoderGate(0)}
	if got = take(free, 8); len(got) != 8 {
		t.Errorf("without a cap VOD got %d of 8", len(got))
	}
}
