// Package ffmpeg wraps the ffmpeg subprocess management used by
// stories 8.4 (remux), 8.5 (HLS transcode), 8.6 (DASH manifest), and
// 8.7 (hwaccel detection).
//
// All FFmpeg work is shell-out: we never link libav directly.
// Subprocess lifecycle is owned by Process which the session store
// pins to a session id; the reaper kills it when the player goes idle.
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Binary configures which executable to run. Defaults to "ffmpeg" on
// the operator's PATH; overridable via streaming.toml.
type Binary struct {
	FFmpeg  string
	FFprobe string
}

// DefaultBinary returns the convention default. Tests inject
// mock binaries via t.TempDir() shell scripts.
func DefaultBinary() Binary { return Binary{FFmpeg: "ffmpeg", FFprobe: "ffprobe"} }

// Args is a stringly-typed arg list. Callers build via the helpers
// below (RemuxArgs, HLSArgs, DASHArgs); we never accept user input
// directly.
type Args []string

// Process is a running FFmpeg child. Stop is idempotent, safe from
// any goroutine. PID returns 0 once stopped.
type Process struct {
	cmd     *exec.Cmd
	stopped atomic.Bool
	mu      sync.Mutex
	wg      sync.WaitGroup
	exitErr error
	stdout  io.ReadCloser
	stderr  io.ReadCloser
}

// Spawn starts a new FFmpeg process with args. Stdout/stderr are
// piped to the caller for log scraping. Cancel ctx to terminate.
func Spawn(ctx context.Context, bin Binary, args Args) (*Process, error) {
	if bin.FFmpeg == "" {
		bin.FFmpeg = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, bin.FFmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Process{cmd: cmd, stdout: stdout, stderr: stderr}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		err := cmd.Wait()
		p.mu.Lock()
		p.exitErr = err
		p.stopped.Store(true)
		p.mu.Unlock()
	}()
	return p, nil
}

// Stdout returns the stdout pipe.
func (p *Process) Stdout() io.Reader { return p.stdout }

// Stderr returns the stderr pipe.
func (p *Process) Stderr() io.Reader { return p.stderr }

// PID returns the OS pid (0 once exited).
func (p *Process) PID() int {
	if p.cmd.Process == nil || p.stopped.Load() {
		return 0
	}
	return p.cmd.Process.Pid
}

// Active reports whether the process is still running.
func (p *Process) Active() bool { return !p.stopped.Load() }

// Stop kills the subprocess and waits for it to exit. Idempotent.
func (p *Process) Stop(ctx context.Context) error {
	if p.stopped.Load() {
		return nil
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return errors.New("ffmpeg: stop timeout")
	}
}

// Wait blocks until the process exits, returning the exit error.
func (p *Process) Wait() error {
	p.wg.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

// RemuxArgs builds the ffmpeg invocation for Story 8.4 — same codecs,
// new container, no re-encode. The output is mapped to fragmented MP4
// suitable for ranged direct play.
func RemuxArgs(input, output string) Args {
	return Args{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-i", input,
		"-c", "copy",
		"-movflags", "+faststart+frag_keyframe+empty_moov",
		"-f", "mp4",
		output,
	}
}

// HLSArgs builds the FFmpeg ladder for Story 8.5. We emit a master
// playlist and N variant playlists; segments live in the session's
// per-rendition folder. Hwaccel is applied if present.
func HLSArgs(input, outputDir, masterName string, ladder []Rendition, hwaccel string) Args {
	args := Args{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
	}
	if hwaccel != "" {
		args = append(args, "-hwaccel", hwaccel)
	}
	args = append(args, "-i", input)

	// Map streams: one video output per ladder rung, one audio.
	for i, rung := range ladder {
		args = append(args,
			"-map", "0:v:0",
			"-c:v:"+itoa(i), encoderForHWAccel(hwaccel),
			"-b:v:"+itoa(i), itoa(rung.BitrateKbps)+"k",
			"-s:v:"+itoa(i), fmt.Sprintf("%dx%d", rung.Width, rung.Height),
			"-preset:v:"+itoa(i), "veryfast",
		)
	}
	args = append(args,
		"-map", "0:a:0",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_list_size", "6",
		"-hls_flags", "delete_segments+independent_segments",
		"-hls_segment_filename", outputDir+"/v%v/seg-%d.ts",
		"-master_pl_name", masterName,
		outputDir+"/v%v/index.m3u8",
	)
	return args
}

// DASHArgs builds Story 8.6's DASH-only invocation. Note the §4.3
// callout: emitting both HLS and DASH from one encode isn't supported,
// so DASH is opt-in per session.
func DASHArgs(input, outputDir string, ladder []Rendition, hwaccel string) Args {
	args := Args{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
	}
	if hwaccel != "" {
		args = append(args, "-hwaccel", hwaccel)
	}
	args = append(args, "-i", input)

	for i, rung := range ladder {
		args = append(args,
			"-map", "0:v:0",
			"-c:v:"+itoa(i), encoderForHWAccel(hwaccel),
			"-b:v:"+itoa(i), itoa(rung.BitrateKbps)+"k",
			"-s:v:"+itoa(i), fmt.Sprintf("%dx%d", rung.Width, rung.Height),
		)
	}
	args = append(args,
		"-map", "0:a:0",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "dash",
		"-seg_duration", "6",
		"-window_size", "10",
		"-extra_window_size", "5",
		"-init_seg_name", "init-$RepresentationID$.m4s",
		"-media_seg_name", "chunk-$RepresentationID$-$Number$.m4s",
		outputDir+"/manifest.mpd",
	)
	return args
}

// Rendition is one rung of the ABR ladder.
type Rendition struct {
	Name        string // "v0", "v1", "v2"
	Height      int
	Width       int
	BitrateKbps int
	Codec       string // "h264" by default; "h265" for HEVC ladders
}

// DefaultLadder returns the §4.3 default rungs trimmed to the matrix
// bitrate cap. Order is highest → lowest so consumers can iterate to
// find the highest rung that fits.
func DefaultLadder(maxBitrateKbps int) []Rendition {
	full := []Rendition{
		{Name: "v0", Height: 1080, Width: 1920, BitrateKbps: 6000, Codec: "h264"},
		{Name: "v1", Height: 720, Width: 1280, BitrateKbps: 3000, Codec: "h264"},
		{Name: "v2", Height: 480, Width: 854, BitrateKbps: 1200, Codec: "h264"},
	}
	if maxBitrateKbps <= 0 {
		return full
	}
	out := make([]Rendition, 0, len(full))
	for _, r := range full {
		if r.BitrateKbps <= maxBitrateKbps {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		// fall back to the lowest rung even if it exceeds the cap
		out = append(out, full[len(full)-1])
	}
	return out
}

// encoderForHWAccel maps an hwaccel verdict to the right encoder name.
func encoderForHWAccel(hwaccel string) string {
	switch strings.ToLower(hwaccel) {
	case "videotoolbox":
		return "h264_videotoolbox"
	case "nvenc":
		return "h264_nvenc"
	case "qsv":
		return "h264_qsv"
	default:
		return "libx264"
	}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
