package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/procattr"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// Origin names where a source's TS comes from (a host and its placement
// generation; Epic 33 adds the slot).
type Origin struct {
	Host string
	Gen  int64
	Slot string
}

func (o Origin) String() string {
	s := fmt.Sprintf("%s/%d", o.Host, o.Gen)
	if o.Slot != "" {
		s += "/" + o.Slot
	}
	return s
}

// Source is where the relay reads a channel's TS from.
//
// Stream writes the channel's TS from where it is routed now until that
// source ends (its station stops, its host dies, the channel moves) or ctx
// ends; it calls started with the origin before the first byte. The relay
// calls Stream again at once, and treats the next bytes as a new source
// (Retimer.Switch): this is the hook Epic 33.8's swap uses, a slot-routed
// Source ending one slot's stream at the swap IDR and starting the next.
//
// Attach holds a front-door lease on the channel for as long as the push
// runs, so its station (local or on a node) is not reaped (story 32.7 AC7).
type Source interface {
	Stream(ctx context.Context, ch uuid.UUID, w io.Writer, started func(Origin)) error
	Attach(ch uuid.UUID) func()
}

// Proc is a started ffmpeg.
type Proc interface {
	Stdin() io.WriteCloser
	Wait() error
	Kill()
	PID() int
	// Tail is the end of its stderr (for the error report).
	Tail() string
}

// Runner starts ffmpeg (tests: a fake).
type Runner interface {
	Start(bin string, args []string) (Proc, error)
}

// ExecRunner runs a real ffmpeg tied to this process (Story 22.9).
type ExecRunner struct{}

type execProc struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	tail *tailBuf
}

func (p *execProc) Stdin() io.WriteCloser { return p.in }
func (p *execProc) Wait() error           { return p.cmd.Wait() }
func (p *execProc) PID() int              { return p.cmd.Process.Pid }
func (p *execProc) Tail() string          { return p.tail.String() }
func (p *execProc) Kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// Start implements Runner.
func (ExecRunner) Start(bin string, args []string) (Proc, error) {
	cmd := exec.Command(bin, args...) //nolint:gosec // the configured ffmpeg
	procattr.Tie(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	t := &tailBuf{max: 2048}
	cmd.Stdout, cmd.Stderr = io.Discard, t
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProc{cmd: cmd, in: in, tail: t}, nil
}

type tailBuf struct {
	mu  sync.Mutex
	b   []byte
	max int
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
	return strings.TrimSpace(string(t.b))
}

// Session timings (story 32.7 EC2, 33.8 AC8).
const (
	// RestartDelay: a crashed relay ffmpeg is restarted within this (inside
	// the input's 30 s reconnect window, so Cloudflare keeps the video).
	RestartDelay = 500 * time.Millisecond
	// StallTimeout: a write to ffmpeg blocked this long means the uplink
	// cannot carry the bitrate: the relay drops behind and restarts ffmpeg.
	StallTimeout = 5 * time.Second
	// BehindLimit drops within BehindWindow fail the push ("uplink too slow").
	BehindLimit  = 3
	BehindWindow = 5 * time.Minute
	sourceRetry  = 500 * time.Millisecond
)

// Session is one running push: one ffmpeg, fed from the channel's current
// source through the Retimer.
type Session struct {
	PushID  string
	Channel uuid.UUID
	Creds   Credentials
	Audio   Audio
	FFmpeg  string
	Source  Source
	Runner  Runner
	// Report sends tv_push_report (connecting, live_seen, error, stopped).
	Report func(state, detail string)
	Logger *slog.Logger
	// KbpsHint is the channel's bitrate (for the "uplink too slow" message).
	KbpsHint int
	// Stall and Restart override StallTimeout and RestartDelay (tests).
	Stall, Restart time.Duration
	// KillGrace is how long a stopped ffmpeg may take to exit (default 3 s).
	KillGrace time.Duration

	feed    chan chunk
	gen     int // bumped at every Resync: older chunks are dropped
	mu      sync.Mutex
	rt      *Retimer
	origin  Origin
	starts  int
	stalls  []time.Time
	restart chan struct{}
	kick    chan struct{}
}

func (s *Session) log() *slog.Logger {
	l := s.Logger
	if l == nil {
		l = slog.Default()
	}
	return l.With("push_id", s.PushID, "channel_id", s.Channel.String())
}

func (s *Session) report(state, detail string) {
	if state == "error" {
		tvmetrics.PushErrors.Inc("relay_ffmpeg")
	}
	if s.Report != nil {
		s.Report(state, Mask(detail, s.Creds.Key))
	}
}

// Starts is how many times ffmpeg was started (1 = one RTMPS/SRT session).
func (s *Session) Starts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts
}

