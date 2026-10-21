// Package channel implements Story 27.3 — the live stream engine.
//
// A live channel is a long-lived virtual broadcast whose input is a
// schedule (slot 0082 channel_programs), not one file. Per channel the
// engine runs one Station (station.go):
//
//   - a Feeder (feeder.go) walks the schedule block by block, re-reading
//     channel_programs as time advances, and plans each block against
//     the wall clock (plan.go + join.go): a viewer tuning mid-block joins
//     at offset now − start_at, every block plays exactly its scheduled
//     span, schedule gaps and failed blocks become slates, and a block
//     whose file cannot be read airs the channel's filler (unreadable.go);
//   - each block is one ffmpeg (blockargs.go) that normalises the source
//     to the channel's fixed output profile (resolution / fps / H.264 via
//     NVENC or libx264 / AAC-or-AC3 48 kHz stereo), paced with -re, with
//     the channel look (watermark, loudness) applied, writing MPEG-TS
//     whose timestamps continue the channel timeline (-output_ts_offset);
//   - all blocks are written into ONE persistent muxer ffmpeg per channel
//     (stream copy) that feeds the sliding live HLS window and a fan-out
//     hub (broadcast.go) serving every HDHomeRun / raw-TS client — one
//     encode per channel no matter how many viewers;
//   - stations start lazily on the first HLS/TS request, stay warm for a
//     grace window after the last viewer, and are reaped after that
//     (engine.go + registry.go), bounded by a per-host cap.
package channel

import (
	"context"
	"errors"
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
	// SourceURL is the stream/page URL of a `live` block (nullable
	// channel_programs.source_url; empty before that migration lands).
	SourceURL string
	// Info is the display part of title_snapshot (the strip's text).
	Info BlockInfo
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

	// Look loads the channel's presentation settings (logo, watermark,
	// loading picture, loudness). ErrChannelNotFound when there is no
	// such channel or it is disabled (disabled channels never stream).
	Look(ctx context.Context, channelID uuid.UUID) (Look, error)

	// SetRuntime upserts the channel_runtime row (state machine +
	// viewer count) for observability and cross-replica host pinning.
	SetRuntime(ctx context.Context, rt Runtime) error

	// ClearRuntime removes the runtime row when a channel is reaped.
	ClearRuntime(ctx context.Context, channelID uuid.UUID) error
}

// Runtime mirrors the slot-0083 channel_runtime row.
type Runtime struct {
	ChannelID uuid.UUID
	// HostID / Host / StreamBaseURL: which streaming host runs the channel
	// and where it is reached (slot 0117; the row is cluster-wide).
	HostID        string
	StreamBaseURL string
	Host          string
	PID           int
	State         string // idle | warming | live | draining
	ViewerCount   int
	StartedAt     time.Time
	LastSegmentAt time.Time
	// Encoder of the block on air (nvenc | videotoolbox | software),
	// muxer restarts and blocks that fell back from NVENC (slot 0117).
	Encoder        string
	Restarts       int
	NVENCFallbacks int64
}

// HostRuntimeClearer is implemented by repos that can drop every
// channel_runtime row of a host (stale rows from before a restart:
// stations live in memory, so none of them survived it).
type HostRuntimeClearer interface {
	ClearHostRuntime(ctx context.Context, hostID, host string) (int64, error)
}

// Runtime states (mirror the slot-0083 CHECK).
const (
	StateIdle     = "idle"
	StateWarming  = "warming"
	StateLive     = "live"
	StateDraining = "draining"
)

// ErrChannelNotFound is returned by Repo.Look for an unknown channel.
var ErrChannelNotFound = errors.New("channel: not found")

// Block kinds (channel_programs.kind).
const (
	KindProgram = "program"
	KindFiller  = "filler"
	KindBumper  = "bumper"
	KindSlate   = "slate"
	KindLive    = "live"
)
