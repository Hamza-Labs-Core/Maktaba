package channel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
)

// Defaults for the engine lifecycle.
const (
	DefaultLookahead  = 4                // blocks queued into the concat input
	DefaultWarmGrace  = 60 * time.Second // keep encoder warm after last viewer
	DefaultPerHostCap = 8                // max concurrent channel encoders
	defaultMasterName = "master.m3u8"
)

// Runner spawns an FFmpeg job for a channel and returns a controller. The
// production runner shells out to ffmpeg; tests inject a fake. Mirrors
// ffmpeg.Runner but channel-scoped (concat input + live window).
type Runner interface {
	StartHLS(ctx context.Context, job Job) (Controller, error)
}

// Controller is the small surface the engine + reaper need to terminate
// an encoder (the session.Transcoder shape).
type Controller interface {
	Stop(ctx context.Context) error
	PID() int
	Active() bool
}

// Job is one channel encoder invocation.
type Job struct {
	ChannelID  uuid.UUID
	ConcatPath string
	OutputDir  string
	MasterName string
	Ladder     []ffmpeg.Rendition
	HWAccel    string
}

// Layout resolves the on-disk HLS directory for a channel session.
type Layout interface {
	HLSDir(sessionID string) string
}

// Engine owns the live-channel lifecycle: lazy activation, the warm
// re-tune window, the concat input, and the per-host cap (D1/D5/D6).
type Engine struct {
	Repo      Repo
	Runner    Runner
	Layout    Layout
	Ladder    []ffmpeg.Rendition
	HWAccel   string
	Host      string
	Lookahead int
	WarmGrace time.Duration

	// TS is the MPEG-TS runner used by StreamChannelTS (the HDHomeRun
	// path, Story 27.5). Optional; nil means TS pulls are unsupported.
	TS TSRunner

	reg *registry
	now func() time.Time
}

// NewEngine builds an Engine with the per-host cap and clock. A nil clock
// uses time.Now; cap ≤ 0 uses DefaultPerHostCap.
func NewEngine(repo Repo, runner Runner, layout Layout, perHostCap int, now func() time.Time) *Engine {
	if perHostCap <= 0 {
		perHostCap = DefaultPerHostCap
	}
	if now == nil {
		now = time.Now
	}
	return &Engine{
		Repo:      repo,
		Runner:    runner,
		Layout:    layout,
		Lookahead: DefaultLookahead,
		WarmGrace: DefaultWarmGrace,
		reg:       newRegistry(perHostCap, now),
		now:       now,
	}
}

// TuneResult is what a successful Tune returns to the caller (the API
// proxy turns it into {session, manifest_url}).
type TuneResult struct {
	ChannelID   uuid.UUID
	SessionID   string
	ManifestURL string // relative path served by the manifest handler
}

// Tune activates a channel (or attaches to a warm one) at the current
// wall clock and returns its live HLS manifest path. Lazy: a cold
// channel spawns FFmpeg here; a warm one (recently zero viewers) is
// re-attached instantly without a respawn (D5).
func (e *Engine) Tune(ctx context.Context, channelID uuid.UUID) (TuneResult, error) {
	sessionID := channelSessionID(channelID)
	manifest := "/stream/channel/" + channelID.String() + "/" + defaultMasterName

	// Warm re-tune: attach to the running encoder, no respawn.
	if _, ok := e.reg.get(channelID); ok {
		if e.reg.attach(channelID) {
			e.writeRuntime(ctx, channelID, StateLive)
			return TuneResult{ChannelID: channelID, SessionID: sessionID, ManifestURL: manifest}, nil
		}
	}

	// Cold tune: reserve a slot (may evict an LRU warm channel), then
	// locate the join point and spawn.
	if err := e.reg.admit(channelID); err != nil {
		return TuneResult{}, err
	}

	now := e.now()
	blocks, err := e.Repo.ProgramsFrom(ctx, channelID, now, e.Lookahead+1)
	if err != nil {
		return TuneResult{}, err
	}
	join, err := Locate(blocks, now)
	if err != nil {
		return TuneResult{}, err
	}
	entries := BuildConcat(blocks, join, e.Lookahead)
	if len(entries) == 0 {
		return TuneResult{}, errors.New("channel: no resolvable program files at join point")
	}

	outDir := e.Layout.HLSDir(sessionID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return TuneResult{}, err
	}
	concatPath := filepath.Join(outDir, "concat.ffconcat")
	if err := os.WriteFile(concatPath, []byte(FormatConcat(entries)), 0o644); err != nil {
		return TuneResult{}, err
	}

	ctrl, err := e.Runner.StartHLS(ctx, Job{
		ChannelID:  channelID,
		ConcatPath: concatPath,
		OutputDir:  outDir,
		MasterName: defaultMasterName,
		Ladder:     e.Ladder,
		HWAccel:    e.HWAccel,
	})
	if err != nil {
		return TuneResult{}, err
	}

	e.reg.put(channelID, func() {
		_ = ctrl.Stop(context.Background())
		_ = e.Repo.ClearRuntime(context.Background(), channelID)
	})
	e.writeRuntimeFull(ctx, channelID, StateLive, ctrl.PID(), now)

	return TuneResult{ChannelID: channelID, SessionID: sessionID, ManifestURL: manifest}, nil
}

// Detach records that a viewer left; the channel stays warm until the
// grace window expires (instant re-tune while surfing).
func (e *Engine) Detach(channelID uuid.UUID) {
	e.reg.detach(channelID)
}

// Touch refreshes the LRU/idle timestamp on a segment fetch.
func (e *Engine) Touch(channelID uuid.UUID) {
	e.reg.touch(channelID)
}

// ReapIdle stops channels idle past the warm grace window and clears
// their runtime rows. Called from the streaming reaper Sweep.
func (e *Engine) ReapIdle(_ context.Context) []uuid.UUID {
	return e.reg.reapIdle(e.WarmGrace)
}

// ActiveCount reports the number of live channel encoders on this host.
func (e *Engine) ActiveCount() int { return e.reg.size() }

func (e *Engine) writeRuntime(ctx context.Context, channelID uuid.UUID, state string) {
	_ = e.Repo.SetRuntime(ctx, Runtime{
		ChannelID:     channelID,
		Host:          e.Host,
		State:         state,
		ViewerCount:   e.viewerCount(channelID),
		LastSegmentAt: e.now(),
	})
}

func (e *Engine) writeRuntimeFull(ctx context.Context, channelID uuid.UUID, state string, pid int, started time.Time) {
	_ = e.Repo.SetRuntime(ctx, Runtime{
		ChannelID:     channelID,
		Host:          e.Host,
		PID:           pid,
		State:         state,
		ViewerCount:   e.viewerCount(channelID),
		StartedAt:     started,
		LastSegmentAt: started,
	})
}

func (e *Engine) viewerCount(channelID uuid.UUID) int {
	if ent, ok := e.reg.get(channelID); ok {
		return ent.viewers
	}
	return 0
}

// channelSessionID derives the stable session id for a channel's encoder
// so its HLS dir is deterministic across re-tunes.
func channelSessionID(channelID uuid.UUID) string {
	return "channel-" + channelID.String()
}
