package ffmpeg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// HLB-328: until this file landed, FFmpeg was never spawned for the
// HLS/DASH path — HLSArgs/DASHArgs/Remuxer.Run had no runtime caller, so
// every manifest/segment request 404'd in production. Orchestrator owns
// the "compute output dir → write master playlist → spawn FFmpeg" flow
// that grpcsrv.OpenSession invokes for transcode-mode sessions, behind
// the Runner seam so tests inject a fake that produces canned output
// instead of exec'ing ffmpeg (mirrors the hwaccel Detector seam and the
// pipeline's EXTRACT/PROBE ffmpeg/ffprobe DI seams).

// Job is one transcode invocation produced by grpcsrv.OpenSession.
type Job struct {
	SessionID  string
	InputPath  string
	OutputDir  string // per-session HLS dir (cache/hls/{session_id})
	Ladder     []Rendition
	HWAccel    string // "" → software (libx264)
	Format     string // "hls" | "dash"
	MasterName string // master playlist filename (default master.m3u8)
	// SourceCodec is the source's video codec (the probe's): the GPU decodes
	// only H.264 and HEVC (PlanFor).
	SourceCodec string
	// NoAudio: the source has no audio stream (none is mapped).
	NoAudio bool
	// StartSec is where in the source the transcode starts (seconds; HLS
	// only, EncodePlan.Start).
	StartSec int
	// Colour is the source's colour from the catalogue (CatalogueColour), nil
	// when it does not settle HDR or not: then the runner probes the file.
	Colour *Colour

	// toneMap: the runner's tone-mapping for an HDR source (toneMapFor)
	toneMap string
}

// Handle is the live transcode controller pinned to a session. It
// satisfies session.Transcoder (Stop/PID/Active) so the reaper and
// CloseSession can terminate the child on idle/close. *Process already
// implements this; Handle adds a nil-safe wrapper for the fake-runner
// path where no OS process exists.
//
// HLB-328 C1: the encoder MUST outlive the OpenSession RPC. grpc-go
// cancels the inbound per-RPC ctx the instant OpenSession returns, so
// the child process can NOT be parented to that ctx (exec.CommandContext
// would SIGKILL ffmpeg within ms of the reply, before any segment was
// produced). The encoder is therefore spawned under a session-scoped
// context owned here: procCancel cancels that context and is driven
// only by Handle.Stop (reaper Sweep / MemoryStore.Close / CloseSession
// all funnel through Handle.Stop). The request ctx is kept strictly for
// request-scoped concerns (the Orchestrator's pre-spawn validation).
type Handle struct {
	mu         sync.Mutex
	proc       *Process
	procCancel context.CancelFunc // cancels the session-scoped encoder ctx
	// stopped: Stop ran (no new encoder starts); restarting: the hardware
	// run failed and its software run is starting (the session stays active)
	stopped, restarting bool
	// ended: the transcode is over (no encoder runs, or will); finished: it
	// encoded the whole source (its last encoder exited 0, not stopped)
	ended, finished bool
	onEnd           []func()
	stopFn          func(context.Context) error
	pidFn           func() int
	actFn           func() bool
}

// current is the encoder running now, and its context's cancel.
func (h *Handle) current() (*Process, context.CancelFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.proc, h.procCancel
}

// set makes proc the session's encoder; false when the session was stopped
// meanwhile (the caller stops proc).
func (h *Handle) set(proc *Process, cancel context.CancelFunc) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	h.proc, h.procCancel, h.restarting = proc, cancel, false
	return true
}

// restart marks the session as getting its software run; false once stopped.
func (h *Handle) restart() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	h.restarting = true
	return true
}

// exited records the transcode's end (err: its last encoder's); the OnEnd
// functions run once.
func (h *Handle) exited(err error) {
	h.mu.Lock()
	if h.ended {
		h.mu.Unlock()
		return
	}
	h.ended, h.finished = true, err == nil && !h.stopped
	fns := h.onEnd
	h.onEnd = nil
	h.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

// Ended reports whether the transcode is over, finished or not.
func (h *Handle) Ended() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ended
}

