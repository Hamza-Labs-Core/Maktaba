package channel

import (
	"context"
	"errors"
	"strings"
	"sync"
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
	mu        sync.Mutex
	clk       *fakeClock
	played    []Segment
	stopAfter int
	cancel    context.CancelFunc
	fail      func(Segment) bool
	failAfter time.Duration
	// events records "start:<seg>" / "done:<seg>" in order.
	events []string
	// holdUntilNext makes Wait return only once the following segment has
	// been started (models a block airing while the next one is queued).
	holdUntilNext bool
	ctx           context.Context
}

type fakePlayback struct {
	p   *fakePlayer
	seg Segment
	idx int
}

func (p *fakePlayer) Start(_ context.Context, seg Segment) Playback {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.played = append(p.played, seg)
	p.events = append(p.events, "start:"+segName(seg))
	if len(p.played) >= p.stopAfter {
		p.cancel()
	}
	return &fakePlayback{p: p, seg: seg, idx: len(p.played) - 1}
}

func (b *fakePlayback) Wait() (bool, error) {
	p, seg := b.p, b.seg
	for p.holdUntilNext && p.ctx.Err() == nil {
		p.mu.Lock()
		n := len(p.played)
		p.mu.Unlock()
		if n > b.idx+1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	start := seg.Start
	if n := p.clk.now(); n.After(start) {
		start = n
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() { p.events = append(p.events, "done:"+segName(seg)) }()
	if p.fail != nil && p.fail(seg) {
		p.clk.set(start.Add(p.failAfter))
		return false, errors.New("boom")
	}
	p.clk.set(seg.End)
	return true, nil
}

func segName(s Segment) string {
	return s.Kind + "@" + s.Start.UTC().Format("15:04:05")
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
	p, _ := runFeederMode(t, repo, clk, n, fail, failAfter, false)
	return p.played
}

// runFeederMode runs the feeder against the fake player. prefetch=false
// makes WaitUntil wait for the current segment to finish (sequential);
// prefetch=true returns at once (the lead point is "now"), exercising the
// queue-ahead path.
func runFeederMode(t *testing.T, repo ScheduleSource, clk *fakeClock, n int, fail func(Segment) bool,
	failAfter time.Duration, prefetch bool) (*fakePlayer, *Feeder) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fakePlayer{clk: clk, stopAfter: n, cancel: cancel, fail: fail, failAfter: failAfter,
		holdUntilNext: prefetch, ctx: ctx}
	f := &Feeder{Source: repo, ChannelID: uuid.New(), Player: p, Now: clk.now,
		Sleep: func(_ context.Context, d time.Duration) { clk.advance(d) },
		WaitUntil: func(ctx context.Context, _ time.Time, done <-chan struct{}) {
			if prefetch {
				return
			}
			select {
			case <-done:
			case <-ctx.Done():
			}
		}}
	finished := make(chan struct{})
	go func() { f.Run(ctx); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("feeder did not stop")
	}
	return p, f
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

func TestFeeder_QueuesNextBlockBeforeBoundary(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	repo := &growingRepo{blocks: []ProgramBlock{
		blk(1, KindProgram, t0, time.Minute),
		blk(2, KindFiller, t0.Add(time.Minute), 30*time.Second),
		blk(3, KindProgram, t0.Add(90*time.Second), time.Minute),
		blk(4, KindBumper, t0.Add(150*time.Second), 10*time.Second),
	}}
	p, _ := runFeederMode(t, repo, clk, 4, nil, 0, true)
	segs := p.played
	assertContiguous(t, segs)
	// Each next segment is started (its ffmpeg spawned) before the
	// current one has finished airing, i.e. queued ahead of the boundary,
	// and segments still complete strictly in order.
	p.mu.Lock()
	ev := strings.Join(p.events, " ")
	p.mu.Unlock()
	for i := 1; i < len(segs); i++ {
		startNext := strings.Index(ev, "start:"+segName(segs[i]))
		donePrev := strings.Index(ev, "done:"+segName(segs[i-1]))
		if donePrev >= 0 && startNext > donePrev {
			t.Errorf("segment %d started only after %d finished: %s", i, i-1, ev)
		}
	}
	var dones []string
	for _, e := range p.events {
		if strings.HasPrefix(e, "done:") {
			dones = append(dones, e)
		}
	}
	for i := 1; i < len(dones); i++ {
		at := func(e string) string { return e[strings.Index(e, "@"):] }
		if at(dones[i]) < at(dones[i-1]) {
			t.Errorf("segments finished out of order: %v", dones)
		}
	}
}

func TestFeeder_PrewarmsLiveBlocksAhead(t *testing.T) {
	t0 := tm("2026-06-14T20:00:00Z")
	clk := newClock(t0)
	live1 := blk(2, KindLive, t0.Add(50*time.Second), time.Hour) // inside 60 s
	live2 := blk(4, KindLive, t0.Add(10*time.Minute), time.Hour) // too far
	repo := &growingRepo{blocks: []ProgramBlock{
		blk(1, KindProgram, t0, 50*time.Second), live1,
		blk(3, KindProgram, live1.EndAt, 10*time.Minute), live2,
	}}
	var mu sync.Mutex
	var warmed []int64
	f := &Feeder{Source: repo, Now: clk.now, Prewarm: func(_ context.Context, b ProgramBlock) {
		mu.Lock()
		warmed = append(warmed, b.Seq)
		mu.Unlock()
	}}
	f.PrewarmOnce(context.Background())
	f.PrewarmOnce(context.Background()) // no duplicate
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(warmed) >= 1 })
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	if len(warmed) != 1 || warmed[0] != 2 {
		t.Errorf("warmed %v, want only the live block starting within 60 s", warmed)
	}
	mu.Unlock()
	// Ten minutes later the second live block comes into range.
	clk.set(t0.Add(9*time.Minute + 30*time.Second))
	f.PrewarmOnce(context.Background())
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(warmed) == 2 })
	mu.Lock()
	if warmed[1] != 4 {
		t.Errorf("warmed %v", warmed)
	}
	mu.Unlock()
}

func TestYtDlp_PrewarmFillsCache(t *testing.T) {
	y := NewYtDlp("x")
	calls := 0
	y.run = func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte("https://cdn/x.m3u8\n"), nil
	}
	st := &Station{cfg: StationConfig{Resolver: y}, log: discardLogger()}
	st.prewarmLive(context.Background(), ProgramBlock{Seq: 1, Kind: KindLive, SourceURL: "https://www.youtube.com/watch?v=1"})
	if u, _ := y.Resolve(context.Background(), LiveRequest{SourceURL: "https://www.youtube.com/watch?v=1"}); u != "https://cdn/x.m3u8" || calls != 1 {
		t.Errorf("prewarm should populate the cache: %q calls=%d", u, calls)
	}
}
