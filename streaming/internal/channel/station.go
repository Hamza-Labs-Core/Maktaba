package channel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
// MPEG-TS fan-out) fed block by block by a Feeder. The muxer is
// supervised: if it dies while the station is on, it is restarted (with
// backoff) and the block stream simply continues into the new one.
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

	muxMu    sync.Mutex
	mux      *muxProc
	restarts int

	stopOnce sync.Once
	stopping chan struct{}

	software atomic.Bool // NVENC failed on this station: libx264 from now on
	// reachedBits: float64 bits of the timeline position (s since epoch)
	// the forwarded output has reached.
	reachedBits atomic.Uint64
	lookMu      sync.Mutex
	look        Look
	lookAt      time.Time
	logMu       sync.Mutex
	blockLog    *os.File
}

// muxProc is one muxer process.
type muxProc struct {
	cmd *exec.Cmd
	in  io.WriteCloser
}

// Muxer supervision backoff.
const (
	muxBackoffMin   = 250 * time.Millisecond
	muxBackoffMax   = 10 * time.Second
	muxHealthyAfter = 30 * time.Second
)

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

	m, err := s.startMuxer(false)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("channel: start muxer: %w", err)
	}
	s.mux = m
	s.epoch = cfg.Now()

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
		s.supervise(m)
		cancel()
		<-feederDone
		s.hub.Close()
		s.logMu.Lock()
		if s.blockLog != nil {
			_ = s.blockLog.Close()
			s.blockLog = nil
		}
		s.logMu.Unlock()
		close(s.done)
	}()
	s.log.Info("channel: station started", "encoder", cfg.Profile.Encoder,
		"profile", fmt.Sprintf("%dx%d@%d", cfg.Profile.Width, cfg.Profile.Height, cfg.Profile.FPS),
		"event", "channel_station_started")
	return s, nil
}

