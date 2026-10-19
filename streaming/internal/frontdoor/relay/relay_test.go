package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TC3 (32.7): the command lines; AC-3 → AAC in the push only; the key masked.
func TestRelayArgs(t *testing.T) {
	c := Credentials{URL: "rtmps://live.cloudflare.com:443/live/", Key: "s3cr3t-key", Ingest: "rtmps"}
	a := strings.Join(Args(c, Audio{Codec: "aac", Kbps: 160}), " ")
	for _, want := range []string{"-f mpegts -i pipe:0", "-c:v copy", "-c:a copy", "-f flv -flvflags no_duration_filesize",
		"rtmps://live.cloudflare.com:443/live/s3cr3t-key"} {
		if !strings.Contains(a, want) {
			t.Errorf("rtmps args lack %q: %s", want, a)
		}
	}
	if strings.Contains(a, "libx264") || strings.Contains(a, "h264_") || strings.Contains(a, "-c:v lib") {
		t.Errorf("a video encode in the relay: %s", a)
	}
	ac3 := strings.Join(Args(c, Audio{Codec: "ac3", Kbps: 192}), " ")
	if !strings.Contains(ac3, "-c:a aac -b:a 192k") || !strings.Contains(ac3, "-c:v copy") {
		t.Errorf("ac3: %s", ac3)
	}
	srt := Credentials{URL: "srt://live.cloudflare.com:778", Key: "pass phrase", StreamID: "sid-1", Ingest: "srt"}
	s := strings.Join(Args(srt, Audio{Codec: "aac"}), " ")
	if !strings.Contains(s, "-f mpegts srt://live.cloudflare.com:778?") || !strings.Contains(s, "mode=caller") ||
		!strings.Contains(s, "streamid=sid-1") || !strings.Contains(s, "passphrase=pass+phrase") {
		t.Errorf("srt: %s", s)
	}
	for _, line := range []string{MaskedArgs("ffmpeg", Args(c, Audio{}), c), MaskedArgs("ffmpeg", Args(srt, Audio{}), srt)} {
		if strings.Contains(line, "s3cr3t") || strings.Contains(line, "pass+phrase") || strings.Contains(line, "pass phrase") {
			t.Errorf("key in the log line: %s", line)
		}
		if !strings.Contains(line, "****") {
			t.Errorf("no mask: %s", line)
		}
	}
}

// Two sources with unrelated timestamps and PIDs become one continuous
// program: monotonic per stream, the video gap at the switch one frame, the
// first source's PIDs and PAT/PMT, continuity counters continuous.
func TestRetimerSwitchMonotonic(t *testing.T) {
	r := NewRetimer()
	a := newGen(0x1000, 0x100, 0x101, 900_000)
	b := newGen(0x1100, 0x200, 0x201, ptsWrap-100*6000) // wraps 33 bits within 100 frames
	var out []byte
	feed := func(x []byte, chunk int) {
		for len(x) > 0 {
			n := chunk
			if n > len(x) {
				n = len(x)
			}
			o, err := r.Process(x[:n])
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, o...)
			x = x[n:]
		}
	}
	// the first source joined mid-GOP: dropped until its first keyframe
	a.n = 7
	feed(a.frames(97), 1000)
	r.Switch()
	b.n = 11 // the new station starts mid-GOP too
	feed(b.frames(200), 4096)
	tr := readTrack(t, out)
	if tr.pmt != 0x1000 || len(tr.es) != 2 || tr.es[0].pid != 0x100 || tr.es[1].pid != 0x101 {
		t.Fatalf("output program pmt=%#x es=%+v", tr.pmt, tr.es)
	}
	if tr.first[0] != 0 || tr.first[1] != 0x1000 || tr.first[2] != 0x100 {
		t.Errorf("the output does not start with PAT, PMT, keyframe: %#x", tr.first)
	}
	for pid := range tr.dts {
		if pid == 0x200 || pid == 0x201 {
			t.Fatalf("a new source PID leaked: %#x", pid)
		}
	}
	v := tr.dts[0x100]
	// a: 97-7 frames from the keyframe at 30 → 67 frames; b: from the keyframe at 30 → 181
	if len(v) != (97+7-30)+(200+11-30) {
		t.Errorf("video frames %d", len(v))
	}
	monotonic(t, "video", v, 6000)
	monotonic(t, "audio", tr.dts[0x101], 2*6000)
	if tr.ccBreak != 0 {
		t.Errorf("%d continuity breaks", tr.ccBreak)
	}
	if r.Switches() != 1 {
		t.Errorf("switches %d", r.Switches())
	}
}

