package channel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// growingRepo serves a schedule that the "scheduler" keeps extending:
// every read may append more blocks, like channel_programs top-ups.
type growingRepo struct {
	blocks []ProgramBlock
	reads  int
	extend func(r *growingRepo)
}

func (g *growingRepo) ProgramsFrom(_ context.Context, _ uuid.UUID, from time.Time, limit int) ([]ProgramBlock, error) {
	g.reads++
	if g.extend != nil {
		g.extend(g)
	}
	var out []ProgramBlock
	for _, b := range g.blocks {
		if b.EndAt.After(from) {
			out = append(out, b)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// fakePlayer advances the fake clock as if each segment aired; fail
// marks segments (by predicate) that stop after `failAfter`.
type fakePlayer struct {
	clk       *fakeClock
	played    []Segment
	stopAfter int
	cancel    context.CancelFunc
	fail      func(Segment) bool
	failAfter time.Duration
}

func (p *fakePlayer) Play(_ context.Context, seg Segment) (bool, error) {
	p.played = append(p.played, seg)
	if len(p.played) >= p.stopAfter {
		p.cancel()
	}
	start := seg.Start
	if n := p.clk.now(); n.After(start) {
		start = n
	}
	if p.fail != nil && p.fail(seg) {
		p.clk.set(start.Add(p.failAfter))
		return false, errors.New("boom")
	}
	p.clk.set(seg.End)
	return true, nil
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.t) {
		c.t = t
	}
}

func runFeeder(t *testing.T, repo ScheduleSource, clk *fakeClock, n int, fail func(Segment) bool, failAfter time.Duration) []Segment {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fakePlayer{clk: clk, stopAfter: n, cancel: cancel, fail: fail, failAfter: failAfter}
	f := &Feeder{Source: repo, ChannelID: uuid.New(), Player: p, Now: clk.now,
		Sleep: func(_ context.Context, d time.Duration) { clk.advance(d) }}
	done := make(chan struct{})
	go func() { f.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("feeder did not stop")
	}
	return p.played
}

func blk(seq int64, kind string, start time.Time, d time.Duration) ProgramBlock {
	b := ProgramBlock{Seq: seq, Kind: kind, StartAt: start, EndAt: start.Add(d), SourceDurationMS: int(d / time.Millisecond)}
	switch kind {
	case KindProgram, KindFiller, KindBumper:
		b.Path = "/media/" + kind + ".mp4"
		b.VideoID = uuid.New()
	case KindLive:
		b.SourceURL = "https://www.youtube.com/watch?v=live"
	}
	return b
}

// assertContiguous checks the segments tile the timeline with no gaps.
func assertContiguous(t *testing.T, segs []Segment) {
	t.Helper()
	for i := 1; i < len(segs); i++ {
		if !segs[i].Start.Equal(segs[i-1].End) {
			t.Fatalf("gap/overlap between segment %d (end %v) and %d (start %v)",
				i-1, segs[i-1].End, i, segs[i].Start)
		}
	}
}

func TestFeeder_ReFeedsForeverAsScheduleGrows(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	repo := &growingRepo{}
	// The scheduler keeps a ~1 h horizon of 10-minute programs.
	repo.extend = func(r *growingRepo) {
		for len(r.blocks) == 0 || r.blocks[len(r.blocks)-1].EndAt.Before(clk.now().Add(time.Hour)) {
			start := t0
			if n := len(r.blocks); n > 0 {
				start = r.blocks[n-1].EndAt
			}
			r.blocks = append(r.blocks, blk(int64(len(r.blocks)+1), KindProgram, start, 10*time.Minute))
		}
	}
	segs := runFeeder(t, repo, clk, 200, nil, 0)
	if len(segs) != 200 {
		t.Fatalf("played %d segments, want 200 (feeder must never stop)", len(segs))
	}
	assertContiguous(t, segs)
	for i, s := range segs {
		if s.Kind != KindProgram || s.Synthetic || s.Block.Seq != int64(i+1) {
			t.Fatalf("segment %d = %+v, want program seq %d", i, s, i+1)
		}
	}
	if repo.reads < 200 {
		t.Errorf("schedule should be re-read at every boundary, reads=%d", repo.reads)
	}
	if got := segs[199].End.Sub(t0); got != 2000*time.Minute {
		t.Errorf("timeline advanced %v, want exactly 2000m", got)
	}
}

func TestFeeder_JoinMidBlockSeeksWallClock(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0.Add(7*time.Minute + 30*time.Second))
	b1 := blk(1, KindProgram, t0, 10*time.Minute)
	b1.SourceOffsetMS = 60_000 // block plays the source from 1:00
	repo := &growingRepo{blocks: []ProgramBlock{b1, blk(2, KindFiller, t0.Add(10*time.Minute), time.Minute)}}
	segs := runFeeder(t, repo, clk, 2, nil, 0)
	if segs[0].SeekMS != 60_000+450_000 {
		t.Errorf("join seek = %d ms, want 510000", segs[0].SeekMS)
	}
	if !segs[0].End.Equal(b1.EndAt) || segs[1].Kind != KindFiller || segs[1].SeekMS != 0 {
		t.Errorf("unexpected: %+v / %+v", segs[0], segs[1])
	}
}

func TestFeeder_SlatesKeepTime(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	missing := blk(2, KindProgram, t0.Add(5*time.Minute), 5*time.Minute)
	missing.Path = "" // video deleted
	repo := &growingRepo{blocks: []ProgramBlock{
		blk(1, KindProgram, t0, 5*time.Minute),
		missing,
		blk(3, KindSlate, t0.Add(10*time.Minute), 2*time.Minute),
		// gap 12m → 20m
		blk(4, KindBumper, t0.Add(20*time.Minute), 30*time.Second),
	}}
	segs := runFeeder(t, repo, clk, 6, nil, 0)
	assertContiguous(t, segs)
	want := []struct {
		kind  string
		synth bool
		end   time.Duration
	}{
		{KindProgram, false, 5 * time.Minute},
		{KindSlate, false, 10 * time.Minute}, // unresolved file → slate, same span
		{KindSlate, false, 12 * time.Minute}, // scheduled slate
		{KindSlate, true, 17 * time.Minute},  // gap slate (capped at 5 min, schedule re-read)
		{KindSlate, true, 20 * time.Minute},  // rest of the gap
		{KindBumper, false, 20*time.Minute + 30*time.Second},
	}
	for i, w := range want {
		s := segs[i]
		if s.Kind != w.kind || s.Synthetic != w.synth || !s.End.Equal(t0.Add(w.end)) {
			t.Errorf("segment %d = kind %s synth %v end %v; want %s %v %v", i, s.Kind, s.Synthetic,
				s.End.Sub(t0), w.kind, w.synth, w.end)
		}
	}
}

func TestFeeder_FailedBlockSlatesRemainder(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	repo := &growingRepo{blocks: []ProgramBlock{
		blk(1, KindProgram, t0, 10*time.Minute),
		blk(2, KindProgram, t0.Add(10*time.Minute), 10*time.Minute),
	}}
	segs := runFeeder(t, repo, clk, 3, func(s Segment) bool { return s.Block.Seq == 1 && s.Kind == KindProgram },
		3*time.Minute)
	if segs[1].Kind != KindSlate || !segs[1].Synthetic || !segs[1].Start.Equal(t0.Add(3*time.Minute)) ||
		!segs[1].End.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("failure fill = %+v", segs[1])
	}
	if segs[2].Block.Seq != 2 || !segs[2].Start.Equal(t0.Add(10*time.Minute)) {
		t.Errorf("next block must start on schedule: %+v", segs[2])
	}
}

