package channel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// StationConfig is everything a Station needs from the host.
type StationConfig struct {
	FFmpeg   string // ffmpeg binary
	Profile  OutputProfile
	Repo     Repo
	Prober   Prober
	Resolver LiveResolver
	PathMap  pathmap.Map
	FontFile string // for slate captions; "" → no caption
	Logger   *slog.Logger
	Now      func() time.Time
	// LiveRetry: how long a failed live block slates before retrying.
	LiveRetry time.Duration
}

// Station is one channel on air: the persistent muxer (HLS window +
// MPEG-TS fan-out) fed block by block by a Feeder.
type Station struct {
	id    uuid.UUID
	cfg   StationConfig
	dir   string
	epoch time.Time
	hub   *Hub
	log   *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mux     *exec.Cmd
	muxIn   io.WriteCloser
	writeMu sync.Mutex

	stopOnce sync.Once
	stopping chan struct{}

	// Player state (only touched by the feeder goroutine).
	lastEnd  float64 // timeline seconds the last block's output reached
	software bool    // NVENC failed on this station → libx264 from now on
	look     Look
	lookAt   time.Time
	blockLog *os.File
}

// lookTTL: how often the channel look is re-read (edits apply live).
const lookTTL = time.Minute

// StartStation cleans dir, starts the muxer and the feeder.
func StartStation(cfg StationConfig, channelID uuid.UUID, dir string) (*Station, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.FFmpeg == "" {
		cfg.FFmpeg = "ffmpeg"
	}
	cfg.Profile = cfg.Profile.normalised()
	if err := resetDir(dir); err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(dir, "master.m3u8"), []byte(MasterPlaylist(cfg.Profile))); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Station{
		id: channelID, cfg: cfg, dir: dir, hub: NewHub(0),
		log: cfg.Logger.With("channel_id", channelID.String()),
		ctx: ctx, cancel: cancel,
		done: make(chan struct{}), stopping: make(chan struct{}),
	}
	s.blockLog, _ = os.Create(filepath.Join(dir, "blocks.log"))

	mux := exec.Command(cfg.FFmpeg, MuxerArgs(cfg.Profile, dir)...)
	in, err := mux.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := mux.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if lf, err := os.Create(filepath.Join(dir, "mux.log")); err == nil {
		mux.Stderr = lf
		defer lf.Close() // the child holds its own handle
	}
	if err := mux.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("channel: start muxer: %w", err)
	}
	s.mux, s.muxIn = mux, in
	s.epoch = cfg.Now()

	go s.hub.pump(out)

	feeder := &Feeder{
		Source: cfg.Repo, ChannelID: channelID, Player: s, Now: cfg.Now, LiveRetry: cfg.LiveRetry,
		OnSegment: func(seg Segment) {
			s.log.Info("channel: on air", "kind", seg.Kind, "seq", seg.Block.Seq,
				"start", seg.Start.UTC().Format(time.RFC3339), "end", seg.End.UTC().Format(time.RFC3339),
				"synthetic", seg.Synthetic, "event", "channel_segment")
		},
	}
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		feeder.Run(ctx)
	}()

	go func() {
		err := mux.Wait()
		if ctx.Err() == nil {
			s.log.Warn("channel: muxer exited", "err", errString(err), "event", "channel_muxer_exit")
		}
		cancel()
		<-feederDone
		s.hub.Close()
		if s.blockLog != nil {
			_ = s.blockLog.Close()
		}
		close(s.done)
	}()
	s.log.Info("channel: station started", "encoder", cfg.Profile.Encoder,
		"profile", fmt.Sprintf("%dx%d@%d", cfg.Profile.Width, cfg.Profile.Height, cfg.Profile.FPS),
		"event", "channel_station_started")
	return s, nil
}

// Stop tears the station down (non-blocking; Done closes when finished).
func (s *Station) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopping)
		s.cancel()
		s.writeMu.Lock()
		_ = s.muxIn.Close()
		s.writeMu.Unlock()
		go func() {
			select {
			case <-s.done:
			case <-time.After(3 * time.Second):
				if s.mux.Process != nil {
					_ = s.mux.Process.Kill()
				}
			}
		}()
	})
}

// Done closes once the station has fully stopped.
func (s *Station) Done() <-chan struct{} { return s.done }

// Alive reports whether the station is running and not being stopped.
func (s *Station) Alive() bool {
	select {
	case <-s.done:
		return false
	case <-s.stopping:
		return false
	default:
		return true
	}
}

// Subscribe attaches an MPEG-TS client to the fan-out.
func (s *Station) Subscribe() (<-chan []byte, func()) { return s.hub.Subscribe() }

// PID is the muxer's process id.
func (s *Station) PID() int {
	if s.mux != nil && s.mux.Process != nil {
		return s.mux.Process.Pid
	}
	return 0
}

// Dir is the HLS output directory.
func (s *Station) Dir() string { return s.dir }