// Origin is the source being forwarded now.
func (s *Session) Origin() Origin {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.origin
}

// Kick ends the current source so the relay resolves the channel again at
// once (a move or a swap announced by the front door).
func (s *Session) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

type chunk struct {
	b   []byte
	gen int
}

// errFatal ends the session (the push fails).
type errFatal struct{ msg string }

func (e errFatal) Error() string { return e.msg }

// Run pushes until ctx ends (the push stops) or a fatal error.
func (s *Session) Run(ctx context.Context) error {
	if s.Runner == nil {
		s.Runner = ExecRunner{}
	}
	if s.FFmpeg == "" {
		s.FFmpeg = "ffmpeg"
	}
	s.rt = NewRetimer()
	s.feed = make(chan chunk, 1024)
	s.restart = make(chan struct{}, 1)
	s.kick = make(chan struct{}, 1)
	release := s.Source.Attach(s.Channel)
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fatal := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.sourceLoop(ctx) }()
	go func() { defer wg.Done(); s.pushLoop(ctx, fatal) }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-fatal:
		cancel()
	}
	wg.Wait()
	if err != nil {
		s.report("error", "fatal: "+err.Error())
		return err
	}
	s.report("stopped", "")
	return nil
}

// feeder is the io.Writer a source writes into: retimed, then queued for ffmpeg.
type feeder struct {
	s   *Session
	ctx context.Context
	err error
}

func (f *feeder) Write(b []byte) (int, error) {
	f.s.mu.Lock()
	out, err := f.s.rt.Process(b)
	var cp []byte
	if len(out) > 0 {
		cp = append([]byte(nil), out...)
	}
	gen := f.s.gen
	f.s.mu.Unlock()
	if err != nil {
		f.err = err
		return 0, err
	}
	if cp == nil {
		return len(b), nil
	}
	stall := f.s.Stall
	if stall <= 0 {
		stall = StallTimeout
	}
	t := time.NewTimer(stall)
	defer t.Stop()
	select {
	case f.s.feed <- chunk{b: cp, gen: gen}:
	case <-f.ctx.Done():
		return 0, f.ctx.Err()
	case <-t.C:
		f.s.behind()
	}
	return len(b), nil
}

// behind records a stall: ffmpeg (the uplink) did not take the stream in time.
func (s *Session) behind() {
	now := time.Now()
	s.mu.Lock()
	keep := s.stalls[:0]
	for _, t := range s.stalls {
		if now.Sub(t) < BehindWindow {
			keep = append(keep, t)
		}
	}
	s.stalls = append(keep, now)
	n := len(s.stalls)
	s.mu.Unlock()
	s.log().Warn("relay: ffmpeg fell behind the channel; restarting it", "stalls", n, "event", "tv_push_error")
	select {
	case s.restart <- struct{}{}:
	default:
	}
}

func (s *Session) tooSlow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.stalls) >= BehindLimit
}

func (s *Session) sourceLoop(ctx context.Context) {
	first := true
	lastErr := ""
	for ctx.Err() == nil {
		sctx, scancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-s.kick:
				scancel()
			case <-sctx.Done():
			}
		}()
		fw := &feeder{s: s, ctx: sctx}
		err := s.Source.Stream(sctx, s.Channel, fw, func(o Origin) {
			s.mu.Lock()
			prev := s.origin
			s.origin = o
			if !first {
				s.rt.Switch()
			}
			s.mu.Unlock()
			if !first && prev != o {
				s.log().Info("relay: source switched; the push session stays up", "from", prev.String(), "to", o.String(),
					"event", "tv_push_source_switch")
			}
			first = false
		})
		scancel()
		if errors.Is(fw.err, ErrParamChange) {
			s.log().Warn("relay: the new source's streams differ; reconnecting the push", "event", "tv_push_reconnect")
			s.mu.Lock()
			s.rt = NewRetimer()
			s.mu.Unlock()
			select {
			case s.restart <- struct{}{}:
			default:
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, context.Canceled) && err.Error() != lastErr {
			s.log().Info("relay: source ended; resolving the channel again", "err", err, "event", "tv_push_source_lost")
		}
		lastErr = ""
		if err != nil {
			lastErr = err.Error()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.sourceRetry()):
		}
	}
}

