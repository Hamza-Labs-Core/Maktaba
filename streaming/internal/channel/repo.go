// Package channel implements Story 27.3 — the live stream engine.
//
// A live channel is a long-lived virtual streaming session whose input
// is a schedule (slot 0082 channel_programs), not one file. The engine:
//
//   - reads the schedule and computes the wall-clock join point
//     (join.go): at absolute time T, program P plays at offset
//     T − P.start_at, so every viewer who tunes at the same second sees
//     the same frame;
//   - feeds the upcoming program files to one FFmpeg via the concat
//     demuxer (concat.go), the first seeked to the join offset;
//   - emits a sliding live HLS window (args.go), or a continuous MPEG-TS
//     for HDHomeRun (mpegts.go, Story 27.5);
//   - activates lazily: spawns on first tune, keeps a warm grace window
//     for instant re-tune while surfing, and is reaped after zero
//     viewers (engine.go + registry.go), bounded by a per-host cap.
//
// The pure pieces (join, concat, args, registry) are unit-tested with a
// fake clock + fake runner; no ffmpeg is exec'd in tests.
package channel

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ProgramBlock is one channel_programs row resolved to a playable file.
// Path is the library file for VideoID (or filler media) — only ever a
// server-resolved path, never a user string (threat model).
type ProgramBlock struct {
	Seq              int64
	Kind             string
	VideoID          uuid.UUID
	StartAt          time.Time
	EndAt            time.Time
	SourceOffsetMS   int
	SourceDurationMS int
	Path             string
}

// Duration is the wall-clock length of the block.
func (b ProgramBlock) Duration() time.Duration {
	return b.EndAt.Sub(b.StartAt)
}

// Repo is the data surface the engine needs. Production reads
// channel_programs + writes channel_runtime; tests inject a fake.
type Repo interface {
	// ProgramsFrom returns up to `limit` blocks whose end_at is after
	// `from`, ordered by start_at — the current block plus the
	// look-ahead tail used to build the concat input.
	ProgramsFrom(ctx context.Context, channelID uuid.UUID, from time.Time, limit int) ([]ProgramBlock, error)

	// SetRuntime upserts the channel_runtime row (state machine +
	// viewer count) for observability and cross-replica host pinning.
	SetRuntime(ctx context.Context, rt Runtime) error

	// ClearRuntime removes the runtime row when a channel is reaped.
	ClearRuntime(ctx context.Context, channelID uuid.UUID) error
}

// Runtime mirrors the slot-0083 channel_runtime row.
type Runtime struct {
	ChannelID     uuid.UUID
	Host          string
	PID           int
	State         string // idle | warming | live | draining
	ViewerCount   int
	StartedAt     time.Time
	LastSegmentAt time.Time
}

// Runtime states (mirror the slot-0083 CHECK).
const (
	StateIdle     = "idle"
	StateWarming  = "warming"
	StateLive     = "live"
	StateDraining = "draining"
)
