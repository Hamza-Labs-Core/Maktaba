package channel

import "time"

// Segment is one unit of on-air output: a span of the channel timeline
// [Start, End) and what plays in it. Most segments are a schedule block
// joined at Start; synthetic slates fill schedule gaps, an empty horizon,
// and the remainder of a block that failed.
type Segment struct {
	// Kind is what the player renders: program | filler | bumper (a
	// library file), live (a stream URL) or slate (a still card).
	Kind      string
	Block     ProgramBlock // the schedule block (zero for a synthetic slate)
	Start     time.Time    // timeline position this segment starts at
	End       time.Time    // timeline position it must end at
	SeekMS    int          // ms into the source file at Start (files only)
	Message   string       // slate caption (e.g. "Live stream unavailable")
	Synthetic bool         // not a schedule block (gap / empty / failure fill)
	// Next is the next programme after this segment (the strip's «التالي»),
	// nil when the look-ahead holds none.
	Next *ProgramBlock
}

// Duration is the planned on-air length.
func (s Segment) Duration() time.Duration { return s.End.Sub(s.Start) }

// Planning tolerances.
const (
	// minRemaining: a block with less than this left at the position is
	// skipped (not worth spawning ffmpeg for a few frames).
	minRemaining = 250 * time.Millisecond
	// earlyStart: a block starting within this of the position is
	// started right away instead of emitting a sub-second slate.
	earlyStart = time.Second
	// maxGapSlate caps a gap slate so the schedule is re-read regularly
	// (a scheduler run may fill the gap meanwhile).
	maxGapSlate = 5 * time.Minute
	// emptySlate is the slate length when nothing is scheduled at all.
	emptySlate = 30 * time.Second
)

// Slate captions.
const (
	MsgLiveUnavailable = "Live stream unavailable"
)

// PlanNext decides what airs from timeline position `pos`, given blocks
// ordered by start_at (end_at > pos − typically ProgramsFrom(pos)). It
// never fails: anything unplayable becomes a slate of the right length,
// so the channel clock never drifts.
func PlanNext(blocks []ProgramBlock, pos time.Time) Segment {
	var live []ProgramBlock
	for _, b := range blocks {
		if b.EndAt.Sub(pos) >= minRemaining {
			live = append(live, b)
		}
	}
	if len(live) == 0 {
		return Segment{Kind: KindSlate, Start: pos, End: pos.Add(emptySlate), Synthetic: true}
	}
	first := live[0]
	if gap := first.StartAt.Sub(pos); gap > earlyStart {
		end := first.StartAt
		if gap > maxGapSlate {
			end = pos.Add(maxGapSlate)
		}
		return Segment{Kind: KindSlate, Start: pos, End: end, Synthetic: true, Next: nextProgramme(live, 0)}
	}
	// Wall-clock join (join.go): seek = (pos − start) + source_offset;
	// a block starting within earlyStart joins at its own offset.
	j, err := Locate(live[:1], pos)
	if err != nil {
		return Segment{Kind: KindSlate, Start: pos, End: pos.Add(emptySlate), Synthetic: true}
	}
	b := j.Block
	seg := Segment{Kind: b.Kind, Block: b, Start: pos, End: b.EndAt, SeekMS: j.SeekMS,
		Next: nextProgramme(live, 1)}
	switch b.Kind {
	case KindProgram, KindFiller, KindBumper:
		if b.Path == "" {
			seg.Kind = KindSlate // video deleted / unresolved → keep time
		}
	case KindLive:
		seg.SeekMS = 0
		if b.SourceURL == "" {
			seg.Kind = KindSlate
			seg.Message = MsgLiveUnavailable
		}
	case KindSlate:
		seg.SeekMS = 0
	default:
		if b.Path != "" {
			seg.Kind = KindProgram
		} else {
			seg.Kind = KindSlate
		}
	}
	return seg
}

// nextProgramme is the first programme or live block in blocks[from:]
// (filler, bumpers and slates are not "next"), else the first block with a
// title, else nil.
func nextProgramme(blocks []ProgramBlock, from int) *ProgramBlock {
	var titled *ProgramBlock
	for i := from; i < len(blocks); i++ {
		b := blocks[i]
		if b.Kind == KindProgram || b.Kind == KindLive {
			return &b
		}
		if titled == nil && b.Kind != KindSlate && b.Info.Line() != "" {
			titled = &b
		}
	}
	return titled
}

// failureFill returns the slate that covers the rest of `seg` after its
// player stopped early at `at`. A failed live block is retried after
// liveRetry rather than slated for its whole (possibly hours-long) span.
func failureFill(seg Segment, at time.Time, liveRetry time.Duration) Segment {
	if at.Before(seg.Start) {
		at = seg.Start
	}
	fill := Segment{Kind: KindSlate, Block: seg.Block, Start: at, End: seg.End, Synthetic: true}
	if seg.Kind == KindLive {
		fill.Message = MsgLiveUnavailable
		if at.Add(liveRetry).Before(seg.End) {
			fill.End = at.Add(liveRetry)
		}
	}
	return fill
}
