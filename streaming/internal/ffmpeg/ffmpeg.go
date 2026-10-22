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
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/procattr"
)

// stopGrace is how long Process.Stop waits after SIGTERM for ffmpeg to
// finalize the in-flight segment + variant playlist before escalating
// to SIGKILL. Kept small so idle-reap / CloseSession stays responsive;
// overridable (tests shrink it). HLB-328 I2: a bare Kill() truncated
// the current .ts/.m4s and left a stale variant playlist served to
// players on every reap/close.
var stopGrace = 5 * time.Second

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
	tail    *tailBuf
	// copied closes once stderr's copy into tail has ended (its writers
	// gone): Wait does not wait for it (see Spawn), stderrAfterExit does.
	copied chan struct{}
}

// Spawn starts a new FFmpeg process with args. Stdout goes to the null
// device; stderr is kept (its last 8 KiB, Stderr) — never an undrained pipe,
// which fills and stalls ffmpeg mid-encode. Cancel ctx to terminate.
func Spawn(ctx context.Context, bin Binary, args Args) (*Process, error) {
	if bin.FFmpeg == "" {
		bin.FFmpeg = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, bin.FFmpeg, args...)
	procattr.Tie(cmd) // dies with this process, however it dies (Story 22.9)
	p := &Process{cmd: cmd, tail: &tailBuf{max: 8 << 10}, copied: make(chan struct{})}
	// stderr through our own pipe (an *os.File is handed to the child as
	// is), read here into the tail: Wait never waits on the copy, so a
	// child that outlives ffmpeg (a wrapper script's) cannot hold Stop up.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, err
	}
	_ = pw.Close()
	go func() {
		_, _ = io.Copy(p.tail, pr)
		_ = pr.Close()
		close(p.copied)
	}()
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

// Stdout is drained by the process itself; kept for callers of the old
// accessor (always empty).
func (p *Process) Stdout() io.Reader { return strings.NewReader("") }

// Stderr returns the last 8 KiB ffmpeg wrote to stderr so far.
func (p *Process) Stderr() io.Reader { return strings.NewReader(p.tail.String()) }

// stderrAfterExit is the last of stderr once the process has exited: what it
// wrote just before exiting is read in, waiting up to wait for that (a child
// it left behind may hold the pipe open).
func (p *Process) stderrAfterExit(wait time.Duration) string {
	select {
	case <-p.copied:
	case <-time.After(wait):
	}
	return p.tail.String()
}

