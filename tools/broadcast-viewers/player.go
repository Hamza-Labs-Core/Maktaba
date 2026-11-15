package viewers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is one simulated player.
type Config struct {
	// Base is the edge's live origin (http://edge:8787).
	Base    string
	Pub     string
	Channel string
	Session string
	// Location is the simulated Cloudflare location (the X-Rehearsal-Location header).
	Location int
	// Rung is the rung it plays; "" is the master's first.
	Rung string
	// Token mints a viewer token for the session (watch.maktaba.tv's part); called again before
	// the one it holds expires.
	Token func(session string) (string, error)
	HTTP  *http.Client
	// BeatEvery is the heartbeat period (default 60 s, the edge counts one a minute).
	BeatEvery time.Duration
	// Published is when the home finished uploading a segment (its PUT answered), for the
	// latency after the home; false when unknown.
	Published func(rung string, seq int64) (time.Time, bool)
}

// State is what a player shows.
type State string

const (
	StateStarting State = "starting"
	StateLive     State = "live"
	StateSlate    State = "slate"
	StateOffAir   State = "off_air"
	StateWaiting  State = "waiting"
)

// PlayerStats is one player's record.
type PlayerStats struct {
	Requests     map[string]int
	Segments     int
	SegmentBytes int64
	// Fetch is each segment's request to its last byte; AfterHome is its last byte after the
	// home finished uploading it (live segments of a known upload only).
	Fetch, AfterHome []time.Duration
	Errors           []string
	State            State
	FirstSlate       time.Time
	FirstOffAir      time.Time
	LastLive         time.Time
	// LiveAgain is the first live segment after an off-air or a slate spell.
	LiveAgain time.Time
}

// Player plays one session: master, then its rung's playlist every target duration, each new
// segment once, a heartbeat every BeatEvery, as hls.js and AVPlayer do.
type Player struct {
	cfg Config

	mu      sync.Mutex
	st      PlayerStats
	token   string
	tokenAt time.Time
	seen    map[string]bool
	order   []string
}

// NewPlayer makes a player; Run plays it.
func NewPlayer(c Config) *Player {
	if c.BeatEvery <= 0 {
		c.BeatEvery = time.Minute
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	return &Player{cfg: c, st: PlayerStats{Requests: map[string]int{}, State: StateStarting}, seen: map[string]bool{}}
}

// Stats is a copy of the player's record.
func (p *Player) Stats() PlayerStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.st
	s.Requests = map[string]int{}
	for k, v := range p.st.Requests {
		s.Requests[k] = v
	}
	s.Fetch = append([]time.Duration(nil), p.st.Fetch...)
	s.AfterHome = append([]time.Duration(nil), p.st.AfterHome...)
	s.Errors = append([]string(nil), p.st.Errors...)
	return s
}

func (p *Player) fail(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.st.Errors = append(p.st.Errors, fmt.Sprintf(format, a...))
}

func (p *Player) setState(s State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	prev := p.st.State
	switch s {
	case StateSlate:
		if p.st.FirstSlate.IsZero() {
			p.st.FirstSlate = now
		}
	case StateOffAir:
		if p.st.FirstOffAir.IsZero() {
			p.st.FirstOffAir = now
		}
	case StateLive:
		if (prev == StateSlate || prev == StateOffAir) && p.st.LiveAgain.IsZero() {
			p.st.LiveAgain = now
		}
		p.st.LastLive = now
	}
	p.st.State = s
}

func (p *Player) state() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st.State
}

const tokenRefresh = 5 * time.Minute // viewer tokens live 10 minutes

func (p *Player) viewerToken() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && time.Since(p.tokenAt) < tokenRefresh {
		return p.token, nil
	}
	t, err := p.cfg.Token(p.cfg.Session)
	if err != nil {
		return "", err
	}
	p.token, p.tokenAt = t, time.Now()
	return t, nil
}

type answer struct {
	status int
	code   string
	body   []byte
	retry  time.Duration
}

func (p *Player) do(ctx context.Context, kind, method, u string, body io.Reader) (answer, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return answer{}, err
	}
	req.Header.Set("X-Rehearsal-Location", strconv.Itoa(p.cfg.Location))
	req.Header.Set("X-Rehearsal-Session", p.cfg.Session)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	p.mu.Lock()
	p.st.Requests[kind]++
	p.mu.Unlock()
	resp, err := p.cfg.HTTP.Do(req)
	if err != nil {
		return answer{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return answer{}, err
	}
	a := answer{status: resp.StatusCode, body: raw}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		a.code = e.Error
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			a.retry = time.Duration(s) * time.Second
		}
	}
	return a, nil
}

