package channel

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ScheduleSource is the part of Repo the feeder reads.
type ScheduleSource interface {
	ProgramsFrom(ctx context.Context, channelID uuid.UUID, from time.Time, limit int) ([]ProgramBlock, error)
}

// Player renders segments onto the channel output. Start begins
// preparing a segment right away (probe, spawn ffmpeg, buffer its
// output) so it can be queued ahead of the boundary; the returned
// Playback forwards it to the output when Wait is called and blocks until
// it has aired.
type Player interface {
	Start(ctx context.Context, seg Segment) Playback
}

// Playback is one started segment.
type Playback interface {
	// Wait forwards the segment's output and blocks until it is done.
	// complete reports that the whole segment aired; otherwise (source
	// shorter than the slot, ffmpeg failure, unplayable media) the feeder
	// fills the rest with a slate when there is still time to.
	Wait() (complete bool, err error)
}

// Feeder walks the schedule forever: it plans the segment at the current
// timeline position, starts it, and — Lead before the segment's scheduled
// end — plans and starts the next one so its ffmpeg is already running
// (output buffered) when the current one finishes. The output therefore
// switches at the exact boundary with contiguous timestamps. The schedule
// is re-read at every boundary, so extensions and edits are picked up as
// time advances. Run ends only when ctx is cancelled.
type Feeder struct {
	Source    ScheduleSource
	ChannelID uuid.UUID
	Player    Player
	Now       func() time.Time
	// Sleep waits (ctx-aware); tests substitute a fake-clock advance.
	Sleep func(ctx context.Context, d time.Duration)
	// WaitUntil blocks until wall time t, done closing, or ctx ending;
	// tests substitute it.
	WaitUntil func(ctx context.Context, t time.Time, done <-chan struct{})
	// OnSegment, if set, observes every segment as it is started.
	OnSegment func(Segment)

	// Lead is how long before a boundary the next segment is started
	// (default 3 s).
	Lead time.Duration
	// LiveRetry is how long a failed live block shows the "unavailable"
	// slate before the stream is retried (default 30 s).
	LiveRetry time.Duration

	// Prewarm, if set, is called (in the background, once per block) for
	// each live block starting within PrewarmWindow, so its yt-dlp
	// resolution and probe are cached before the block is queued.
	Prewarm func(ctx context.Context, b ProgramBlock)
	// PrewarmWindow defaults to 60 s; PrewarmEvery (the scan period) to 10 s.
	PrewarmWindow time.Duration
	PrewarmEvery  time.Duration

	// A block whose file cannot be read (unreadable.go). Check, if set, tells
	// before a file segment starts whether its source can be read; a failing
	// check, or a block that fails with ErrSourceUnreadable, marks the block
	// unreadable for the rest of its span. OnUnreadable is told once per
	// block. UnreadableFill is what airs in its place: FillFiller (the
	// default; the Source's FillersAround) or FillSlate.
	Check          func(ctx context.Context, b ProgramBlock) error
	OnUnreadable   func(b ProgramBlock, err error)
	UnreadableFill string

	warmMu sync.Mutex
	warmed map[int64]time.Time

	// unreadable: seq → end of the blocks marked unreadable; aired counts
	// the stand-ins per video, so they take turns. Both are Run's alone.
	unreadable map[int64]time.Time
	aired      map[uuid.UUID]int
}

// Feeder tunables.
const (
	lookaheadBlocks = 4
	// earlyEnd: tolerance when judging whether a block ran its full
	// length (ffmpeg start-up, -re initial burst).
	earlyEnd = 2 * time.Second
	// maxAhead bounds how far the timeline may run ahead of the wall
	// clock (filters with look-ahead flush their tail early; ffmpeg
	// failing instantly) before the feeder waits.
	maxAhead = 6 * time.Second
	// scheduleErrorSlate is the slate played when the schedule can't be
	// read (DB hiccup) before retrying.
	scheduleErrorSlate = 10 * time.Second
	defaultLiveRetry   = 30 * time.Second
	defaultLead        = 3 * time.Second
	// defaultPrewarmWindow: live blocks are resolved this long ahead.
	defaultPrewarmWindow = 60 * time.Second
)

// running is a started segment whose Wait runs once its predecessor
// has finished forwarding.
type running struct {
	seg      Segment
	done     chan struct{}
	complete bool
	err      error
}