// A jump inside one source (a muxer restart) is evened out.
func TestRetimerJumpInsideSource(t *testing.T) {
	r := NewRetimer()
	g := newGen(0x1000, 0x100, 0x101, 1_000_000)
	o1, _ := r.Process(g.frames(60))
	out := append([]byte(nil), o1...)
	g.pts, g.apts = 77_000, 77_000 // backwards, then on
	o2, _ := r.Process(g.frames(60))
	out = append(out, o2...)
	tr := readTrack(t, out)
	monotonic(t, "video", tr.dts[0x100], 6000)
	monotonic(t, "audio", tr.dts[0x101], 12000)
	if r.Jumps() != 1 {
		t.Errorf("jumps %d", r.Jumps())
	}
}

// A new source without the audio track is a parameter change (33.8 AC7).
func TestRetimerParamChange(t *testing.T) {
	r := NewRetimer()
	_, _ = r.Process(newGen(0x1000, 0x100, 0x101, 0).frames(40))
	r.Switch()
	g := newGen(0x1000, 0x100, 0x101, 0)
	g.noAudio = true
	if _, err := r.Process(g.frames(40)); !errors.Is(err, ErrParamChange) {
		t.Fatalf("err %v", err)
	}
}

// fakeSource is two "hosts": each Stream call serves the next one's TS.
type fakeSource struct {
	mu       sync.Mutex
	gens     []*tsGen
	frames   []int
	calls    int
	attached int
	origins  []Origin
	block    chan struct{} // after the last source: wait here
	pace     time.Duration // between frames (a live source)
}

func (f *fakeSource) Attach(uuid.UUID) func() {
	f.mu.Lock()
	f.attached++
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.attached--
		f.mu.Unlock()
	}
}

func (f *fakeSource) Stream(ctx context.Context, _ uuid.UUID, w io.Writer, started func(Origin)) error {
	f.mu.Lock()
	i := f.calls
	f.calls++
	f.mu.Unlock()
	if i >= len(f.gens) {
		select {
		case <-ctx.Done():
		case <-f.block:
		}
		return ctx.Err()
	}
	o := Origin{Host: []string{"node-pc", "home"}[i%2], Gen: int64(10 + i)}
	f.mu.Lock()
	f.origins = append(f.origins, o)
	f.mu.Unlock()
	started(o)
	g := f.gens[i]
	for n := 0; n < f.frames[i]; n++ {
		if _, err := w.Write(g.frame()); err != nil {
			return err
		}
		if f.pace > 0 {
			time.Sleep(f.pace)
		}
	}
	return errors.New("host gone")
}

// fakeProc is an ffmpeg that keeps what it read.
type fakeProc struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	pr     *io.PipeReader
	pw     *io.PipeWriter
	done   chan struct{}
	err    error
	stopAt int // exit after reading this many bytes (0: never)
}

func newFakeProc(stopAt int) *fakeProc {
	pr, pw := io.Pipe()
	p := &fakeProc{pr: pr, pw: pw, done: make(chan struct{}), stopAt: stopAt}
	go func() {
		defer close(p.done)
		b := make([]byte, 32<<10)
		for {
			n, err := pr.Read(b)
			p.mu.Lock()
			p.buf.Write(b[:n])
			full := p.stopAt > 0 && p.buf.Len() >= p.stopAt
			p.mu.Unlock()
			if err != nil {
				return
			}
			if full {
				p.err = errors.New("exit status 1")
				_ = pr.CloseWithError(errors.New("broken pipe"))
				return
			}
		}
	}()
	return p
}

func (p *fakeProc) Stdin() io.WriteCloser { return p.pw }
func (p *fakeProc) Wait() error           { <-p.done; return p.err }
func (p *fakeProc) Kill()                 { _ = p.pr.Close() }
func (p *fakeProc) PID() int              { return 4242 }
func (p *fakeProc) Tail() string          { return "Connection reset by peer" }
func (p *fakeProc) bytes() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.buf.Bytes()...)
}

type fakeRunner struct {
	mu     sync.Mutex
	procs  []*fakeProc
	args   [][]string
	stopAt []int
}

func (r *fakeRunner) Start(_ string, args []string) (Proc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stop := 0
	if len(r.procs) < len(r.stopAt) {
		stop = r.stopAt[len(r.procs)]
	}
	p := newFakeProc(stop)
	r.procs = append(r.procs, p)
	r.args = append(r.args, args)
	return p, nil
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.procs)
}

type reports struct {
	mu sync.Mutex
	l  []string
}

