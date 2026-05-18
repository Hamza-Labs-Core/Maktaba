package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	proc       *Process
	procCancel context.CancelFunc // cancels the session-scoped encoder ctx
	stopFn     func(context.Context) error
	pidFn      func() int
	actFn      func() bool
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
	if h.procCancel != nil {
		h.procCancel()
	}
	if h.proc != nil {
		return h.proc.Stop(ctx)
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
	if h.proc != nil {
		return h.proc.PID()
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
	if h.proc != nil {
		return h.proc.Active()
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
// ffmpeg-spawning Runner. main.go wires this; tests swap Runner.
func DefaultOrchestrator(bin Binary) *Orchestrator {
	return &Orchestrator{Runner: &execRunner{Bin: bin}}
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
	for _, r := range ladder {
		sb.WriteString(fmt.Sprintf(
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.4d4028,mp4a.40.2\"\n",
			r.BitrateKbps*1000, r.Width, r.Height,
		))
		sb.WriteString(r.Name + "/index.m3u8\n")
	}
	return []byte(sb.String())
}

// execRunner is the production Runner: it builds the HLS/DASH arg list
// and Spawns ffmpeg. The child writes variant playlists + segments into
// job.OutputDir; the reaper kills it on idle via the returned Handle.
type execRunner struct{ Bin Binary }

func (e *execRunner) Start(ctx context.Context, job Job) (*Handle, error) {
	var args Args
	if strings.EqualFold(job.Format, "dash") {
		args = DASHArgs(job.InputPath, job.OutputDir, job.Ladder, job.HWAccel)
	} else {
		args = HLSArgs(job.InputPath, job.OutputDir, job.MasterName, job.Ladder, job.HWAccel)
	}
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
	procCtx, procCancel := context.WithCancel(context.Background())
	proc, err := Spawn(procCtx, e.Bin, args)
	if err != nil {
		procCancel()
		return nil, err
	}
	return &Handle{proc: proc, procCancel: procCancel}, nil
}

// NewHandleForTest builds a Handle wired to caller-supplied closures —
// the fake Runner uses this so unit tests never exec ffmpeg.
func NewHandleForTest(stop func(context.Context) error, pid func() int, active func() bool) *Handle {
	return &Handle{stopFn: stop, pidFn: pid, actFn: active}
}
