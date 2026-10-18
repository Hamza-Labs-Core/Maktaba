package channel

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ScheduleSource is the part of Repo the feeder reads.
type ScheduleSource interface {
	ProgramsFrom(ctx context.Context, channelID uuid.UUID, from time.Time, limit int) ([]ProgramBlock, error)
}

// Player renders one segment onto the channel output, blocking until it
// has played (or failed). complete reports that the whole segment aired;
// otherwise (source shorter than the slot, ffmpeg failure, unplayable
// media) the feeder fills the rest of the segment with a slate.
type Player interface {
	Play(ctx context.Context, seg Segment) (complete bool, err error)
}

// Feeder walks the schedule forever: it plans the segment at the current
// timeline position, plays it, and advances to the segment's scheduled
// end, re-reading channel_programs at every boundary so schedule
// extensions and edits are picked up as time advances. It ends only when
// ctx is cancelled (the station is stopped).
type Feeder struct {
	Source    ScheduleSource
	ChannelID uuid.UUID
	Player    Player
	Now       func() time.Time
	// Sleep waits (ctx-aware); tests substitute a fake-clock advance.
	Sleep func(ctx context.Context, d time.Duration)
	// OnSegment, if set, observes every segment before it plays.
	OnSegment func(Segment)

	// LiveRetry is how long a failed live block shows the "unavailable"
	// slate before the stream is retried (default 30 s).
	LiveRetry time.Duration
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
)

// Run feeds segments until ctx is done.
func (f *Feeder) Run(ctx context.Context) {
	now := f.Now
	if now == nil {
		now = time.Now
	}
	sleep := f.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	liveRetry := f.LiveRetry
	if liveRetry <= 0 {
		liveRetry = defaultLiveRetry
	}

	pos := now()
	for ctx.Err() == nil {
		// Never fall behind the wall clock: after a stall, rejoin at now
		// (the seek keeps viewers on the scheduled frame).
		if t := now(); pos.Before(t) {
			pos = t
		}
		// Never run far ahead of it either.
		if ahead := pos.Sub(now()); ahead > maxAhead {
			sleep(ctx, ahead-time.Second)
			if ctx.Err() != nil {
				return
			}
		}

		var seg Segment
		blocks, err := f.Source.ProgramsFrom(ctx, f.ChannelID, pos, lookaheadBlocks)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			seg = Segment{Kind: KindSlate, Start: pos, End: pos.Add(scheduleErrorSlate), Synthetic: true}
		} else {
			seg = PlanNext(blocks, pos)
		}

		complete := f.play(ctx, seg)
		if ctx.Err() != nil {
			return
		}
		if at := now(); !complete && seg.Kind != KindSlate && at.Before(seg.End.Add(-minRemaining)) {
			fill := failureFill(seg, at, liveRetry)
			f.play(ctx, fill)
			if ctx.Err() != nil {
				return
			}
			pos = fill.End
			continue
		}
		pos = seg.End
	}
}

func (f *Feeder) play(ctx context.Context, seg Segment) bool {
	if f.OnSegment != nil {
		f.OnSegment(seg)
	}
	complete, _ := f.Player.Play(ctx, seg)
	return complete
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