// Finished reports whether the transcode encoded the whole source: its
// playlist ends at the video's end. Not when it was stopped or failed.
func (h *Handle) Finished() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.finished
}

// OnEnd runs f once the transcode is over, finished or not (at once when it
// is).
func (h *Handle) OnEnd(f func()) {
	h.mu.Lock()
	if !h.ended {
		h.onEnd = append(h.onEnd, f)
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	f()
}

// isStopped reports whether Stop ran (the encoder's exit was asked for).
func (h *Handle) isStopped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopped
}

// Stderr is the encoder's recent stderr (empty for a test handle).
func (h *Handle) Stderr() string {
	if h == nil {
		return ""
	}
	if proc, _ := h.current(); proc != nil {
		return proc.tail.String()
	}
	return ""
}

// Stop terminates the underlying process. Idempotent.
//
// procCancel() is invoked IN ADDITION to Process.Stop so that the
// session-scoped encoder context is always torn down even though the
// child is no longer parented to the request ctx. Process.Stop still
// does the deterministic SIGTERM→grace→SIGKILL termination + Wait reap;
// cancelling procCtx is belt-and-braces (and frees the context's
// goroutine) and makes Stop the single owner of the encoder lifetime.
func (h *Handle) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	h.stopped = true
	proc, cancel := h.proc, h.procCancel
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if proc != nil {
		return proc.Stop(ctx)
	}
	var err error
	if h.stopFn != nil {
		err = h.stopFn(ctx)
	}
	h.exited(errStopped) // a test handle: no encoder's watch ends it
	return err
}

// errStopped ends a transcode stopped before an encoder of it ran.
var errStopped = errors.New("ffmpeg: transcode stopped")

// PID returns the OS pid (0 for the fake runner / once stopped).
func (h *Handle) PID() int {
	if h == nil {
		return 0
	}
	if proc, _ := h.current(); proc != nil {
		return proc.PID()
	}
	if h.pidFn != nil {
		return h.pidFn()
	}
	return 0
}

// Active reports whether the transcode is still running.
func (h *Handle) Active() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	proc, restarting := h.proc, h.restarting
	h.mu.Unlock()
	if proc != nil {
		return restarting || proc.Active()
	}
	if h.actFn != nil {
		return h.actFn()
	}
	return false
}

// Runner is the DI seam. The real implementation shells out to ffmpeg
// via Spawn; tests inject a fake that writes canned segments/manifests
// and returns a Handle with no OS process. OpenSession never execs
// ffmpeg in unit tests because the seam is replaced.
type Runner interface {
	Start(ctx context.Context, job Job) (*Handle, error)
}

// Orchestrator drives a Job: it ensures the output dir, writes the HLS
// master playlist up-front (so the player's first manifest GET resolves
// even before FFmpeg has produced a variant segment), then asks the
// Runner to spawn the encoder. The variant index.m3u8 + segments are
// produced by FFmpeg's own -f hls/-f dash muxer into OutputDir.
type Orchestrator struct {
	Runner Runner
}

// DefaultOrchestrator returns an Orchestrator backed by the real
// ffmpeg-spawning Runner, its hardware encoders' sessions counted by gates
// (nil: uncounted), logging to logger (nil: slog's default). main.go wires
// this; tests swap Runner.
func DefaultOrchestrator(bin Binary, gates map[HWAccel]SessionGate, logger *slog.Logger) *Orchestrator {
	return &Orchestrator{Runner: &execRunner{Bin: bin, Gates: gates, Logger: logger}}
}

// SessionGate is a hardware encoder's session budget, shared with the TV's
// playout in this process (channel.EncoderGate): a GeForce card opens only a
// few NVENC sessions, and every ladder rung on NVENC is one.
type SessionGate interface {
	Acquire(ctx context.Context, owner uuid.UUID, wait time.Duration) (release func(refused bool), ok bool)
}

