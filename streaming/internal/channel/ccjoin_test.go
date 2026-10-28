package channel

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/verify"
)

// muxerTS is what a freshly started muxer writes: every PID counts from 0 — PAT,
// PMT, video (with PCR in an adaptation field), audio, a PCR-only packet (no
// payload: the counter does not count it) and null packets.
func muxerTS(rounds int) []byte {
	cc := map[int]int{}
	var b []byte
	pkt := func(pid int, pusi, payload bool, af []byte) {
		p := make([]byte, tsPacket)
		p[0], p[1], p[2] = 0x47, byte(pid>>8&0x1f), byte(pid)
		if pusi {
			p[1] |= 0x40
		}
		afc := byte(0)
		off := 4
		if af != nil {
			afc |= 0x20
			p[4] = byte(len(af))
			copy(p[5:], af)
			off = 5 + len(af)
		}
		if payload {
			afc |= 0x10
		}
		p[3] = afc | byte(cc[pid]&0x0f)
		if payload && pid != 0x1fff {
			cc[pid]++
		}
		for i := off; i < tsPacket; i++ {
			p[i] = 0xff
		}
		b = append(b, p...)
	}
	pcr := []byte{0x10, 0, 0, 0, 0, 0x7e, 0}
	for r := 0; r < rounds; r++ {
		pkt(0, true, true, nil)
		pkt(0x1000, true, true, nil)
		pkt(0x100, true, true, pcr)
		pkt(0x100, false, true, nil)
		pkt(0x100, false, false, pcr) // PCR only
		pkt(0x100, false, true, nil)
		pkt(0x101, true, true, nil)
		pkt(0x101, false, true, nil)
		pkt(0x1fff, false, true, nil)
	}
	return b
}

// A muxer restarted inside a station starts its counters afresh; the hub carries
// them on, so a client of live.ts sees one continuous stream.
func TestHubKeepsContinuityAcrossMuxers(t *testing.T) {
	raw := append(muxerTS(5), muxerTS(5)...)
	if n := verify.AnalyzeTS(raw, verify.Options{}).CCErrors; n != 4 {
		t.Fatalf("two muxers back to back: %d continuity errors, want 4 (PAT, PMT, video, audio)", n)
	}
	h := NewHub(64)
	sub, cancel := h.Subscribe()
	h.pump(bytes.NewReader(muxerTS(5)))
	h.pump(bytes.NewReader(muxerTS(5)))
	cancel()
	var got []byte
	for c := range sub {
		got = append(got, c...)
	}
	if len(got) != len(raw) {
		t.Fatalf("%d bytes through the hub, want %d", len(got), len(raw))
	}
	if n := verify.AnalyzeTS(got, verify.Options{}).CCErrors; n != 0 {
		t.Errorf("live.ts across a muxer restart: %d continuity errors", n)
	}
}

// TV change #56 (2026-10-28): the staging slot stopped a station under a live.ts read
// (its rolling window shrank) and StreamChannelTS carried the connection on with the
// restarted station, whose counters started afresh: "live.ts: 4 continuity errors".
func TestStreamChannelTSKeepsContinuityAcrossStationRestart(t *testing.T) {
	clk := newClock(tm("2026-10-28T17:41:00Z"))
	e, _, sc := newTestEngine(t, clk, 0)
	id := uuid.New()
	var mu sync.Mutex
	var got bytes.Buffer
	w := writerFunc(func(b []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return got.Write(b) })
	n := func() int { mu.Lock(); defer mu.Unlock(); return got.Len() }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = e.StreamChannelTS(ctx, id, w); close(done) }()
	defer func() { cancel(); <-done }()

	live := func() *fakePipeline {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if sc.last != nil && sc.last.hub.Clients() == 1 {
			return sc.last
		}
		return nil
	}
	waitFor(t, func() bool { return live() != nil })
	first := live()
	a := muxerTS(5)
	first.hub.Publish(a)
	waitFor(t, func() bool { return n() == len(a) })

	// the station stops under the read; the connection carries on with a new one
	e.StopChannel(id)
	waitFor(t, func() bool { p := live(); return p != nil && p != first })
	second := live()
	b := muxerTS(5)
	// a second client on the restarted station shares its chunks: they stay as sent
	other, cancelOther := second.hub.Subscribe()
	defer cancelOther()
	second.hub.Publish(b)
	waitFor(t, func() bool { return n() == len(a)+len(b) })
	if c := <-other; !bytes.Equal(c, b) {
		t.Error("the shared chunk was rewritten for another client")
	}

	mu.Lock()
	out := append([]byte(nil), got.Bytes()...)
	mu.Unlock()
	if n := verify.AnalyzeTS(out, verify.Options{}).CCErrors; n != 0 {
		t.Errorf("live.ts across a station restart: %d continuity errors", n)
	}
}

// The join keeps each source's own counting: a fault inside a source still shows.
func TestCCJoinKeepsFaultsInsideASource(t *testing.T) {
	src := muxerTS(5)
	v := (2*9 + 2) * tsPacket // the third round's first video packet
	src[v+3] = src[v+3]&0xf0 | (src[v+3]+3)&0x0f
	j := newCCJoin()
	out := j.apply(muxerTS(5), false)
	j.resume()
	out = append(out, j.apply(src, false)...)
	if n := verify.AnalyzeTS(out, verify.Options{}).CCErrors; n != 2 { // out of step and back
		t.Errorf("%d continuity errors, want the two inside the second source", n)
	}
}