func (s *Session) pushLoop(ctx context.Context, fatal chan<- error) {
	args := Args(s.Creds, s.Audio)
	for ctx.Err() == nil {
		p, err := s.Runner.Start(s.FFmpeg, args)
		if err != nil {
			s.report("error", "ffmpeg did not start: "+err.Error())
			fatal <- errFatal{"the relay's ffmpeg does not start: " + err.Error()}
			return
		}
		s.mu.Lock()
		s.starts++
		n := s.starts
		if n > 1 {
			s.rt.Resync() // the new ffmpeg begins on a keyframe after the PAT/PMT
			s.gen++
		}
		s.mu.Unlock()
		s.log().Info("relay: pushing", "cmd", MaskedArgs(s.FFmpeg, args, s.Creds), "pid", p.PID(), "start", n,
			"event", map[bool]string{true: "tv_push_start", false: "tv_push_reconnect"}[n == 1])
		done := make(chan error, 1)
		go func() { done <- p.Wait() }()
		reason, exited := s.feedProc(ctx, p, done)
		_ = p.Stdin().Close()
		if exited != nil {
			err = *exited
		} else {
			select {
			case err = <-done:
			case <-time.After(s.killGrace()):
				p.Kill()
				err = <-done
			}
		}
		if ctx.Err() != nil {
			return
		}
		if s.tooSlow() {
			fatal <- errFatal{fmt.Sprintf("uplink too slow for %d kbit/s", s.KbpsHint)}
			return
		}
		detail := reason
		if err != nil {
			detail = strings.TrimSpace(detail + " " + err.Error())
		}
		if t := p.Tail(); t != "" {
			detail += ": " + lastLine(t)
		}
		s.log().Warn("relay: ffmpeg ended; restarting on the same input", "why", Mask(detail, s.Creds.Key),
			"event", "tv_push_reconnect")
		s.report("error", detail)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.restartDelay()):
		}
	}
}

func (s *Session) killGrace() time.Duration {
	if s.KillGrace > 0 {
		return s.KillGrace
	}
	return 3 * time.Second
}

func (s *Session) sourceRetry() time.Duration {
	if s.Restart > 0 {
		return s.Restart
	}
	return sourceRetry
}

func (s *Session) restartDelay() time.Duration {
	if s.Restart > 0 {
		return s.Restart
	}
	return RestartDelay
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(ls[len(ls)-1])
}

// feedProc writes the feed into one ffmpeg until it fails, a restart is
// asked for, or ctx ends.
func (s *Session) feedProc(ctx context.Context, p Proc, done <-chan error) (string, *error) {
	in := p.Stdin()
	reported := false
	// a write that blocks past the stall timeout (ffmpeg, i.e. the uplink,
	// does not take the stream) counts as falling behind and ends this ffmpeg
	stall := s.Stall
	if stall <= 0 {
		stall = StallTimeout
	}
	var writing atomic.Int64 // unix nanos of the write in progress (0: none)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tk := time.NewTicker(stall / 4)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if at := writing.Load(); at != 0 && time.Since(time.Unix(0, at)) > stall {
					writing.Store(0)
					s.behind()
					p.Kill()
				}
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return "stopped", nil
		case <-s.restart:
			return "restart asked", nil
		case err := <-done:
			return "ffmpeg exited", &err
		case c := <-s.feed:
			s.mu.Lock()
			stale := c.gen != s.gen
			s.mu.Unlock()
			if stale {
				continue // retimed for the previous ffmpeg
			}
			writing.Store(time.Now().UnixNano())
			_, err := in.Write(c.b)
			writing.Store(0)
			if err != nil {
				return "ffmpeg stopped reading", nil
			}
			if !reported {
				reported = true
				s.report("connecting", "ffmpeg up, pid "+fmt.Sprint(p.PID()))
			}
		}
	}
}