// SessionBatch is a SessionGate that gives a transcode's rungs their sessions
// in one go (up to n; the rest are libx264's): VOD's gate asks the TV's slots
// once for them all.
type SessionBatch interface {
	AcquireN(ctx context.Context, owner uuid.UUID, n int) []func(refused bool)
}

// Start prepares the session HLS dir, writes the master playlist for
// HLS, and spawns the encoder via the Runner seam. The returned Handle
// is attached to the session row so the reaper/CloseSession can kill it.
func (o *Orchestrator) Start(ctx context.Context, job Job) (*Handle, error) {
	if o == nil || o.Runner == nil {
		return nil, errors.New("ffmpeg: orchestrator has no runner")
	}
	if job.OutputDir == "" {
		return nil, errors.New("ffmpeg: empty output dir")
	}
	if job.InputPath == "" {
		return nil, errors.New("ffmpeg: empty input path")
	}
	if job.MasterName == "" {
		job.MasterName = "master.m3u8"
	}
	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		return nil, err
	}
	// HLS: write the master playlist now. FFmpeg's -master_pl_name also
	// writes one, but doing it up-front means the player's first
	// manifest GET resolves to a 200 (it then polls the variant index,
	// which 404s with a retry hint until the first segment lands) —
	// instead of the manifest itself 404'ing for the whole spin-up.
	if !strings.EqualFold(job.Format, "dash") {
		master := BuildMasterPlaylistFor(job.Ladder)
		tmp := filepath.Join(job.OutputDir, "."+job.MasterName+".part")
		if err := os.WriteFile(tmp, master, 0o644); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, filepath.Join(job.OutputDir, job.MasterName)); err != nil {
			_ = os.Remove(tmp)
			return nil, err
		}
	}
	return o.Runner.Start(ctx, job)
}

// hlsVideoCodec is the video codec the master playlist names: H.264 High profile
// (rungArgs asks every encoder for it), level 4.2, the most any rung may need (1080p
// at 60 fps). It was Main 4.0 (avc1.4d4028), which libx264 and VideoToolbox never
// wrote (both default to High) and which under-states a 1080p60 rung: a player that
// takes the CODECS at its word (MSE's isTypeSupported, a TV's decoder setup) could
// refuse or mis-set up the stream. Naming a higher level than the stream's is safe.
const hlsVideoCodec = "avc1.64002a"

// BuildMasterPlaylistFor emits the §4.3 HLS master playlist for the
// ladder. Kept here (not in handlers) so the ffmpeg package has no
// import cycle with handlers; handlers.BuildMasterPlaylist delegates.
func BuildMasterPlaylistFor(ladder []Rendition) []byte {
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	sb.WriteString("#EXT-X-VERSION:6\n")
	sb.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i, r := range ladder {
		fmt.Fprintf(&sb,
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\""+hlsVideoCodec+",mp4a.40.2\"\n",
			r.BitrateKbps*1000, r.Width, r.Height,
		)
		// FFmpeg's HLS muxer names the variant folders by variant index
		// (HLSArgs: <out>/v%v/index.m3u8), not by rung name: a capped
		// ladder (only the 480p rung, Name "v2") is still written to v0/.
		fmt.Fprintf(&sb, "v%d/index.m3u8\n", i)
	}
	return []byte(sb.String())
}

// fallbackWindow: a run that fails this soon after it started is run again,
// the next way down: a hardware run in software (a source or a format the GPU
// will not take, a session the driver would not open), a tone-mapped software
// run without the tone-mapping (a colour space zscale cannot convert).
var fallbackWindow = 30 * time.Second

// probeTimeout bounds the source's colour probe at a session's open (the API
// gives the open 5 s in all).
const probeTimeout = 3 * time.Second

