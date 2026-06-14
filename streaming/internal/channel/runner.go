package channel

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
)

// execRunner is the production Runner + TSRunner: it spawns ffmpeg with
// the concat-demuxer input. main.go wires this; tests swap in fakes.
type execRunner struct {
	Bin ffmpeg.Binary
}

// NewExecRunner returns the real ffmpeg-spawning runner (Runner +
// TSRunner).
func NewExecRunner(bin ffmpeg.Binary) *execRunner { return &execRunner{Bin: bin} }

// StartHLS spawns the live-window encoder. The master playlist is written
// up front so the player's first manifest GET resolves to a 200 while the
// first variant segment is still being produced (mirrors the VOD
// orchestrator). The encoder is parented to a session-scoped context
// (NOT the request ctx) so it outlives the tune RPC — HLB-328.
func (e *execRunner) StartHLS(_ context.Context, job Job) (Controller, error) {
	if job.MasterName == "" {
		job.MasterName = defaultMasterName
	}
	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		return nil, err
	}
	master := ffmpeg.BuildMasterPlaylistFor(job.Ladder)
	tmp := filepath.Join(job.OutputDir, "."+job.MasterName+".part")
	if err := os.WriteFile(tmp, master, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(job.OutputDir, job.MasterName)); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	args := LiveHLSArgs(job.ConcatPath, job.OutputDir, job.MasterName, job.Ladder, job.HWAccel)
	procCtx := context.Background() // encoder lifetime owned by Controller.Stop
	return ffmpeg.Spawn(procCtx, e.Bin, args)
}

// StreamMPEGTS runs the continuous MPEG-TS mux, piping ffmpeg's stdout
// straight to `w`. It blocks until ffmpeg exits or `ctx` is cancelled
// (the HDHomeRun tuner disconnecting), matching real tuner semantics.
func (e *execRunner) StreamMPEGTS(ctx context.Context, concatPath string, w io.Writer) error {
	bin := e.Bin.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	args := MPEGTSArgs(concatPath, nil, "")
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	return cmd.Run()
}