// Epoch is the wall-clock time of timeline position 0.
func (s *Station) Epoch() time.Time { return s.epoch }

// WaitReady blocks until the live playlist lists at least one segment.
func (s *Station) WaitReady(ctx context.Context) error {
	idx := filepath.Join(s.dir, "index.m3u8")
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		if b, err := readShared(idx); err == nil && bytes.Contains(b, []byte("#EXTINF")) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return errors.New("channel: station stopped before first segment")
		case <-t.C:
		}
	}
}

// Play renders one segment (Player).
func (s *Station) Play(ctx context.Context, seg Segment) (bool, error) {
	look := s.currentLook(ctx)
	in, start, err := s.blockInput(ctx, seg, look)
	if err != nil {
		s.log.Warn("channel: block unplayable", "kind", seg.Kind, "seq", seg.Block.Seq,
			"err", err.Error(), "event", "channel_block_unplayable")
		return false, err
	}
	if in == nil {
		return true, nil // nothing left of this segment
	}
	prof := s.cfg.Profile
	if s.software {
		prof.Encoder = EncoderSoftware
		in.HWDecode = false
	}
	res, err := s.runBlock(ctx, prof, *in, start)
	if err != nil && ctx.Err() == nil && prof.Encoder == EncoderNVENC && res.written == 0 {
		// NVENC can refuse a session (GeForce session cap, driver
		// hiccup): fall back to libx264 for the rest of this station.
		s.log.Warn("channel: NVENC failed, falling back to libx264", "err", err.Error(),
			"event", "channel_nvenc_fallback")
		s.software = true
		prof.Encoder = EncoderSoftware
		in2, start2, err2 := s.blockInput(ctx, seg, look)
		if err2 != nil || in2 == nil {
			return in2 == nil && err2 == nil, err2
		}
		in2.HWDecode = false
		in = in2
		res, err = s.runBlock(ctx, prof, *in2, start2)
	}
	if err != nil && ctx.Err() == nil {
		s.log.Warn("channel: block failed", "kind", seg.Kind, "seq", seg.Block.Seq,
			"err", err.Error(), "event", "channel_block_failed")
		if seg.Kind == KindLive {
			if f, ok := s.cfg.Resolver.(interface{ Forget(string) }); ok {
				f.Forget(seg.Block.SourceURL)
			}
		}
	}
	// A block whose output was cut short of the segment (slice shorter
	// than the slot) is incomplete even when ffmpeg exited cleanly.
	full := in.DurationSec >= seg.End.Sub(start).Seconds()-earlyEnd.Seconds()
	return err == nil && res.complete && full, err
}

// blockInput builds the ffmpeg input for `seg` starting now (or at the
// segment start if the timeline is slightly ahead of the clock).
func (s *Station) blockInput(ctx context.Context, seg Segment, look Look) (*BlockInput, time.Time, error) {
	now := s.cfg.Now()
	start := seg.Start
	if now.After(start) {
		start = now
	}
	dur := seg.End.Sub(start)
	if dur < minRemaining {
		return nil, start, nil
	}
	in := &BlockInput{DurationSec: dur.Seconds()}

	switch seg.Kind {
	case KindProgram, KindFiller, KindBumper:
		path := s.cfg.PathMap.Apply(seg.Block.Path)
		info, err := s.cfg.Prober.Probe(ctx, path)
		if err != nil {
			return nil, start, fmt.Errorf("probe %s: %w", path, err)
		}
		seekMS := seg.SeekMS + int(start.Sub(seg.Start)/time.Millisecond)
		if b := seg.Block; b.SourceDurationMS > 0 {
			remain := time.Duration(b.SourceOffsetMS+b.SourceDurationMS-seekMS) * time.Millisecond
			if remain+earlyEnd < dur {
				if remain < minRemaining {
					return nil, start, errors.New("source slice exhausted")
				}
				in.DurationSec = remain.Seconds() // rest is slate-filled
			}
		}
		in.Kind = "file"
		in.Source = path
		in.SeekSec = float64(seekMS) / 1000
		in.HasVideo, in.HasAudio = info.HasVideo, info.HasAudio
		in.HWDecode = s.cfg.Profile.Encoder == EncoderNVENC
		s.applyLook(in, look)
	case KindLive:
		u, err := s.cfg.Resolver.Resolve(ctx, seg.Block.SourceURL)
		if err != nil {
			return nil, start, fmt.Errorf("resolve live: %w", err)
		}
		info, err := s.cfg.Prober.Probe(ctx, u)
		if err != nil {
			return nil, start, fmt.Errorf("probe live: %w", err)
		}
		// Resolving/probing took time: recompute the remaining span.
		if t := s.cfg.Now(); t.After(start) {
			start = t
			in.DurationSec = seg.End.Sub(start).Seconds()
			if seg.End.Sub(start) < minRemaining {
				return nil, start, nil
			}
		}
		in.Kind = "live"
		in.Source = u
		in.HasVideo, in.HasAudio = info.HasVideo, info.HasAudio
		s.applyLook(in, look)
	default:
		in.Kind = "slate"
		if p := s.localAsset(look.LoadingPath); p != "" {
			in.SlateImage = p
		} else if p := s.localAsset(look.LogoPath); p != "" {
			in.SlateLogo = p
		}
		if seg.Message != "" {
			in.SlateText = seg.Message
			in.FontFile = s.cfg.FontFile
		}
	}
	return in, start, nil
}