// execRunner is the production Runner: it builds the HLS/DASH arg list
// and Spawns ffmpeg. The child writes variant playlists + segments into
// job.OutputDir; the reaper kills it on idle via the returned Handle.
//
// A hardware verdict takes one session per rung from its gate, at once or
// not at all (the highest rungs first; the rungs it gets none for are
// encoded by libx264), and gives them back when ffmpeg exits: VOD never
// takes a session the TV's playout here counts on. A hardware run that
// fails within fallbackWindow is run again the next way down (fallbackWindow);
// when the driver refused the session, the gate pauses the hardware encoder
// for everyone.
type execRunner struct {
	Bin    Binary
	Gates  map[HWAccel]SessionGate
	Logger *slog.Logger // nil: slog's default

	// what tests replace: the source's colour, and the filters this ffmpeg has
	colour  func(ctx context.Context, ffprobe, input string) (Colour, error)
	filters func(ctx context.Context, ffmpeg string) (map[string]bool, error)
	// poll and stall override outputPoll and outputStall (the tests)
	poll, stall time.Duration
	// toneMapMu guards canToneMap, known once a look at the filters succeeded
	toneMapMu  sync.Mutex
	toneKnown  bool
	canToneMap bool
}

// toneMapFor is the tone-mapping an HDR source gets (ToneMapFilters, at most
// the ladder's top rung), when this ffmpeg has zscale and tonemap; "" for SDR,
// or without them (the 8-bit cut as before). A probe that failed counts the
// source SDR, logged (the catalogue keeps no colour to fall back on).
func (e *execRunner) toneMapFor(ctx context.Context, job Job) string {
	colour := e.colour
	if colour == nil {
		colour = sourceColour
	}
	ffprobe := e.Bin.FFprobe
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}
	var c Colour
	var err error
	if job.Colour != nil {
		c = *job.Colour
	} else {
		c, err = colour(ctx, ffprobe, job.InputPath)
	}
	if err != nil {
		e.logger().Warn("transcode: the source's colour probe failed, encoding it as SDR",
			"session_id", job.SessionID, "input", job.InputPath, "err", err.Error(), "event", "vod_colour_probe_failed")
	}
	if !c.HDR() || len(job.Ladder) == 0 || !e.hasToneMap(ctx) {
		return ""
	}
	top := job.Ladder[0]
	for _, r := range job.Ladder[1:] {
		if r.Width*r.Height > top.Width*top.Height {
			top = r
		}
	}
	return ToneMapFilters(c, top.Width, top.Height)
}

// hasToneMap: this ffmpeg has zscale and tonemap; asked again until a look at
// its filters succeeds (one that failed does not turn tone-mapping off).
func (e *execRunner) hasToneMap(ctx context.Context) bool {
	e.toneMapMu.Lock()
	defer e.toneMapMu.Unlock()
	if !e.toneKnown {
		list := e.filters
		if list == nil {
			list = listFilters
		}
		have, err := list(ctx, e.ffmpeg())
		if err != nil {
			e.logger().Warn("transcode: listing ffmpeg's filters failed, an HDR source is not tone-mapped",
				"err", err.Error(), "event", "vod_filters_list_failed")
			return false
		}
		e.toneKnown, e.canToneMap = true, have["zscale"] && have["tonemap"]
	}
	return e.canToneMap
}

func (e *execRunner) logger() *slog.Logger {
	if e.Logger == nil {
		return slog.Default()
	}
	return e.Logger
}

func (e *execRunner) ffmpeg() string {
	if e.Bin.FFmpeg == "" {
		return "ffmpeg"
	}
	return e.Bin.FFmpeg
}

// sourceColour is the colour of the source's first video frame as ffprobe
// decodes it, the stream's where the frame names none: the frame's is there for
// MPEG-TS (whose stream values ffprobe prints twice) and for an MKV whose
// Colour element names only the matrix (FFmpeg 8.1 then reports the stream's
// transfer as unknown). The error when ffprobe fails or takes over
// probeTimeout.
func sourceColour(ctx context.Context, ffprobe, input string) (Colour, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0", "-read_intervals", "%+#1",
		"-show_entries", "frame=color_transfer,color_space,color_primaries,color_range:"+
			"stream=color_transfer,color_space,color_primaries,color_range",
		"-of", "json", input).Output()
	if err != nil {
		if ctx.Err() != nil {
			return Colour{}, fmt.Errorf("ffprobe: %w", ctx.Err())
		}
		return Colour{}, fmt.Errorf("ffprobe: %w", err)
	}
	return parseColour(out), nil
}

