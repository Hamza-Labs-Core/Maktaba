package broadcast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/relay"
)

// Job is a published channel the home should broadcast, with its ingest token.
type Job struct {
	Channel uuid.UUID
	Name    string
	// Rungs are the effective ladder (the newest drops already removed), top first.
	Rungs []string
	// Mode is always_on or on_demand.
	Mode  string
	State string
	Creds Creds
}

// Store is where jobs come from and what the broadcaster writes back.
type Store interface {
	Jobs(ctx context.Context) ([]Job, error)
	SetState(ctx context.Context, ch uuid.UUID, state, detail string) error
	Report(ctx context.Context, ch uuid.UUID, report map[string]any) error
}

// Output is the channels' own encode (tv_output) as the ladder needs it.
type Output struct {
	// Encoder is the resolved family (videotoolbox, nvenc, qsv, vaapi, software).
	Encoder              string
	Width, Height, FPS   int
	KeyintSeconds        int
	VideoKbps, AudioKbps int
	AllowSoftware        bool
}

// Timings.
const (
	// SleepGrace: an on-demand channel stops this long after the edge reports no viewers.
	SleepGrace = 5 * time.Minute
	// StatePeriod: the rungs' state is posted this often.
	StatePeriod = 10 * time.Second
)

// Ctl runs the home's broadcasters: one Session per published channel the home should be
// sending now (always-on; on demand while woken), stopped when the channel leaves.
type Ctl struct {
	Store  Store
	Source relay.Source
	Runner relay.Runner
	FFmpeg string
	// Dir holds a directory per channel (the segments on their way out).
	Dir    string
	Output func(ctx context.Context) Output
	HTTP   *http.Client
	Logger *slog.Logger
	Now    func() time.Time
	// Poll (default 5 s), StatePeriod (default 10 s) and SleepGrace (default 5 min);
	// RestartDelay is the wait before a crashed ffmpeg starts again (default 500 ms).
	Poll, StateEvery, Grace, RestartDelay time.Duration

	mu       sync.Mutex
	jobs     map[uuid.UUID]Job
	running  map[uuid.UUID]*session
	woken    map[uuid.UUID]time.Time // on demand: when it was woken
	sleepAt  map[uuid.UUID]time.Time // on demand: when the edge said no one watches
	limits   map[uuid.UUID][]string
	wake     chan struct{}
	slateFor map[uuid.UUID]string // the token a channel's slate was uploaded with
	// demandAware: the edge has sent a wake or a sleep (it tells demand)
	demandAware bool
}

type session struct {
	key    string
	cancel context.CancelFunc
	done   chan struct{}
	up     *Uploader
	stats  *stats
}

type stats struct {
	mu       sync.Mutex
	sent     map[string]int
	bytes    int64
	restarts int
	started  time.Time
	last     time.Time
}

func (c *Ctl) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Ctl) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Ctl) init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running == nil {
		c.jobs = map[uuid.UUID]Job{}
		c.running = map[uuid.UUID]*session{}
		c.woken = map[uuid.UUID]time.Time{}
		c.sleepAt = map[uuid.UUID]time.Time{}
		c.limits = map[uuid.UUID][]string{}
		c.slateFor = map[uuid.UUID]string{}
		c.wake = make(chan struct{}, 1)
	}
}

func (c *Ctl) kick() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run syncs until ctx ends, then stops every broadcaster (each posts on_air false).
func (c *Ctl) Run(ctx context.Context) {
	c.init()
	poll := c.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		c.Sync(ctx)
		select {
		case <-ctx.Done():
			c.stopAll()
			return
		case <-t.C:
		case <-c.wake:
		}
	}
}

// creds is a channel's current token (refreshed by the API in broadcast_ingest_tokens).
func (c *Ctl) creds(ch uuid.UUID) (Creds, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	j, ok := c.jobs[ch]
	return j.Creds, ok && j.Creds.Token != ""
}

// Jobs is the channels last read (the control sockets follow it).
func (c *Ctl) Jobs() []Job {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Job, 0, len(c.jobs))
	for _, j := range c.jobs {
		out = append(out, j)
	}
	return out
}

// wanted reports whether a job should be sending now.
func (c *Ctl) wanted(j Job, now time.Time) bool {
	if j.State != "live" || j.Creds.Token == "" || (!j.Creds.ExpiresAt.IsZero() && !now.Before(j.Creds.ExpiresAt)) {
		return false
	}
	// on demand needs an edge that tells demand: until one has (a wake or a sleep), an
	// on-demand channel runs as if always on, so a published channel is never dark
	if j.Mode != "on_demand" || !c.demandAware {
		return true
	}
	if _, ok := c.woken[j.Channel]; !ok {
		return false
	}
	at, asleep := c.sleepAt[j.Channel]
	return !asleep || now.Before(at)
}