func (p *Player) liveURL(path string) (string, error) {
	tok, err := p.viewerToken()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(p.cfg.Base, "/") + "/" + tok + "/" + p.cfg.Channel + "/" + path, nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Run plays until ctx ends. An off-air answer is a state, not an error: the player shows the
// off-air card and asks for the master again every few seconds, as the apps do.
func (p *Player) Run(ctx context.Context) {
	for ctx.Err() == nil {
		rung, ok := p.master(ctx)
		if !ok {
			continue
		}
		p.play(ctx, rung)
	}
}

const retryEvery = 4 * time.Second

// master asks for the master playlist until it is one (or ctx ends), and picks the rung.
func (p *Player) master(ctx context.Context) (string, bool) {
	u, err := p.liveURL("master.m3u8")
	if err != nil {
		p.fail("token: %v", err)
		sleep(ctx, retryEvery)
		return "", false
	}
	a, err := p.do(ctx, "master", http.MethodGet, u, nil)
	switch {
	case ctx.Err() != nil:
		return "", false
	case err != nil:
		sleep(ctx, time.Second) // a connection refused: retried, an error only if it lasts (play)
		return "", false
	case a.status == http.StatusServiceUnavailable && a.code == "off_air":
		p.setState(StateOffAir)
		sleep(ctx, retryEvery)
		return "", false
	case a.status == http.StatusTooManyRequests:
		p.setState(StateWaiting)
		sleep(ctx, max(a.retry, retryEvery))
		return "", false
	case a.status != http.StatusOK:
		p.fail("master: %d %s", a.status, a.code)
		sleep(ctx, retryEvery)
		return "", false
	}
	rungs := Variants(string(a.body))
	if len(rungs) == 0 {
		p.fail("master: no variant")
		sleep(ctx, retryEvery)
		return "", false
	}
	if p.cfg.Rung != "" {
		for _, r := range rungs {
			if r == p.cfg.Rung {
				return r, true
			}
		}
		p.fail("master: no rung %s in %v", p.cfg.Rung, rungs)
		sleep(ctx, retryEvery)
		return "", false
	}
	return rungs[0], true
}

// play follows one rung until it goes off air (back to master) or ctx ends.
func (p *Player) play(ctx context.Context, rung string) {
	nextBeat := time.Now()
	first := true
	failing := time.Time{}
	for ctx.Err() == nil {
		started := time.Now()
		if !started.Before(nextBeat) {
			if !p.beat(ctx, rung) {
				return
			}
			nextBeat = started.Add(p.cfg.BeatEvery)
		}
		u, err := p.liveURL(rung + "/index.m3u8")
		if err != nil {
			p.fail("token: %v", err)
			return
		}
		a, err := p.do(ctx, "playlist", http.MethodGet, u, nil)
		target := float64(4)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil || a.status >= 500 && a.code != "off_air":
			// the edge unreachable for a moment (a deploy): an error once it lasts a target
			// duration and more, as a player's buffer runs dry
			if failing.IsZero() {
				failing = started
			} else if time.Since(failing) > 2*time.Duration(target)*time.Second {
				p.fail("playlist: unreachable since %s (%v %d)", failing.Format(time.RFC3339), err, a.status)
				failing = time.Now()
			}
			sleep(ctx, time.Second)
			continue
		case a.status == http.StatusServiceUnavailable && a.code == "off_air":
			p.setState(StateOffAir)
			return
		case a.status != http.StatusOK:
			p.fail("playlist: %d %s", a.status, a.code)
			sleep(ctx, retryEvery)
			return
		}
		failing = time.Time{}
		pl := ParseMedia(string(a.body))
		if pl.Target > 0 {
			target = pl.Target
		}
		base, _ := url.Parse(u)
		if first && len(pl.Segments) > 0 {
			// start at the live edge: the newest segment only
			for _, s := range pl.Segments[:len(pl.Segments)-1] {
				p.mark(s.URI)
			}
			first = false
		}
		for _, s := range pl.Segments {
			if p.isSeen(s.URI) {
				continue
			}
			p.mark(s.URI)
			ref, err := url.Parse(s.URI)
			if err != nil {
				p.fail("segment uri %q", s.URI)
				continue
			}
			p.segment(ctx, rung, s, base.ResolveReference(ref).String(), time.Duration(target*float64(time.Second)))
		}
		if n := len(pl.Segments); n > 0 {
			if pl.Segments[n-1].Slate {
				p.setState(StateSlate)
			} else {
				p.setState(StateLive)
			}
		} else if p.state() == StateStarting {
			p.setState(StateWaiting)
		}
		if !sleep(ctx, time.Duration(target*float64(time.Second))-time.Since(started)) {
			return
		}
	}
}

