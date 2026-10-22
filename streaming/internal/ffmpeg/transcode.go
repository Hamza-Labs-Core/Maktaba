package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	stopFn              func(context.Context) error
	pidFn               func() int
	actFn               func() bool
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
	if h.stopFn != nil {
		return h.stopFn(ctx)
	}
	return nil
}

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
// (nil: uncounted). main.go wires this; tests swap Runner.
func DefaultOrchestrator(bin Binary, gates map[HWAccel]SessionGate) *Orchestrator {
	return &Orchestrator{Runner: &execRunner{Bin: bin, Gates: gates}}
}

// SessionGate is a hardware encoder's session budget, shared with the TV's
// playout in this process (channel.EncoderGate): a GeForce card opens only a
// few NVENC sessions, and every ladder rung on NVENC is one.
type SessionGate interface {
	Acquire(ctx context.Context, owner uuid.UUID, wait time.Duration) (release func(refused bool), ok bool)
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
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.4d4028,mp4a.40.2\"\n",
			r.BitrateKbps*1000, r.Width, r.Height,
		)
		// FFmpeg's HLS muxer names the variant folders by variant index
		// (HLSArgs: <out>/v%v/index.m3u8), not by rung name: a capped
		// ladder (only the 480p rung, Name "v2") is still written to v0/.
		fmt.Fprintf(&sb, "v%d/index.m3u8\n", i)
	}
	return []byte(sb.String())
}

// fallbackWindow: a hardware run that fails this soon after it started is
// run again in software (a source or a format the GPU will not take, a
// session the driver would not open).
var fallbackWindow = 30 * time.Second

// execRunner is the production Runner: it builds the HLS/DASH arg list
// and Spawns ffmpeg. The child writes variant playlists + segments into
// job.OutputDir; the reaper kills it on idle via the returned Handle.
//
// A hardware verdict takes one session per rung from its gate, at once or
// not at all (the highest rungs first; the rungs it gets none for are
// encoded by libx264), and gives them back when ffmpeg exits: VOD never
// takes a session the TV's playout here counts on. A hardware run that
// fails within fallbackWindow is run again in software; when the driver
// refused the session, the gate pauses the hardware encoder for everyone.
type execRunner struct {
	Bin   Binary
	Gates map[HWAccel]SessionGate
}

func (e *execRunner) Start(ctx context.Context, job Job) (*Handle, error) {
	// HLB-328 C1: derive a session-scoped context from Background, NOT
	// from the inbound request ctx. grpc-go cancels the request ctx the
	// instant OpenSession returns; if exec.CommandContext were parented
	// to it, ffmpeg would be SIGKILLed within ms — no variant playlist
	// or segments would ever be produced and the client would poll
	// forever. The encoder's lifetime is owned by the Handle and torn
	// down only via Handle.Stop (reaper / CloseSession / store Close).
	// The request ctx is intentionally unused here (request-scoped
	// validation happens upstream in Orchestrator.Start).
	_ = ctx
	h := &Handle{}
	if err := e.run(h, job, job.HWAccel); err != nil {
		return nil, err
	}
	return h, nil
}

// run spawns job's encoder for the verdict hw into h, under a session-scoped
// context (see Start), and watches it: the gate's sessions go back when it
// exits, and a hardware run that failed early is run again in software.
func (e *execRunner) run(h *Handle, job Job, hw string) error {
	rungs := len(job.Ladder)
	hwRungs := 0
	var releases []func(refused bool)
	if hw != "" {
		hwRungs = rungs
		if g := e.Gates[HWAccel(hw)]; g != nil {
			hwRungs = 0
			owner := sessionOwner(job.SessionID)
			for i := 0; i < rungs; i++ {
				release, ok := g.Acquire(context.Background(), owner, 0)
				if !ok {
					break
				}
				releases = append(releases, release)
				hwRungs++
			}
		}
	}
	giveBack := func(refused bool) {
		for _, r := range releases {
			r(refused)
		}
	}
	plan := PlanFor(hw, hwRungs, rungs, job.SourceCodec, !job.NoAudio)
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
		return nil
	}
	started := time.Now()
	go func() {
		werr := proc.Wait()
		giveBack(hwRungs > 0 && refusedSession(proc.stderrAfterExit(2*time.Second)))
		if werr == nil || hwRungs == 0 || time.Since(started) >= fallbackWindow || !h.restart() {
			return
		}
		if err := e.run(h, job, ""); err != nil {
			h.mu.Lock()
			h.restarting = false
			h.mu.Unlock()
		}
	}()
	return nil
}

// refusedSession: ffmpeg's stderr says the driver would not open an NVENC
// session (the card's sessions are all taken, here or by another program).
func refusedSession(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "openencodesessionex failed")
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