func (c *Ctl) rungsFor(j Job) []string {
	lim, ok := c.limits[j.Channel]
	if !ok {
		return j.Rungs
	}
	allowed := map[string]bool{}
	for _, r := range lim {
		allowed[r] = true
	}
	var out []string
	for _, r := range j.Rungs {
		if allowed[r] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return j.Rungs[len(j.Rungs)-1:]
	}
	return out
}

// Sync starts, restarts (the ladder changed) and stops broadcasters to match the jobs.
func (c *Ctl) Sync(ctx context.Context) {
	c.init()
	jobs, err := c.Store.Jobs(ctx)
	if err != nil {
		c.log().Warn("broadcast: reading the published channels failed", "err", err, "event", "broadcast_sync_error")
		return
	}
	now := c.now()
	c.mu.Lock()
	c.jobs = map[uuid.UUID]Job{}
	want := map[uuid.UUID]string{}
	byID := map[uuid.UUID]Job{}
	for _, j := range jobs {
		c.jobs[j.Channel] = j
		if j.Creds.ExpiresAt.IsZero() || now.Before(j.Creds.ExpiresAt) {
			if j.State == "live" || j.State == "offline" {
				byID[j.Channel] = j
			}
		} else if j.State == "live" {
			go func(ch uuid.UUID) { _ = c.Store.SetState(context.WithoutCancel(ctx), ch, "offline", "tokenExpired") }(j.Channel)
		}
		if c.wanted(j, now) {
			j.Rungs = c.rungsFor(j)
			want[j.Channel] = strings.Join(j.Rungs, ",")
			byID[j.Channel] = j
		}
	}
	var stop []*session
	for ch, s := range c.running {
		if k, ok := want[ch]; !ok || k != s.key {
			stop = append(stop, s)
			delete(c.running, ch)
		}
	}
	var start []Job
	for ch, k := range want {
		if _, ok := c.running[ch]; !ok {
			j := byID[ch]
			j.Rungs = strings.Split(k, ",")
			start = append(start, j)
		}
	}
	var slates []Job
	for _, j := range byID {
		if c.slateFor[j.Channel] != j.Creds.Token && j.Creds.Token != "" {
			c.slateFor[j.Channel] = j.Creds.Token
			slates = append(slates, j)
		}
	}
	for ch := range c.woken {
		if _, ok := c.jobs[ch]; !ok {
			delete(c.woken, ch)
			delete(c.sleepAt, ch)
			delete(c.limits, ch)
		}
	}
	c.mu.Unlock()

	for _, s := range stop {
		s.cancel()
		<-s.done
	}
	for _, j := range start {
		c.start(ctx, j)
	}
	for _, j := range slates {
		go c.uploadSlate(context.WithoutCancel(ctx), j)
	}
}

func (c *Ctl) stopAll() {
	c.mu.Lock()
	all := c.running
	c.running = map[uuid.UUID]*session{}
	c.mu.Unlock()
	for _, s := range all {
		s.cancel()
		<-s.done
	}
}

// Running is the channels broadcasting now.
func (c *Ctl) Running() []uuid.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]uuid.UUID, 0, len(c.running))
	for ch := range c.running {
		out = append(out, ch)
	}
	return out
}

// Event handles a control socket event for ch.
func (c *Ctl) Event(ctx context.Context, ch uuid.UUID, ev Event) {
	c.init()
	grace := c.Grace
	if grace <= 0 {
		grace = SleepGrace
	}
	switch ev.Type {
	case "wake":
		c.mu.Lock()
		c.demandAware = true
		c.woken[ch] = c.now()
		delete(c.sleepAt, ch)
		c.mu.Unlock()
		c.log().Info("broadcast: the edge woke the channel", "channel", ch, "event", "broadcast_wake")
	case "sleep":
		c.mu.Lock()
		c.demandAware = true
		if _, ok := c.woken[ch]; ok || c.running[ch] != nil {
			// running (woken, or on the fallback before the edge told demand): the grace first
			c.woken[ch] = c.now()
			c.sleepAt[ch] = c.now().Add(grace)
		}
		c.mu.Unlock()
		time.AfterFunc(grace, c.kick)
	case "limits":
		c.mu.Lock()
		if len(ev.Rungs) > 0 {
			c.limits[ch] = append([]string(nil), ev.Rungs...)
		} else {
			delete(c.limits, ch)
		}
		c.mu.Unlock()
	case "revoked":
		// Maktaba's kill switch (50.25): the reason (notice, report, incident, ...) is what the
		// Broadcast page shows; the list's own entries are the API's (detail "list:<reason>")
		_ = c.Store.SetState(ctx, ch, "revoked", ev.Reason)
	case "plan_off":
		_ = c.Store.SetState(ctx, ch, "offline", "planOff")
	case "restored":
		_ = c.Store.SetState(ctx, ch, "live", "")
	case "rung_stalled":
		c.log().Warn("broadcast: a rung stalled at the edge", "channel", ch, "rung", ev.Rung, "event", "broadcast_rung_stalled")
	default:
		return
	}
	c.kick()
}