// segment fetches one segment, retried until a target duration has passed (a player's buffer).
func (p *Player) segment(ctx context.Context, rung string, s MediaSegment, u string, budget time.Duration) {
	deadline := time.Now().Add(budget)
	for {
		t0 := time.Now()
		a, err := p.do(ctx, "segment", http.MethodGet, u, nil)
		if ctx.Err() != nil {
			return
		}
		if err == nil && a.status == http.StatusOK {
			done := time.Now()
			p.mu.Lock()
			p.st.Segments++
			p.st.SegmentBytes += int64(len(a.body))
			p.st.Fetch = append(p.st.Fetch, done.Sub(t0))
			p.mu.Unlock()
			if !s.Slate && p.cfg.Published != nil {
				if seq, ok := SegmentSeq(s.URI); ok {
					if at, ok := p.cfg.Published(rung, seq); ok {
						p.mu.Lock()
						p.st.AfterHome = append(p.st.AfterHome, done.Sub(at))
						p.mu.Unlock()
					}
				}
			}
			return
		}
		if err == nil && a.status == http.StatusServiceUnavailable && a.code == "off_air" {
			p.setState(StateOffAir)
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				p.fail("segment %s: %v", s.URI, err)
			} else {
				p.fail("segment %s: %d %s", s.URI, a.status, a.code)
			}
			return
		}
		sleep(ctx, 250*time.Millisecond)
	}
}

// beat is the heartbeat (story 50.20/50.28): false when the channel went off air.
func (p *Player) beat(ctx context.Context, rung string) bool {
	u, err := p.liveURL("beat")
	if err != nil {
		return true
	}
	body, _ := json.Marshal(map[string]any{"rung": rung, "rebuffer_ms": 0, "device": "web"})
	a, err := p.do(ctx, "beat", http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil || ctx.Err() != nil {
		return true // a lost beat is resent with the next
	}
	if a.status == http.StatusServiceUnavailable && a.code == "off_air" {
		p.setState(StateOffAir)
		return false
	}
	if a.status != http.StatusOK && a.status < 500 {
		p.fail("beat: %d %s", a.status, a.code)
	}
	return true
}

const seenMax = 128

func (p *Player) mark(uri string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen[uri] {
		return
	}
	p.seen[uri] = true
	p.order = append(p.order, uri)
	if len(p.order) > seenMax {
		delete(p.seen, p.order[0])
		p.order = p.order[1:]
	}
}

func (p *Player) isSeen(uri string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[uri]
}

// MediaSegment is one segment of a media playlist.
type MediaSegment struct {
	URI           string
	Duration      float64
	Discontinuity bool
	// Slate: the channel's offline slate (../slate/…), not the home's live picture.
	Slate bool
}

// MediaPlaylist is what a player reads of a media playlist.
type MediaPlaylist struct {
	Target   float64
	Sequence int64
	Segments []MediaSegment
}

// ParseMedia reads an HLS media playlist.
func ParseMedia(text string) MediaPlaylist {
	var pl MediaPlaylist
	var dur float64
	disc := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			pl.Target, _ = strconv.ParseFloat(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"), 64)
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			pl.Sequence, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			dur, _ = strconv.ParseFloat(v, 64)
		case line == "#EXT-X-DISCONTINUITY":
			disc = true
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			pl.Segments = append(pl.Segments, MediaSegment{URI: line, Duration: dur, Discontinuity: disc,
				Slate: strings.Contains(line, "slate/")})
			dur, disc = 0, false
		}
	}
	return pl
}

// Variants are a master playlist's rungs (the first path element of each variant's URI).
func Variants(text string) []string {
	var out []string
	next := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF"):
			next = true
		case next && line != "" && !strings.HasPrefix(line, "#"):
			out = append(out, strings.SplitN(line, "/", 2)[0])
			next = false
		}
	}
	return out
}

// SegmentSeq is a segment's sequence number from its name (<seq>-<hash8>.ts).
func SegmentSeq(uri string) (int64, bool) {
	name := uri[strings.LastIndexByte(uri, '/')+1:]
	i := strings.IndexByte(name, '-')
	if i <= 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(name[:i], 10, 64)
	return n, err == nil
}