// Run feeds segments until ctx is done.
func (f *Feeder) Run(ctx context.Context) {
	if f.Now == nil {
		f.Now = time.Now
	}
	if f.Sleep == nil {
		f.Sleep = sleepCtx
	}
	if f.WaitUntil == nil {
		f.WaitUntil = waitUntil
	}
	lead := f.Lead
	if lead <= 0 {
		lead = defaultLead
	}
	liveRetry := f.LiveRetry
	if liveRetry <= 0 {
		liveRetry = defaultLiveRetry
	}

	if f.Prewarm != nil {
		go f.prewarmLoop(ctx)
	}

	closed := make(chan struct{})
	close(closed)
	cur := f.start(ctx, f.plan(ctx, f.Now()), closed)
	for ctx.Err() == nil {
		// Wait until the current segment approaches its end (or ends).
		f.WaitUntil(ctx, cur.seg.End.Add(-lead), cur.done)
		if ctx.Err() != nil {
			break
		}
		if finished(cur.done) && !cur.complete && cur.seg.Kind != KindSlate {
			if at := f.Now(); at.Before(cur.seg.End.Add(-minRemaining)) {
				if errors.Is(cur.err, ErrSourceUnreadable) && fileKind(cur.seg.Kind) {
					// Its file cannot be read: the rest of the span gets
					// a stand-in (plan → standIn).
					f.markUnreadable(cur.seg.Block, cur.err)
					cur = f.start(ctx, f.plan(ctx, at), cur.done)
					continue
				}
				// Failed / ran short with time left: slate the rest (or,
				// for live, until the retry).
				cur = f.start(ctx, failureFill(cur.seg, at, liveRetry), cur.done)
				continue
			}
		}
		// Queue the next segment behind the current one.
		next := f.start(ctx, f.plan(ctx, cur.seg.End), cur.done)
		select {
		case <-cur.done:
			if errors.Is(cur.err, ErrSourceUnreadable) && fileKind(cur.seg.Kind) {
				// failed once the next segment was queued: still reported
				f.markUnreadable(cur.seg.Block, cur.err)
			}
		case <-ctx.Done():
		}
		cur = next
	}
	// Let the last started playback unwind (its process dies with ctx).
	<-cur.done
}

// plan decides the segment at pos (never behind the wall clock, never too
// far ahead of it).
func (f *Feeder) plan(ctx context.Context, pos time.Time) Segment {
	if t := f.Now(); pos.Before(t) {
		pos = t // after a stall, rejoin at now (the seek keeps the frame)
	}
	if ahead := pos.Sub(f.Now()); ahead > maxAhead {
		f.Sleep(ctx, ahead-maxAhead/2)
	}
	blocks, err := f.Source.ProgramsFrom(ctx, f.ChannelID, pos, lookaheadBlocks)
	if err != nil {
		return Segment{Kind: KindSlate, Start: pos, End: pos.Add(scheduleErrorSlate), Synthetic: true}
	}
	return f.standIn(ctx, PlanNext(blocks, pos))
}

// fileKind: the segment plays a library file.
func fileKind(k string) bool { return k == KindProgram || k == KindFiller || k == KindBumper }