// CatalogueColour is the source's colour from what the pipeline's probe stored
// (the video stream's fields and pixel format), and whether it settles HDR or not
// without reading the file again: a named transfer does; none named on an 8-bit
// picture is SDR (PQ and HLG need 10 bits or more). Otherwise (none named, a
// higher or unknown depth: an MKV whose transfer only the frames name) the file
// is probed at the session's open, as before.
func CatalogueColour(transfer, matrix, primaries, rng, pixFmt string) (Colour, bool) {
	named := func(v string) string {
		switch v {
		case "unknown", "unspecified", "reserved":
			return ""
		}
		return v
	}
	c := Colour{Transfer: named(transfer), Matrix: named(matrix), Primaries: named(primaries), Range: named(rng)}
	if c.Transfer != "" || eightBit[pixFmt] {
		return c, true
	}
	return Colour{}, false
}

// eightBit: pixel formats of 8 bits a component.
var eightBit = map[string]bool{
	"yuv420p": true, "yuvj420p": true, "yuv422p": true, "yuvj422p": true, "yuv444p": true, "yuvj444p": true,
	"yuv440p": true, "yuvj440p": true, "yuv411p": true, "yuv410p": true, "nv12": true, "nv16": true, "nv21": true,
	"gray": true, "rgb24": true, "bgr24": true, "rgba": true, "bgra": true, "argb": true, "abgr": true, "pal8": true,
}

func parseColour(out []byte) Colour {
	type colour struct {
		Transfer  string `json:"color_transfer"`
		Matrix    string `json:"color_space"`
		Primaries string `json:"color_primaries"`
		Range     string `json:"color_range"`
	}
	var doc struct {
		Frames  []colour `json:"frames"`
		Streams []colour `json:"streams"`
	}
	if json.Unmarshal(out, &doc) != nil {
		return Colour{}
	}
	pick := func(get func(colour) string) string {
		for _, list := range [][]colour{doc.Frames, doc.Streams} {
			for _, c := range list {
				switch v := get(c); v {
				case "", "unknown", "unspecified", "reserved":
				default:
					return v
				}
			}
		}
		return ""
	}
	return Colour{
		Transfer:  pick(func(c colour) string { return c.Transfer }),
		Matrix:    pick(func(c colour) string { return c.Matrix }),
		Primaries: pick(func(c colour) string { return c.Primaries }),
		Range:     pick(func(c colour) string { return c.Range }),
	}
}

// listFilters is the filters `ffmpeg -filters` names.
func listFilters(ctx context.Context, ffmpeg string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-filters").Output()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		// " TS. zscale  V->V  Apply resizing, colorspace and bit depth conversion."
		if f := strings.Fields(l); len(f) >= 3 && strings.Contains(f[2], "->") {
			have[f[1]] = true
		}
	}
	return have, nil
}

func (e *execRunner) Start(ctx context.Context, job Job) (*Handle, error) {
	// HLB-328 C1: derive a session-scoped context from Background, NOT
	// from the inbound request ctx. grpc-go cancels the request ctx the
	// instant OpenSession returns; if exec.CommandContext were parented
	// to it, ffmpeg would be SIGKILLed within ms — no variant playlist
	// or segments would ever be produced and the client would poll
	// forever. The encoder's lifetime is owned by the Handle and torn
	// down only via Handle.Stop (reaper / CloseSession / store Close).
	// The request ctx bounds only what runs before the spawn (the source's
	// HDR probe, the GPU sessions' sync with the TV's slots: their open
	// stays within its deadline); request-scoped validation happens
	// upstream in Orchestrator.Start.
	h := &Handle{}
	job.toneMap = e.toneMapFor(ctx, job)
	if err := e.run(ctx, h, job, job.HWAccel); err != nil {
		return nil, err
	}
	return h, nil
}