// startMuxer spawns a muxer process and pumps its TS output into the
// hub. restart appends to mux.log and marks an HLS discontinuity.
func (s *Station) startMuxer(restart bool) (*muxProc, error) {
	cmd := exec.Command(s.cfg.FFmpeg, MuxerArgs(s.cfg.Profile, s.dir, restart)...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if restart {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	if lf, err := os.OpenFile(filepath.Join(s.dir, "mux.log"), flags, 0o644); err == nil {
		if restart {
			fmt.Fprintf(lf, "--- muxer restart %s\n", time.Now().Format(time.RFC3339))
		}
		cmd.Stderr = lf
		defer lf.Close() // the child holds its own handle
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go s.hub.pump(out)
	return &muxProc{cmd: cmd, in: in}, nil
}

// supervise waits on the muxer and restarts it (with backoff) until the
// station stops; viewers stay attached to the hub throughout.
func (s *Station) supervise(m *muxProc) {
	backoff := muxBackoffMin
	for {
		started := time.Now()
		err := m.cmd.Wait()
		if s.isStopping() {
			return
		}
		if time.Since(started) > muxHealthyAfter {
			backoff = muxBackoffMin
		}
		s.log.Warn("channel: muxer exited; restarting", "err", errString(err), "backoff", backoff.String(),
			"event", "channel_muxer_restart")
		s.muxMu.Lock()
		s.mux = nil // block output is dropped until the new muxer is up
		s.muxMu.Unlock()
		for {
			select {
			case <-s.stopping:
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > muxBackoffMax {
				backoff = muxBackoffMax
			}
			nm, err := s.startMuxer(true)
			if err != nil {
				s.log.Warn("channel: muxer restart failed", "err", err.Error(), "event", "channel_muxer_restart_failed")
				continue
			}
			s.muxMu.Lock()
			if s.isStopping() {
				s.muxMu.Unlock()
				_ = nm.in.Close()
				_ = nm.cmd.Process.Kill()
				_ = nm.cmd.Wait()
				return
			}
			s.mux = nm
			s.restarts++
			s.muxMu.Unlock()
			m = nm
			break
		}
	}
}

func (s *Station) isStopping() bool {
	select {
	case <-s.stopping:
		return true
	default:
		return false
	}
}

// writeMux forwards TS packets to the current muxer. While the muxer is
// down (restarting) data is dropped: the channel clock keeps running and
// the new muxer picks up at the next keyframe.
func (s *Station) writeMux(b []byte) (int, error) {
	s.muxMu.Lock()
	defer s.muxMu.Unlock()
	if s.mux == nil {
		if s.isStopping() {
			return 0, errors.New("channel: station stopped")
		}
		return len(b), nil
	}
	if _, err := s.mux.in.Write(b); err != nil {
		if s.isStopping() {
			return 0, err
		}
		return len(b), nil // muxer died; the supervisor restarts it
	}
	return len(b), nil
}

// paceMargin is how far ahead of the wall clock output may be forwarded.
const paceMargin = 50 * time.Millisecond

// paceTo blocks until timeline position pos (s since epoch) is within
// paceMargin of the wall clock.
func (s *Station) paceTo(pos float64) {
	at := s.epoch.Add(time.Duration(pos * float64(time.Second)))
	if ahead := at.Sub(s.cfg.Now()); ahead > paceMargin {
		sleepCtx(s.ctx, ahead-paceMargin/2)
	}
}

func (s *Station) reached() float64 { return math.Float64frombits(s.reachedBits.Load()) }

func (s *Station) setReached(v float64) {
	for {
		old := s.reachedBits.Load()
		if v <= math.Float64frombits(old) || s.reachedBits.CompareAndSwap(old, math.Float64bits(v)) {
			return
		}
	}
}

// ptsTracker follows the PES timestamps of a block's MPEG-TS output.
type ptsTracker struct {
	min, max int64
	seen     bool
}

// scan reads PTS values from whole 188-byte packets; true if any found.
func (t *ptsTracker) scan(b []byte) bool {
	found := false
	for i := 0; i+tsPacket <= len(b); i += tsPacket {
		if pts, ok := packetPTS(b[i : i+tsPacket]); ok {
			if !t.seen || pts < t.min {
				t.min = pts
			}
			if !t.seen || pts > t.max {
				t.max = pts
			}
			t.seen, found = true, true
		}
	}
	return found
}

// span is the output duration covered so far, in seconds.
func (t *ptsTracker) span() float64 {
	if !t.seen || t.max < t.min {
		return 0
	}
	return float64(t.max-t.min) / 90000
}

// packetPTS extracts the PTS of a PES packet starting in this TS packet.
func packetPTS(p []byte) (int64, bool) {
	if len(p) != tsPacket || p[0] != 0x47 || p[1]&0x40 == 0 {
		return 0, false
	}
	afc := (p[3] >> 4) & 3
	idx := 4
	if afc&2 != 0 {
		idx += 1 + int(p[4])
	}
	if afc&1 == 0 || idx+14 > len(p) {
		return 0, false
	}
	h := p[idx:]
	if h[0] != 0 || h[1] != 0 || h[2] != 1 {
		return 0, false
	}
	sid := h[3]
	if !(sid >= 0xC0 && sid <= 0xEF) && sid != 0xBD {
		return 0, false
	}
	if h[7]&0x80 == 0 {
		return 0, false
	}
	q := h[9:14]
	pts := int64(q[0]>>1&0x07)<<30 | int64(q[1])<<22 | int64(q[2]>>1)<<15 | int64(q[3])<<7 | int64(q[4]>>1)
	return pts, true
}

// MuxerRestarts reports how many times the muxer was restarted.
func (s *Station) MuxerRestarts() int {
	s.muxMu.Lock()
	defer s.muxMu.Unlock()
	return s.restarts
}

// Stop tears the station down (non-blocking; Done closes when finished).
func (s *Station) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopping)
		s.cancel()
		s.muxMu.Lock()
		m := s.mux
		if m != nil {
			_ = m.in.Close()
		}
		s.muxMu.Unlock()
		go func() {
			select {
			case <-s.done:
			case <-time.After(3 * time.Second):
				if m != nil && m.cmd.Process != nil {
					_ = m.cmd.Process.Kill()
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
	s.muxMu.Lock()
	defer s.muxMu.Unlock()
	if s.mux != nil && s.mux.cmd.Process != nil {
		return s.mux.cmd.Process.Pid
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

// Start begins a segment (Player): input preparation and the block
// ffmpeg run in the background, buffering output until Wait forwards it.
func (s *Station) Start(ctx context.Context, seg Segment) Playback {
	pb := &stationPlayback{s: s, seg: seg, q: newByteQueue(), result: make(chan playResult, 1)}
	go pb.produce(ctx)
	return pb
}

type playResult struct {
	complete bool
	err      error
}

// stationPlayback is one block: produce() runs ffmpeg into q; Wait()
// drains q into the muxer.
type stationPlayback struct {
	s          *Station
	seg        Segment
	q          *byteQueue
	result     chan playResult
	offsetBits atomic.Uint64 // float64 bits of the block's timeline offset
}

func (p *stationPlayback) Wait() (bool, error) {
	tr := &ptsTracker{}
	pw := &packetWriter{w: writerFunc(func(b []byte) (int, error) {
		if tr.scan(b) {
			// Timeline position this block's output has reached.
			off := math.Float64frombits(p.offsetBits.Load())
			pos := off + tr.span()
			p.s.setReached(pos + 1.0/float64(p.s.cfg.Profile.FPS))
			// Hold queued-ahead output back to the wall clock: a block
			// started early (or a filter flushing its tail) must not push
			// the channel ahead of real time.
			p.s.paceTo(pos)
		}
		return p.s.writeMux(b)
	})}
	for {
		b, ok := p.q.pop()
		if !ok {
			break
		}
		if _, err := pw.Write(b); err != nil {
			p.q.abandon()
			break
		}
	}
	pw.Discard()
	r := <-p.result
	return r.complete, r.err
}

func (p *stationPlayback) produce(ctx context.Context) {
	complete, err := p.run(ctx)
	p.q.close()
	p.result <- playResult{complete: complete, err: err}
}

func (p *stationPlayback) run(ctx context.Context) (bool, error) {
	s, seg := p.s, p.seg
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
	if s.software.Load() {
		prof.Encoder = EncoderSoftware
		in.HWDecode = false
	}
	res, err := s.runBlock(ctx, prof, *in, start, p)
	if err != nil && ctx.Err() == nil && prof.Encoder == EncoderNVENC && res.written == 0 {
		// NVENC can refuse a session (GeForce session cap, driver
		// hiccup): fall back to libx264 for the rest of this station.
		s.log.Warn("channel: NVENC failed, falling back to libx264", "err", err.Error(),
			"event", "channel_nvenc_fallback")
		s.software.Store(true)
		prof.Encoder = EncoderSoftware
		in2, start2, err2 := s.blockInput(ctx, seg, look)
		if err2 != nil || in2 == nil {
			return in2 == nil && err2 == nil, err2
		}
		in2.HWDecode = false
		in, start = in2, start2
		res, err = s.runBlock(ctx, prof, *in2, start2, p)
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
	// Never start before the output already reached (a previous block
	// that ran ahead of the clock and then ended short): overlapping
	// timestamps would make the muxer rewrite them. A frame or two of
	// overlap (the last audio frame) is normal and left alone.
	if r := s.epoch.Add(time.Duration(s.reached() * float64(time.Second))); r.Sub(start) > 100*time.Millisecond {
		start = r
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
	s.lookMu.Lock()
	defer s.lookMu.Unlock()
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

// blockResult is the outcome of one block ffmpeg run.
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

func (s *Station) runBlock(ctx context.Context, prof OutputProfile, in BlockInput, start time.Time, pb *stationPlayback) (blockResult, error) {
	// The block's timestamps are its timeline position: a block started
	// ahead of its boundary begins exactly where the previous one ends.
	in.TSOffsetSec = start.Sub(s.epoch).Seconds()
	pb.offsetBits.Store(math.Float64bits(in.TSOffsetSec))
	q := pb.q
	args := BlockArgs(prof, in)

	cmd := exec.CommandContext(ctx, s.cfg.FFmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return blockResult{}, err
	}
	tail := &tailBuffer{max: 4096}
	cmd.Stderr = io.MultiWriter(tail, logWriter{s})
	s.logf("%s %s src=%q dur=%.3f off=%.3f enc=%s\n",
		time.Now().Format(time.RFC3339), in.Kind, in.Source, in.DurationSec, in.TSOffsetSec, prof.Encoder)
	launched := time.Now()
	if err := cmd.Start(); err != nil {
		return blockResult{}, err
	}
	// Buffer the output so ffmpeg's -re clock never stalls on the reader
	// (a block started ahead of its boundary queues up here).
	var written int64
	buf := make([]byte, 64*1024)
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			written += int64(n)
			if !q.push(buf[:n]) {
				_ = cmd.Process.Kill() // abandoned (station stopping)
			}
		}
		if rerr != nil {
			break
		}
	}
	waitErr := cmd.Wait()

	elapsed := time.Since(launched).Seconds()
	s.logf("  done elapsed=%.3f bytes=%d err=%v\n", elapsed, written, waitErr)
	res := blockResult{written: written}
	if waitErr == nil && elapsed >= in.DurationSec-lookaheadAllowance(in) {
		res.complete = true
	}
	if waitErr != nil {
		if msg := strings.TrimSpace(tail.String()); msg != "" {
			return res, fmt.Errorf("%w: %s", waitErr, lastLines(msg, 3))
		}
		return res, waitErr
	}
	return res, nil
}

func (s *Station) logf(format string, a ...any) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.blockLog != nil {
		fmt.Fprintf(s.blockLog, format, a...)
	}
}

type logWriter struct{ s *Station }

func (l logWriter) Write(b []byte) (int, error) {
	l.s.logMu.Lock()
	defer l.s.logMu.Unlock()
	if l.s.blockLog != nil {
		_, _ = l.s.blockLog.Write(b)
	}
	return len(b), nil
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

// byteQueue is an unbounded FIFO of byte chunks between a block's ffmpeg
// reader and the forwarder.
type byteQueue struct {
	mu        sync.Mutex
	cond      *sync.Cond
	chunks    [][]byte
	closed    bool
	abandoned bool
}

func newByteQueue() *byteQueue {
	q := &byteQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends a copy; false once the consumer abandoned the queue.
func (q *byteQueue) push(b []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.abandoned {
		return false
	}
	q.chunks = append(q.chunks, append([]byte(nil), b...))
	q.cond.Signal()
	return true
}

func (q *byteQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *byteQueue) abandon() {
	q.mu.Lock()
	q.abandoned = true
	q.chunks = nil
	q.cond.Broadcast()
	q.mu.Unlock()
}

// pop blocks for the next chunk; ok=false when closed and drained.
func (q *byteQueue) pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed && !q.abandoned {
		q.cond.Wait()
	}
	if len(q.chunks) == 0 {
		return nil, false
	}
	b := q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	return b, true
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