func TestFeeder_LiveBlockRetriesAfterFailure(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	repo := &growingRepo{blocks: []ProgramBlock{
		blk(1, KindLive, t0, 2*time.Minute),
		blk(2, KindProgram, t0.Add(2*time.Minute), 10*time.Minute),
	}}
	attempts := 0
	segs := runFeeder(t, repo, clk, 6, func(s Segment) bool {
		if s.Kind == KindLive {
			attempts++
			return attempts == 1 // first try fails after 10 s, retry works
		}
		return false
	}, 10*time.Second)
	if segs[0].Kind != KindLive || segs[0].SeekMS != 0 {
		t.Fatalf("first = %+v", segs[0])
	}
	if segs[1].Kind != KindSlate || segs[1].Message != MsgLiveUnavailable || segs[1].Duration() != 30*time.Second {
		t.Errorf("live failure → 30 s unavailable slate, got %+v", segs[1])
	}
	if segs[2].Kind != KindLive || !segs[2].Start.Equal(t0.Add(40*time.Second)) || !segs[2].End.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("live retried for the rest of its block, got %+v", segs[2])
	}
	if segs[3].Kind != KindProgram || segs[3].Block.Seq != 2 {
		t.Errorf("then the next program, got %+v", segs[3])
	}
}

func TestFeeder_EmptyScheduleSlatesForever(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	segs := runFeeder(t, &growingRepo{}, clk, 10, nil, 0)
	assertContiguous(t, segs)
	for _, s := range segs {
		if s.Kind != KindSlate || s.Duration() != emptySlate {
			t.Fatalf("empty schedule should slate in %v chunks: %+v", emptySlate, s)
		}
	}
}

func TestPlanNext_Kinds(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	live := blk(1, KindLive, t0, time.Hour)
	if s := PlanNext([]ProgramBlock{live}, t0.Add(5*time.Minute)); s.Kind != KindLive || s.SeekMS != 0 {
		t.Errorf("live: %+v", s)
	}
	live.SourceURL = ""
	if s := PlanNext([]ProgramBlock{live}, t0); s.Kind != KindSlate || s.Message != MsgLiveUnavailable {
		t.Errorf("live without url: %+v", s)
	}
	// A block starting within a second is started now at its offset.
	b := blk(2, KindProgram, t0.Add(500*time.Millisecond), time.Minute)
	b.SourceOffsetMS = 1234
	if s := PlanNext([]ProgramBlock{b}, t0); s.Kind != KindProgram || s.SeekMS != 1234 || !s.Start.Equal(t0) {
		t.Errorf("early start: %+v", s)
	}
	// A block with < 250 ms left is skipped.
	tiny := blk(3, KindProgram, t0.Add(-time.Minute), time.Minute+100*time.Millisecond)
	next := blk(4, KindFiller, tiny.EndAt, time.Minute)
	if s := PlanNext([]ProgramBlock{tiny, next}, t0); s.Block.Seq != 4 {
		t.Errorf("tiny remainder should be skipped: %+v", s)
	}
	// Unknown kind with a file plays as a program.
	odd := blk(5, KindProgram, t0, time.Minute)
	odd.Kind = "promo"
	if s := PlanNext([]ProgramBlock{odd}, t0); s.Kind != KindProgram {
		t.Errorf("unknown kind: %+v", s)
	}
}