// run spawns job's encoder for the verdict hw into h, under a session-scoped
// context (see Start; ctx bounds only taking the gate's sessions), and watches
// it: the gate's sessions go back when it exits, and a hardware run that failed
// early is run again in software.
func (e *execRunner) run(ctx context.Context, h *Handle, job Job, hw string) error {
	rungs := len(job.Ladder)
	hwRungs := 0
	var releases []func(refused bool)
	if hw != "" {
		hwRungs = rungs
		if g := e.Gates[HWAccel(hw)]; g != nil {
			owner := sessionOwner(job.SessionID)
			if b, ok := g.(SessionBatch); ok {
				releases = b.AcquireN(ctx, owner, rungs)
			} else {
				for i := 0; i < rungs; i++ {
					release, ok := g.Acquire(ctx, owner, 0)
					if !ok {
						break
					}
					releases = append(releases, release)
				}
			}
			hwRungs = len(releases)
		}
	}
	giveBack := func(refused bool) {
		for _, r := range releases {
			r(refused)
		}
	}
	plan := PlanFor(hw, hwRungs, rungs, job.SourceCodec, !job.NoAudio)
	plan.ToneMap = job.toneMap
	plan.Start = job.StartSec
	var args Args
	if strings.EqualFold(job.Format, "dash") {
		args = DASHArgs(job.InputPath, job.OutputDir, job.Ladder, plan)
	} else {
		args = HLSArgs(job.InputPath, job.OutputDir, job.MasterName, job.Ladder, plan)
	}
	procCtx, procCancel := context.WithCancel(context.Background())
	proc, err := Spawn(procCtx, e.Bin, args)
	if err != nil {
		procCancel()
		giveBack(false)
		return err
	}
	if !h.set(proc, procCancel) {
		procCancel()
		_ = proc.Stop(context.Background())
		giveBack(false)
		h.exited(errStopped)
		return nil
	}
	started := time.Now()
	poll, stall := e.stallLimits()
	go e.watchOutput(proc, job, hw, started, poll, stall)
	go func() {
		werr := proc.Wait()
		stderr := proc.stderrAfterExit(2 * time.Second)
		giveBack(hwRungs > 0 && refusedSession(stderr))
		e.logExit(job, hw, werr, stderr, time.Since(started), h.isStopped())
		if werr == nil || time.Since(started) >= fallbackWindow {
			h.exited(werr)
			return
		}
		// the next way down: the GPU's run in software (tone-mapped as it was),
		// a tone-mapped software run without the tone-mapping
		next := job
		switch {
		case hwRungs > 0:
		case job.toneMap != "":
			next.toneMap = ""
		default:
			h.exited(werr)
			return
		}
		if !h.restart() {
			h.exited(werr)
			return
		}
		e.logger().Warn("vod transcode failed early: running it again in software",
			"session_id", job.SessionID, "from_hwaccel", hw, "tone_map", next.toneMap,
			"event", "vod_transcode_fallback")
		if err := e.run(context.Background(), h, next, ""); err != nil {
			e.logger().Error("vod transcode fallback did not start",
				"session_id", job.SessionID, "err", err.Error(), "event", "vod_transcode_fallback_failed")
			h.mu.Lock()
			h.restarting = false
			h.mu.Unlock()
			h.exited(err)
		}
	}()
	return nil
}

// outputStall is how long an encoder may run without writing anything (a segment,
// a playlist) before it is taken for stuck and stopped. A VOD encode never waits
// on its reader, so a run that writes nothing is one whose input stopped answering
// (a NAS read that hangs): the player would wait on a playlist that never grows
// until the idle reap. Stopped, its playlist answers 410 and the player reopens.
// The slowest real encode (4K HEVC in software on a weak CPU) still writes a 6 s
// segment well inside it. execRunner.stall overrides it (the tests).
const outputStall = 2 * time.Minute