// tailBuf keeps the last max bytes written to it.
type tailBuf struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append([]byte(nil), t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// PID returns the OS pid (0 once exited).
func (p *Process) PID() int {
	if p.cmd.Process == nil || p.stopped.Load() {
		return 0
	}
	return p.cmd.Process.Pid
}

// Active reports whether the process is still running.
func (p *Process) Active() bool { return !p.stopped.Load() }

// Stop gracefully terminates the subprocess and waits for it to exit.
// Idempotent (the stopped guard).
//
// HLB-328 I2: send SIGTERM first so ffmpeg flushes and finalizes the
// current segment + variant playlist (a bare SIGKILL truncated the
// in-flight .ts/.m4s and left a stale playlist for players). Wait up to
// stopGrace for a clean exit; if the child is still alive, escalate to
// SIGKILL. The Spawn Wait()-goroutine remains the sole cmd.Wait()
// caller, so there is no double-wait and no zombie regardless of which
// signal actually reaps the child — we only observe its completion via
// p.wg here.
func (p *Process) Stop(ctx context.Context) error {
	if p.stopped.Load() {
		return nil
	}
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()

	if p.cmd.Process != nil {
		// SIGTERM: ask ffmpeg to finalize gracefully.
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(stopGrace):
		// Grace window elapsed — force kill.
	}

	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
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

// EncodePlan is how a transcode encodes (PlanFor): the -hwaccel that decodes the
// input ("" on the CPU), each rung's encoder, whether the source has an audio
// stream to carry, and ToneMap: the filters that bring an HDR source to SDR
// BT.709 before the ladder (ToneMapFilters; "" for SDR, or an ffmpeg without
// them).
type EncodePlan struct {
	Decode   string
	Encoders []string
	Audio    bool
	ToneMap  string
}

// segmentSeconds is the HLS and DASH segment length; every rung has a key frame
// at each multiple of it, so the rungs' segments start on the same frames.
const segmentSeconds = 6

// Colour is what a source's first frame says of its colour, in ffprobe's names
// (sourceColour): a PQ (HDR10, Dolby Vision's base layer) or HLG one is HDR.
type Colour struct {
	Transfer, Matrix, Primaries, Range string
}

// HDR: the transfer is PQ or HLG.
func (c Colour) HDR() bool { return c.Transfer == "smpte2084" || c.Transfer == "arib-std-b67" }

// ToneMapFilters bring an HDR source to SDR BT.709, in the ladder's top rung's
// size at most (maxW×maxH): first scaled down to it (tone-mapping 4K frames on
// the CPU was slower than real time, 1080p is twice it), then linear light at
// 100 nits from the source's own transfer, matrix and primaries (BT.2020 where
// it names none), its output primaries named too (zscale finds no conversion
// to or from an unspecified colour space),
// Möbius' curve (Hable's left a 100-nit picture at two thirds of its
// brightness), and BT.709 limited range 4:2:0. Without it an HDR source came
// out washed out: its 10-bit samples only cut to 8 bits.
func ToneMapFilters(c Colour, maxW, maxH int) string {
	transfer := c.Transfer // "smpte2084" or "arib-std-b67": zscale's names too
	matrix := map[string]string{"bt2020c": "2020_cl", "bt709": "709", "smpte170m": "170m", "bt470bg": "470bg"}[c.Matrix]
	if matrix == "" {
		matrix = "2020_ncl"
	}
	primaries := map[string]string{"bt709": "709", "smpte170m": "170m", "smpte431": "smpte431", "smpte432": "smpte432"}[c.Primaries]
	if primaries == "" {
		primaries = "2020"
	}
	rng := "limited"
	if c.Range == "pc" || c.Range == "jpeg" {
		rng = "full"
	}
	return fmt.Sprintf("scale=w=min(iw\\,%d):h=min(ih\\,%d):force_original_aspect_ratio=decrease:force_divisible_by=2,", maxW, maxH) +
		"zscale=tin=" + transfer + ":min=" + matrix + ":pin=" + primaries + ":rin=" + rng + ":t=linear:npl=100:p=" + primaries + "," +
		"format=gbrpf32le,zscale=p=709,tonemap=tonemap=mobius:param=0.3:desat=0," +
		"zscale=t=709:m=709:r=limited,format=yuv420p"
}

// SoftwarePlan encodes every rung with libx264, decoding on the CPU.
func SoftwarePlan(rungs int, audio bool) EncodePlan {
	p := EncodePlan{Audio: audio}
	for i := 0; i < rungs; i++ {
		p.Encoders = append(p.Encoders, "libx264")
	}
	return p
}

// PlanFor is the plan for an encoder verdict (hwaccel, "" software) and the
// source's video codec: the first hwRungs rungs (the highest) on the verdict's
// encoder, the rest on libx264, and the GPU decoding the input only when it is
// H.264 or HEVC (every NVDEC and VideoToolbox decodes those; AV1, VP9 and the
// rest are decoded on the CPU, as FFmpeg 7's native AV1 decoder does not fall
// back to software from a GPU that lacks it).
func PlanFor(hwaccel string, hwRungs, rungs int, sourceCodec string, audio bool) EncodePlan {
	p := SoftwarePlan(rungs, audio)
	enc := encoderForHWAccel(hwaccel)
	if enc == "libx264" || hwRungs <= 0 {
		return p
	}
	for i := 0; i < rungs && i < hwRungs; i++ {
		p.Encoders[i] = enc
	}
	switch strings.ToLower(sourceCodec) {
	case "h264", "avc", "avc1", "hevc", "h265", "hev1", "hvc1":
		p.Decode = decodeHWAccel(hwaccel)
	}
	return p
}

// rungArgs are one ladder rung's video output options: its encoder, bitrate,
// size, 8-bit 4:2:0 (a 10-bit or 4:2:2 source becomes what NVENC's H.264 and
// browsers take: libx264 would otherwise keep High 10, which few play), the
// encoder's own preset, and a key frame (IDR on NVENC) at every segment
// boundary: the rungs' segments align, as the DASH manifest says they do
// (libx264 alone cut at its scene changes, segments of 7 to 15 s).
func rungArgs(i int, rung Rendition, enc string, p EncodePlan) Args {
	n := itoa(i)
	video := "0:v:0"
	if p.ToneMap != "" {
		video = "[v" + n + "]" // one of the tone-mapped split's outputs (videoGraph)
	}
	a := Args{
		"-map", video,
		"-c:v:" + n, enc,
		"-b:v:" + n, itoa(rung.BitrateKbps) + "k",
		"-s:v:" + n, fmt.Sprintf("%dx%d", rung.Width, rung.Height),
		"-pix_fmt:v:" + n, "yuv420p",
		"-force_key_frames:v:" + n, "expr:gte(t,n_forced*" + itoa(segmentSeconds) + ")",
	}
	if p := presetForEncoder(enc); p != "" {
		a = append(a, "-preset:v:"+n, p)
	}
	if enc == "h264_nvenc" {
		a = append(a, "-forced-idr:v:"+n, "1")
	}
	return a
}

func inputArgs(input string, p EncodePlan) Args {
	args := Args{"-y", "-hide_banner", "-loglevel", "warning"}
	if p.Decode != "" {
		args = append(args, "-hwaccel", p.Decode)
	}
	return append(args, "-i", input)
}

// videoGraph is the HDR source's tone-mapping, once, split into one output a
// rung ([v0], [v1], …): nothing for SDR.
func videoGraph(rungs int, p EncodePlan) Args {
	if p.ToneMap == "" || rungs == 0 {
		return nil
	}
	outs := ""
	for i := 0; i < rungs; i++ {
		outs += "[v" + itoa(i) + "]"
	}
	return Args{"-filter_complex", "[0:v:0]" + p.ToneMap + ",split=" + itoa(rungs) + outs}
}

func encoderAt(p EncodePlan, i int) string {
	if i < len(p.Encoders) && p.Encoders[i] != "" {
		return p.Encoders[i]
	}
	return "libx264"
}

// HLSArgs builds the FFmpeg ladder for Story 8.5: one variant per rung
// (-var_stream_map: <out>/v0, v1, … each with its index.m3u8 and segments,
// as the master playlist lists them), each with the source's audio when it
// has one. Each index is an EVENT playlist listing every segment, ended with
// #EXT-X-ENDLIST when the transcode is done: the viewer seeks anywhere already
// transcoded, and a transcode running ahead of playback drops nothing the
// player has yet to fetch. The segments stay until the session's folder goes
// (the session closed: purgeVODSessions).
func HLSArgs(input, outputDir, masterName string, ladder []Rendition, p EncodePlan) Args {
	args := append(inputArgs(input, p), videoGraph(len(ladder), p)...)
	variants := make([]string, len(ladder))
	for i, rung := range ladder {
		args = append(args, rungArgs(i, rung, encoderAt(p, i), p)...)
		variants[i] = "v:" + itoa(i)
		if p.Audio {
			variants[i] += ",a:" + itoa(i)
		}
	}
	if p.Audio {
		for range ladder {
			args = append(args, "-map", "0:a:0")
		}
		args = append(args, "-c:a", "aac", "-b:a", "128k")
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", itoa(segmentSeconds),
		"-hls_playlist_type", "event",
		"-hls_list_size", "0",
		"-hls_flags", "independent_segments",
		"-var_stream_map", strings.Join(variants, " "),
		"-hls_segment_filename", outputDir+"/v%v/seg-%d.ts",
		"-master_pl_name", masterName,
		outputDir+"/v%v/index.m3u8",
	)
	return args
}

// DASHArgs builds Story 8.6's DASH-only invocation. Note the §4.3
// callout: emitting both HLS and DASH from one encode isn't supported,
// so DASH is opt-in per session. The MPD lists every segment (no window):
// dynamic while the transcode runs, static once it is done.
func DASHArgs(input, outputDir string, ladder []Rendition, p EncodePlan) Args {
	args := append(inputArgs(input, p), videoGraph(len(ladder), p)...)
	for i, rung := range ladder {
		args = append(args, rungArgs(i, rung, encoderAt(p, i), p)...)
	}
	// the ladder is one AdaptationSet (a player switches between its
	// Representations), the audio another; by default each stream was its own
	sets := "id=0,streams=v"
	if p.Audio {
		args = append(args, "-map", "0:a:0", "-c:a", "aac", "-b:a", "128k")
		sets += " id=1,streams=a"
	}
	args = append(args,
		"-f", "dash",
		"-adaptation_sets", sets,
		"-seg_duration", itoa(segmentSeconds),
		"-window_size", "0",
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

// decodeHWAccel is the -hwaccel that decodes the input for an encoder
// verdict: CUDA (NVDEC) beside NVENC, VideoToolbox on a Mac. FFmpeg has no
// "nvenc" hwaccel (that is the encoder): `-hwaccel nvenc` made every NVIDIA
// transcode fail at once. None for QuickSync (its decoding wants the qsv
// decoders and a device; the encoder takes frames from memory) or software.
// The decoded frames come back to memory (no -hwaccel_output_format), so the
// ladder's scaling works as in software. PlanFor asks for it only for H.264
// and HEVC sources.
func decodeHWAccel(hwaccel string) string {
	switch strings.ToLower(hwaccel) {
	case "videotoolbox":
		return "videotoolbox"
	case "nvenc":
		return "cuda"
	}
	return ""
}

// presetForEncoder is the encoder's own speed preset: veryfast for libx264
// and QuickSync, p4 for NVENC (its presets are p1..p7 since FFmpeg 4.3; it
// refuses "veryfast"), none for VideoToolbox (it has no -preset).
func presetForEncoder(encoder string) string {
	switch encoder {
	case "libx264", "h264_qsv":
		return "veryfast"
	case "h264_nvenc":
		return "p4"
	}
	return ""
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