func (s *Station) applyLook(in *BlockInput, look Look) {
	if wm := look.Watermark; wm != nil {
		if p := s.localAsset(wm.Path); p != "" {
			w := *wm
			w.Path = p
			in.Watermark = &w
		}
	}
	ln := look.Loudnorm
	in.Loudnorm = &ln
}

// localAsset maps a look/logo path and returns it if it exists (URLs are
// passed through for ffmpeg to fetch).
func (s *Station) localAsset(p string) string {
	if p == "" {
		return ""
	}
	if strings.Contains(p, "://") {
		return p
	}
	m := s.cfg.PathMap.Apply(p)
	if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
		return m
	}
	return ""
}

func (s *Station) currentLook(ctx context.Context) Look {
	if !s.lookAt.IsZero() && time.Since(s.lookAt) < lookTTL {
		return s.look
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	l, err := s.cfg.Repo.Look(lctx, s.id)
	if err != nil {
		if s.lookAt.IsZero() {
			s.look = DefaultLook()
		}
		return s.look
	}
	s.look, s.lookAt = l, time.Now()
	return l
}

// runBlock spawns one block ffmpeg and copies its MPEG-TS into the
// muxer, returning the bytes forwarded.
type blockResult struct {
	written  int64
	complete bool // ran (about) its whole duration
}

// lookaheadAllowance is how much earlier than real time a block may end
// because filters with look-ahead (loudnorm ≈ 3 s) flush their tail at
// EOF faster than real time.
func lookaheadAllowance(in BlockInput) float64 {
	a := earlyEnd.Seconds()
	if in.Loudnorm != nil && in.Loudnorm.Enabled && in.HasAudio {
		a += 3.5
	}
	return a
}

func (s *Station) runBlock(ctx context.Context, prof OutputProfile, in BlockInput, start time.Time) (blockResult, error) {
	offset := start.Sub(s.epoch).Seconds()
	if offset < s.lastEnd {
		offset = s.lastEnd
	}
	in.TSOffsetSec = offset
	args := BlockArgs(prof, in)

	cmd := exec.CommandContext(ctx, s.cfg.FFmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return blockResult{}, err
	}
	tail := &tailBuffer{max: 4096}
	if s.blockLog != nil {
		fmt.Fprintf(s.blockLog, "%s %s src=%q dur=%.3f off=%.3f enc=%s\n",
			time.Now().Format(time.RFC3339), in.Kind, in.Source, in.DurationSec, offset, prof.Encoder)
		cmd.Stderr = io.MultiWriter(tail, s.blockLog)
	} else {
		cmd.Stderr = tail
	}
	launched := time.Now()
	if err := cmd.Start(); err != nil {
		return blockResult{}, err
	}
	pw := &packetWriter{w: lockedWriter{mu: &s.writeMu, w: s.muxIn}}
	_, copyErr := io.Copy(pw, stdout)
	pw.Discard()
	if copyErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill() // muxer gone: don't leave ffmpeg blocked on a full pipe
	}
	waitErr := cmd.Wait()

	elapsed := time.Since(launched).Seconds()
	if s.blockLog != nil {
		fmt.Fprintf(s.blockLog, "  done elapsed=%.3f bytes=%d err=%v\n", elapsed, pw.Written(), waitErr)
	}
	res := blockResult{written: pw.Written()}
	if waitErr == nil && copyErr == nil && elapsed >= in.DurationSec-lookaheadAllowance(in) {
		res.complete = true
		s.lastEnd = offset + in.DurationSec
	} else {
		// Output reached at most elapsed + the -re initial burst.
		end := offset + elapsed + 1
		if end > offset+in.DurationSec {
			end = offset + in.DurationSec
		}
		s.lastEnd = end
	}
	if waitErr != nil {
		if msg := strings.TrimSpace(tail.String()); msg != "" {
			return res, fmt.Errorf("%w: %s", waitErr, lastLines(msg, 3))
		}
		return res, waitErr
	}
	return res, copyErr
}

// lockedWriter serialises writes into the muxer stdin (Stop closes it).
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

// tailBuffer keeps the last `max` bytes written (ffmpeg stderr tail).
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// resetDir empties a station's output dir (stale segments from a past
// run would otherwise be served).
func resetDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readShared reads a whole file opened with delete/rename sharing.
func readShared(path string) ([]byte, error) {
	f, err := openShared(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