// outputPoll is how often watchOutput looks at the output (execRunner.poll overrides).
const outputPoll = 10 * time.Second

// stallLimits are the runner's output poll and stall limit.
func (e *execRunner) stallLimits() (poll, stall time.Duration) {
	poll, stall = outputPoll, outputStall
	if e.poll > 0 {
		poll = e.poll
	}
	if e.stall > 0 {
		stall = e.stall
	}
	return poll, stall
}

// watchOutput stops proc when its output has not changed for outputStall, logged
// as `vod_transcode_stalled`; it returns when proc exits.
func (e *execRunner) watchOutput(proc *Process, job Job, hw string, started time.Time, poll, stall time.Duration) {
	t := time.NewTicker(poll)
	defer t.Stop()
	last := started
	for range t.C {
		if !proc.Active() {
			return
		}
		if w := newestWrite(job.OutputDir); w.After(last) {
			last = w
		}
		if idle := time.Since(last); idle >= stall {
			e.logger().Error("vod transcode wrote nothing for too long: its input is likely stuck; stopping it",
				"session_id", job.SessionID, "input", job.InputPath, "hwaccel", hw,
				"idle_ms", idle.Milliseconds(), "ran_ms", time.Since(started).Milliseconds(),
				"event", "vod_transcode_stalled")
			if err := proc.Stop(context.Background()); err != nil {
				e.logger().Error("vod transcode did not stop", "session_id", job.SessionID, "pid", proc.PID(),
					"err", err.Error(), "event", "vod_transcode_stop_failed")
			}
			return
		}
	}
}

// newestWrite is the latest modification time of a file under dir (zero for none).
func newestWrite(dir string) time.Time {
	var newest time.Time
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a file that went (a deleted segment) is no write
		}
		if info, ierr := d.Info(); ierr == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}

// logExit writes how a session's encoder ended: `vod_transcode_exited`, Info
// when it finished or was stopped, Warn with its exit status and the last of
// its stderr when it failed (the reason a player's playlist stopped growing).
func (e *execRunner) logExit(job Job, hw string, werr error, stderr string, ran time.Duration, stopped bool) {
	outcome := "failed"
	switch {
	case stopped:
		outcome = "stopped"
	case werr == nil:
		outcome = "finished"
	}
	attrs := []any{
		"session_id", job.SessionID,
		"input", job.InputPath,
		"hwaccel", hw,
		"start_sec", job.StartSec,
		"outcome", outcome,
		"ran_ms", ran.Milliseconds(),
	}
	if outcome != "failed" {
		e.logger().Info("vod transcode exited", append(attrs, "event", "vod_transcode_exited")...)
		return
	}
	code := -1
	var xe *exec.ExitError
	if errors.As(werr, &xe) {
		code = xe.ExitCode()
	}
	e.logger().Warn("vod transcode failed", append(attrs,
		"exit_code", code,
		"err", werr.Error(),
		"stderr", tailLines(stderr, stderrLogLines),
		"event", "vod_transcode_exited")...)
}

// stderrLogLines is how much of a failed encoder's stderr the log keeps.
const stderrLogLines = 15

// tailLines is the last n non-empty lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return strings.Join(out, "\n")
}

// refusedSession: ffmpeg's stderr says the driver would not open an NVENC
// session for want of one (the card's sessions are all taken, here or by
// another program); "unsupported device" is a card without NVENC.
func refusedSession(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "openencodesessionex failed") && !strings.Contains(s, "unsupported device")
}

// sessionOwner is the gate's owner for a session id (a UUID, else one made
// from it).
func sessionOwner(id string) uuid.UUID {
	if u, err := uuid.Parse(id); err == nil {
		return u
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(id))
}

// NewHandleForTest builds a Handle wired to caller-supplied closures —
// the fake Runner uses this so unit tests never exec ffmpeg.
func NewHandleForTest(stop func(context.Context) error, pid func() int, active func() bool) *Handle {
	return &Handle{stopFn: stop, pidFn: pid, actFn: active}
}
