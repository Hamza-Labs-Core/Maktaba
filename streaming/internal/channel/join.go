package channel

import (
	"errors"
	"time"
)

// ErrNoProgram is returned when the schedule has no block covering or
// after `now` — a cold channel with an exhausted/empty horizon.
var ErrNoProgram = errors.New("channel: no program at or after now")

// Join is the wall-clock entry point into a channel: which block is on
// air and how far into its source to seek.
type Join struct {
	Index  int          // index into the blocks slice
	Block  ProgramBlock // the block on air
	SeekMS int          // ms into the SOURCE file to start decoding
}

// Locate computes the join point for `now` against an ordered (by
// start_at) block slice (D3). The seek is measured into the source file:
//
//	seek = (now − block.start_at) + block.source_offset
//
// so two viewers tuning at the same wall-clock second compute the same
// seek and see the same frame. If `now` precedes the first block, the
// channel joins the first block at its source offset (seek = offset). If
// `now` is at/after the last block's end, ErrNoProgram is returned.
func Locate(blocks []ProgramBlock, now time.Time) (Join, error) {
	if len(blocks) == 0 {
		return Join{}, ErrNoProgram
	}
	// Before the window starts → join the first block at its offset.
	if now.Before(blocks[0].StartAt) {
		return Join{Index: 0, Block: blocks[0], SeekMS: blocks[0].SourceOffsetMS}, nil
	}
	for i, b := range blocks {
		// Current block: start ≤ now < end.
		if !now.Before(b.StartAt) && now.Before(b.EndAt) {
			into := int(now.Sub(b.StartAt) / time.Millisecond)
			return Join{Index: i, Block: b, SeekMS: b.SourceOffsetMS + into}, nil
		}
		// Exactly at a boundary (now == end) → advance to the next block
		// at offset 0 (EC1).
		if now.Equal(b.EndAt) && i+1 < len(blocks) {
			nb := blocks[i+1]
			return Join{Index: i + 1, Block: nb, SeekMS: nb.SourceOffsetMS}, nil
		}
	}
	return Join{}, ErrNoProgram
}