// standIn returns seg, or what airs in its place when its file cannot be
// read: the channel's filler nearest it, one at a time until the block's
// end (each filler's end re-plans here), else a slate. The block keeps its
// place on the wall clock, so the guide stays true from the next block on.
func (f *Feeder) standIn(ctx context.Context, seg Segment) Segment {
	if seg.Synthetic || !fileKind(seg.Kind) {
		return seg
	}
	if _, bad := f.unreadable[seg.Block.Seq]; !bad {
		if f.Check == nil {
			return seg
		}
		err := f.Check(ctx, seg.Block)
		if err == nil || ctx.Err() != nil {
			return seg
		}
		f.markUnreadable(seg.Block, err)
	}
	// A slate is re-planned within maxGapSlate: a filler list that could
	// not be read now may be readable then.
	slate := Segment{Kind: KindSlate, Block: seg.Block, Start: seg.Start, End: seg.End, Synthetic: true, Next: seg.Next}
	if slate.End.Sub(slate.Start) > maxGapSlate {
		slate.End = slate.Start.Add(maxGapSlate)
	}
	fs, ok := f.Source.(FillerSource)
	if f.UnreadableFill == FillSlate || !ok {
		return slate
	}
	cands, err := fs.FillersAround(ctx, f.ChannelID, seg.Start, fillerCandidates)
	if err != nil {
		return slate
	}
	// the least aired first; among equals the nearest (the list's order)
	sort.SliceStable(cands, func(i, j int) bool { return f.aired[cands[i].VideoID] < f.aired[cands[j].VideoID] })
	for _, c := range cands {
		d := time.Duration(c.SourceDurationMS) * time.Millisecond
		if _, bad := f.unreadable[c.Seq]; bad || c.Path == "" || !fileKind(c.Kind) || d < minRemaining {
			continue
		}
		if f.Check != nil {
			if err := f.Check(ctx, c); err != nil {
				if ctx.Err() != nil {
					return slate
				}
				f.markUnreadable(c, err)
				if errors.Is(err, ErrSourceTimeout) {
					return slate // a share that does not answer: its other files would not either
				}
				continue
			}
		}
		if f.aired == nil {
			f.aired = map[uuid.UUID]int{}
		}
		f.aired[c.VideoID]++
		end := seg.Start.Add(d)
		if end.After(seg.End) {
			end = seg.End
		}
		return Segment{Kind: c.Kind, Block: c, Start: seg.Start, End: end, SeekMS: c.SourceOffsetMS,
			Synthetic: true, Next: seg.Next}
	}
	return slate
}

// markUnreadable records b as unreadable until its end and reports it.
func (f *Feeder) markUnreadable(b ProgramBlock, err error) {
	if f.unreadable == nil {
		f.unreadable = map[int64]time.Time{}
	}
	if _, seen := f.unreadable[b.Seq]; seen {
		return
	}
	now := f.Now()
	for seq, end := range f.unreadable {
		if end.Before(now) {
			delete(f.unreadable, seq)
		}
	}
	f.unreadable[b.Seq] = b.EndAt
	if f.OnUnreadable != nil {
		f.OnUnreadable(b, err)
	}
}

// start begins seg now and forwards it once prev is done.
func (f *Feeder) start(ctx context.Context, seg Segment, prev <-chan struct{}) *running {
	if f.OnSegment != nil {
		f.OnSegment(seg)
	}
	r := &running{seg: seg, done: make(chan struct{})}
	pb := f.Player.Start(ctx, seg)
	go func() {
		defer close(r.done)
		<-prev
		r.complete, r.err = pb.Wait()
	}()
	return r
}

// prewarmLoop scans the schedule ahead for live blocks.
func (f *Feeder) prewarmLoop(ctx context.Context) {
	every := f.PrewarmEvery
	if every <= 0 {
		every = 10 * time.Second
	}
	for ctx.Err() == nil {
		f.PrewarmOnce(ctx)
		f.Sleep(ctx, every)
	}
}

// PrewarmOnce starts Prewarm for every not-yet-warmed live block that
// starts (or is on air) within PrewarmWindow of now.
func (f *Feeder) PrewarmOnce(ctx context.Context) {
	window := f.PrewarmWindow
	if window <= 0 {
		window = defaultPrewarmWindow
	}
	now := f.Now()
	blocks, err := f.Source.ProgramsFrom(ctx, f.ChannelID, now, lookaheadBlocks*2)
	if err != nil {
		return
	}
	f.warmMu.Lock()
	defer f.warmMu.Unlock()
	if f.warmed == nil {
		f.warmed = map[int64]time.Time{}
	}
	for id, end := range f.warmed {
		if end.Before(now) {
			delete(f.warmed, id)
		}
	}
	for _, b := range blocks {
		if b.Kind != KindLive || b.SourceURL == "" || b.StartAt.Sub(now) > window {
			continue
		}
		if _, done := f.warmed[b.Seq]; done {
			continue
		}
		f.warmed[b.Seq] = b.EndAt
		go f.Prewarm(ctx, b)
	}
}

func finished(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func waitUntil(ctx context.Context, t time.Time, done <-chan struct{}) {
	d := time.Until(t)
	if d <= 0 {
		return
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
	case <-done:
	case <-tm.C:
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