// refusal handles an edge refusal heard by a channel's uploader.
func (c *Ctl) refusal(ctx context.Context, ch uuid.UUID, code string) {
	switch code {
	case "revoked":
		_ = c.Store.SetState(ctx, ch, "revoked", "")
	case "broadcast_off":
		_ = c.Store.SetState(ctx, ch, "offline", "planOff")
	case "tokenExpired":
		_ = c.Store.SetState(ctx, ch, "offline", "tokenExpired")
	case "clockSkew":
		_ = c.Store.SetState(ctx, ch, "live", "clockSkew")
		return
	case "plan_rungs":
		c.mu.Lock()
		if j, ok := c.jobs[ch]; ok && len(j.Rungs) > 1 {
			c.limits[ch] = append([]string(nil), c.rungsFor(j)[1:]...)
		}
		c.mu.Unlock()
	default:
		return
	}
	c.kick()
}

func (c *Ctl) start(parent context.Context, j Job) {
	ctx, cancel := context.WithCancel(parent)
	st := &stats{sent: map[string]int{}, started: c.now()}
	ch := j.Channel
	up := &Uploader{HTTP: c.HTTP, Now: c.Now,
		Creds:     func() (Creds, bool) { return c.creds(ch) },
		OnRefusal: func(code string) { c.refusal(context.WithoutCancel(ctx), ch, code) },
		Uploaded: func(rung string, _ int64, n int) {
			st.mu.Lock()
			st.sent[rung]++
			st.bytes += int64(n)
			st.last = time.Now()
			st.mu.Unlock()
		}}
	s := &session{key: strings.Join(j.Rungs, ","), cancel: cancel, done: make(chan struct{}), up: up, stats: st}
	c.mu.Lock()
	c.running[ch] = s
	c.mu.Unlock()
	c.log().Info("broadcast: started", "channel", ch, "rungs", s.key, "event", "broadcast_started")
	go func() {
		defer close(s.done)
		c.runSession(ctx, j, s)
		c.postState(context.WithoutCancel(ctx), j, false)
		c.log().Info("broadcast: stopped", "channel", ch, "event", "broadcast_stopped")
	}()
}