func (r *reports) add(state, detail string) {
	r.mu.Lock()
	r.l = append(r.l, state+":"+detail)
	r.mu.Unlock()
}

func (r *reports) has(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.l {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

func waitUntil(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 33.8 TC1/TC3: across a failover (the source moves from a node to the home)
// one ffmpeg (one RTMPS/SRT session) runs, and what it reads is monotonic.
func TestRelayHoldsSessionAcrossSwitch(t *testing.T) {
	src := &fakeSource{gens: []*tsGen{newGen(0x1000, 0x100, 0x101, 5_000_000), newGen(0x1000, 0x100, 0x101, 40_000)},
		frames: []int{90, 90}, block: make(chan struct{})}
	run := &fakeRunner{}
	rep := &reports{}
	s := &Session{PushID: "p1", Channel: uuid.New(), Creds: Credentials{URL: "rtmps://x/live/", Key: "secret-key-1", Ingest: "rtmps"},
		Source: src, Runner: run, Report: rep.add, Restart: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, "both sources streamed", func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return src.calls >= 3
	})
	waitUntil(t, "the second source reached ffmpeg", func() bool {
		return len(readTrack(t, run.procs[0].bytes()).dts[0x100]) >= 60+60
	})
	if src.attached != 1 {
		t.Errorf("front-door lease held %d times", src.attached)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if run.count() != 1 || s.Starts() != 1 {
		t.Fatalf("ffmpeg started %d times, want one session", run.count())
	}
	tr := readTrack(t, run.procs[0].bytes())
	monotonic(t, "video", tr.dts[0x100], 6000)
	monotonic(t, "audio", tr.dts[0x101], 12000)
	if !rep.has("connecting:") || !rep.has("stopped:") {
		t.Errorf("reports %v", rep.l)
	}
	if src.attached != 0 {
		t.Error("the lease outlived the push")
	}
	if len(src.origins) != 2 || src.origins[0].Host == src.origins[1].Host {
		t.Errorf("origins %v", src.origins)
	}
}

// 33.8 AC8: a crashed ffmpeg is restarted at once on the same input; the new
// one starts with the PAT/PMT and a keyframe, timestamps continuing.
func TestRelayRestartsCrashedFFmpeg(t *testing.T) {
	src := &fakeSource{gens: []*tsGen{newGen(0x1000, 0x100, 0x101, 100_000)}, frames: []int{3000}, block: make(chan struct{}),
		pace: 200 * time.Microsecond}
	run := &fakeRunner{stopAt: []int{40 * 3 * tsSize}}
	rep := &reports{}
	s := &Session{PushID: "p1", Channel: uuid.New(), Creds: Credentials{URL: "rtmps://x/live/", Key: "secret-key-1", Ingest: "rtmps"},
		Source: src, Runner: run, Report: rep.add, Restart: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()
	waitUntil(t, "a second ffmpeg", func() bool { return run.count() >= 2 })
	waitUntil(t, "data in the second ffmpeg", func() bool { return len(run.procs[1].bytes()) > 20*tsSize })
	first := readTrack(t, run.procs[0].bytes())
	second := readTrack(t, run.procs[1].bytes())
	if second.first[0] != 0 || second.first[1] != 0x1000 || second.first[2] != 0x100 || len(second.keys) == 0 || second.keys[0] != 0 {
		t.Fatalf("the restarted ffmpeg does not begin with PAT, PMT, keyframe: %#x %v", second.first, second.keys)
	}
	lastV := first.dts[0x100][len(first.dts[0x100])-1]
	if second.dts[0x100][0] <= lastV {
		t.Errorf("timestamps went back across the restart: %d after %d", second.dts[0x100][0], lastV)
	}
	if !rep.has("error:") {
		t.Errorf("no error report: %v", rep.l)
	}
}

// 32.7 EC2: an ffmpeg that cannot keep up three times in five minutes fails
// the push ("uplink too slow").
func TestRelayFailsWhenUplinkTooSlow(t *testing.T) {
	src := &fakeSource{gens: []*tsGen{newGen(0x1000, 0x100, 0x101, 0)}, frames: []int{1 << 20}, block: make(chan struct{})}
	run := &stuckRunner{}
	rep := &reports{}
	s := &Session{PushID: "p1", Channel: uuid.New(), Creds: Credentials{URL: "rtmps://x/live/", Key: "secret-key-1", Ingest: "rtmps"},
		Source: src, Runner: run, Report: rep.add, KbpsHint: 6128, Stall: 5 * time.Millisecond, Restart: time.Millisecond,
		KillGrace: 10 * time.Millisecond}
	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "uplink too slow for 6128 kbit/s") {
		t.Fatalf("err %v", err)
	}
	if !rep.has("error:fatal: uplink too slow") {
		t.Errorf("reports %v", rep.l)
	}
}

// stuckRunner's ffmpeg never reads (an uplink that does not move).
type stuckRunner struct{}

type stuckProc struct {
	pr   *io.PipeReader
	pw   *io.PipeWriter
	done chan struct{}
	once sync.Once
}

func (stuckRunner) Start(string, []string) (Proc, error) {
	pr, pw := io.Pipe()
	return &stuckProc{pr: pr, pw: pw, done: make(chan struct{})}, nil
}
func (p *stuckProc) Stdin() io.WriteCloser { return p.pw }
func (p *stuckProc) Wait() error           { <-p.done; return nil }
func (p *stuckProc) Kill()                 { p.once.Do(func() { _ = p.pr.Close(); close(p.done) }) }
func (p *stuckProc) PID() int              { return 1 }
func (p *stuckProc) Tail() string          { return "" }

// stallOnceRunner: the first ffmpeg never reads (one stall), the next ones do.
type stallOnceRunner struct {
	fakeRunner
	stuck *stuckProc
}

func (r *stallOnceRunner) Start(bin string, args []string) (Proc, error) {
	r.mu.Lock()
	if r.stuck == nil {
		pr, pw := io.Pipe()
		r.stuck = &stuckProc{pr: pr, pw: pw, done: make(chan struct{})}
		r.procs = append(r.procs, nil)
		r.mu.Unlock()
		return r.stuck, nil
	}
	r.mu.Unlock()
	return r.fakeRunner.Start(bin, args)
}

// Post-merge review: one stall restarts ffmpeg once. The stall watchdog used to
// leave a restart token behind after killing the stuck ffmpeg, which ended the
// next ffmpeg at once (an extra RTMPS/SRT reconnect per stall).
func TestRelayOneStallOneRestart(t *testing.T) {
	src := &fakeSource{gens: []*tsGen{newGen(0x1000, 0x100, 0x101, 0)}, frames: []int{1 << 20}, block: make(chan struct{}),
		pace: 200 * time.Microsecond}
	run := &stallOnceRunner{}
	s := &Session{PushID: "p1", Channel: uuid.New(), Creds: Credentials{URL: "rtmps://x/live/", Key: "k", Ingest: "rtmps"},
		Source: src, Runner: run, Stall: 30 * time.Millisecond, Restart: 5 * time.Millisecond, KillGrace: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitUntil(t, "a second ffmpeg", func() bool { return run.count() >= 2 })
	time.Sleep(300 * time.Millisecond)
	if n := run.count(); n != 2 {
		t.Errorf("one stall started ffmpeg %d times, want 2", n)
	}
	cancel()
	<-done
}

// Post-merge review: Kick before Run (Ctl registers the session first) is safe.
func TestRelayKickBeforeRun(_ *testing.T) {
	src := &fakeSource{gens: []*tsGen{newGen(0x1000, 0x100, 0x101, 0)}, frames: []int{10}, block: make(chan struct{})}
	s := &Session{PushID: "p1", Channel: uuid.New(), Source: src, Runner: &fakeRunner{}, Restart: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	s.Kick()
	cancel()
	<-done
}

// Post-merge review: a new source whose 33-bit clock wraps between its first
// video keyframe and its first audio packet keeps its audio. Each PID used to be
// unwrapped from its own first value, so the audio landed 2^33 behind the video
// and every audio PES of the source was dropped as "before the last audio".
func TestRetimerSwitchAcrossWrapKeepsAudio(t *testing.T) {
	r := NewRetimer()
	a := newGen(0x1000, 0x100, 0x101, 900_000)
	o, err := r.Process(a.frames(60))
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte(nil), o...)
	before := len(readTrack(t, out).dts[0x101])
	r.Switch()
	b := newGen(0x1000, 0x100, 0x101, ptsWrap-3000) // the IDR just before the wrap
	b.apts = ptsWrap + 100                          // its audio just after it
	o, err = r.Process(b.frames(60))
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, o...)
	tr := readTrack(t, out)
	if got := len(tr.dts[0x101]) - before; got < 50 {
		t.Fatalf("only %d audio packets of the new source reached the output", got)
	}
	monotonic(t, "video", tr.dts[0x100], 6000)
	monotonic(t, "audio", tr.dts[0x101], 12000)
}