// runSession holds the channel (Attach: never released by the warm grace while it runs),
// and keeps one ladder ffmpeg fed with the channel's TS, its rungs uploaded, until ctx ends.
func (c *Ctl) runSession(ctx context.Context, j Job, s *session) {
	release := c.Source.Attach(j.Channel)
	defer release()
	dir := filepath.Join(c.Dir, j.Channel.String())
	defer func() { _ = os.RemoveAll(dir) }()
	go c.stateLoop(ctx, j, s)
	delay := c.RestartDelay
	if delay <= 0 {
		delay = relay.RestartDelay
	}
	first := true
	for ctx.Err() == nil {
		if !first {
			s.stats.mu.Lock()
			s.stats.restarts++
			s.stats.mu.Unlock()
			for _, r := range j.Rungs {
				s.up.markNext(r) // a new ffmpeg: its first segment follows a gap
			}
		}
		first = false
		if err := c.runLadder(ctx, j, s, dir); err != nil {
			if errors.Is(err, ErrSoftwareRungs) {
				_ = c.Store.SetState(context.WithoutCancel(ctx), j.Channel, "offline", "softwareRungs")
				return
			}
			c.log().Warn("broadcast: the ladder ended; restarting it", "channel", j.Channel, "err", err,
				"event", "broadcast_ladder_restart")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// feed is the io.Writer the source writes into: retimed across source switches, then
// written to ffmpeg.
type feed struct {
	rt *relay.Retimer
	in io.Writer
}

func (f *feed) Write(b []byte) (int, error) {
	out, err := f.rt.Process(b)
	if err != nil {
		return 0, err
	}
	if len(out) > 0 {
		if _, err := f.in.Write(out); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

func (c *Ctl) runLadder(ctx context.Context, j Job, s *session, dir string) error {
	out := Output{FPS: 30, KeyintSeconds: 2}
	if c.Output != nil {
		out = c.Output(ctx)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, r := range j.Rungs {
		if err := os.MkdirAll(filepath.Join(dir, r), 0o700); err != nil {
			return err
		}
	}
	args, err := Args(LadderSpec{Rungs: j.Rungs, Encoder: out.Encoder, AllowSoftware: out.AllowSoftware,
		KeyintSeconds: out.KeyintSeconds, FPS: out.FPS, Dir: dir,
		// the start time in segment slots: a restarted ladder never reuses a number
		StartNumber: c.now().Unix() / SegmentSeconds})
	if err != nil {
		return err
	}
	runner := c.Runner
	if runner == nil {
		runner = relay.ExecRunner{}
	}
	bin := c.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	proc, err := runner.Start(bin, args)
	if err != nil {
		return err
	}
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for _, r := range j.Rungs {
		w := &RungWatcher{Dir: filepath.Join(dir, r), Rung: r, Up: s.up}
		wg.Add(1)
		go func() { defer wg.Done(); w.Run(lctx) }()
	}
	exited := make(chan error, 1)
	go func() { exited <- proc.Wait() }()
	// the source: streamed again at once when it ends (the channel moved, a host failed)
	wg.Add(1)
	go func() {
		defer wg.Done()
		rt := relay.NewRetimer()
		started := false
		for lctx.Err() == nil {
			err := c.Source.Stream(lctx, j.Channel, &feed{rt: rt, in: proc.Stdin()}, func(relay.Origin) {
				if started {
					rt.Switch()
				}
				started = true
			})
			if errors.Is(err, relay.ErrParamChange) {
				proc.Kill() // other streams: a new ladder
				return
			}
			select {
			case <-lctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
	var werr error
	select {
	case <-ctx.Done():
		_ = proc.Stdin().Close()
		proc.Kill()
		<-exited // stopped on purpose: not an error
	case werr = <-exited:
		if werr == nil {
			werr = errors.New("ffmpeg exited")
		}
		werr = fmt.Errorf("%w: %s", werr, proc.Tail())
	}
	cancel()
	wg.Wait()
	return werr
}

// RungState is one rung of a state post (the master playlist's attributes).
type RungState struct {
	Name       string  `json:"name"`
	Bandwidth  int     `json:"bandwidth"`
	Resolution string  `json:"resolution"`
	Codecs     string  `json:"codecs"`
	FrameRate  float64 `json:"frame_rate"`
}

// States is the rungs' attributes for the edge's master playlist.
func States(rungs []string, out Output) []RungState {
	fps := out.FPS
	if fps <= 0 {
		fps = 30
	}
	var res []RungState
	for _, name := range rungs {
		w, h, kbps := out.Width, out.Height, out.VideoKbps+out.AudioKbps
		if r, ok := LowerRung(name); ok {
			h, kbps = r.Height, r.VideoKbps+r.AudioKbps
			w = (h*16/9 + 1) &^ 1
		}
		res = append(res, RungState{Name: name, Bandwidth: kbps * 1100, Resolution: fmt.Sprintf("%dx%d", w, h),
			Codecs: "avc1.640028,mp4a.40.2", FrameRate: float64(fps)})
	}
	return res
}

func (c *Ctl) stateLoop(ctx context.Context, j Job, s *session) {
	every := c.StateEvery
	if every <= 0 {
		every = StatePeriod
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		c.postState(ctx, j, true)
		c.report(ctx, j, s)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Ctl) report(ctx context.Context, j Job, s *session) {
	s.stats.mu.Lock()
	sent := map[string]int{}
	for k, v := range s.stats.sent {
		sent[k] = v
	}
	rep := map[string]any{"rungs": j.Rungs, "segments": sent, "bytes": s.stats.bytes, "restarts": s.stats.restarts,
		"started_at": s.stats.started.UTC().Format(time.RFC3339)}
	if !s.stats.last.IsZero() {
		rep["last_segment_at"] = s.stats.last.UTC().Format(time.RFC3339)
	}
	s.stats.mu.Unlock()
	_ = c.Store.Report(ctx, j.Channel, rep)
}

// postState sends POST /v1/ingest/<channel>/state.
func (c *Ctl) postState(ctx context.Context, j Job, onAir bool) {
	cr, ok := c.creds(j.Channel)
	if !ok {
		cr = j.Creds // unpublished: the token it ran with still says so
	}
	if cr.Token == "" {
		return
	}
	out := Output{}
	if c.Output != nil {
		out = c.Output(ctx)
	}
	body, _ := json.Marshal(map[string]any{"rungs": States(j.Rungs, out), "on_air": onAir})
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := strings.TrimRight(cr.IngestURL, "/") + "/v1/ingest/" + cr.Channel + "/state"
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+cr.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Maktaba-Time", strconv.FormatInt(c.now().UnixMilli(), 10))
	cl := c.HTTP
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}
