package timelimit

// Pacing (Epic 57 story 57.14, review finding 4). The time rules count wall-clock
// time: the seconds a session's lease runs. That only holds if a restricted
// account's media reaches it no faster than it plays. Without a pace, a client
// could pull a two-hour file inside one 60-second lease and watch its own copy with
// a minute counted.
//
// So what is served to a URL that names a session (the tls claim: an account whose
// time is kept) is paced to playback rate, with a bounded read-ahead:
//
//	one bucket per account and video, holding media time that may be served now
//	it starts full, at ReadAhead, so a player fills its buffer at once
//	it refills by one second of media for each second of wall clock, up to ReadAhead
//	serving takes from it: direct play by bytes over the file's average byte rate,
//	  HLS and DASH by the segment's length
//	a request that finds it empty waits until what was served before has played out
//
// The account can therefore hold at most ReadAhead of media it was not counted for,
// however it asks: a new session of the same video shares the bucket, and a second
// rendition of a segment already served costs nothing more. An account whose time
// is not kept (a URL with tk "0") never meets any of this.
//
// Direct play is paced on the file's average byte rate, so a long scene far above
// the average is delivered more slowly than it plays once the read-ahead is spent.
// ReadAhead is two minutes to keep that rare.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

const (
	// ReadAhead is the media a kept account may be served ahead of the clock.
	ReadAhead = 120 * time.Second
	// MaxPaceWait is the longest a request waits for its turn. One that would wait
	// longer (a client asking for far more than plays) is refused instead.
	MaxPaceWait = 30 * time.Second

	bucketsKept = 4096
	chargedKept = 8192
	// fallbackByteRate paces direct play of a file whose length in time and bit
	// rate are both unknown: 8 Mbit/s.
	fallbackByteRate = 1 << 20
)

// ErrTooFast ends a response to a client asking for media far faster than it plays.
var ErrTooFast = errors.New("timelimit: media asked for faster than it plays")

type bucket struct {
	credit time.Duration // media that may be served now; negative: served ahead
	at     time.Time     // credit is as of here
	// charged are the segments already paid for ("session|index"): the same
	// segment in another rendition, or its audio beside its video, costs nothing
	charged map[string]struct{}
}

func (g *Registry) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	if g.Sleep != nil {
		return g.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reserve takes media from key's bucket and says how long the caller waits before
// it serves it. once, when set, names a segment: one already paid for is free. ok
// is false when the wait would pass MaxPaceWait, or the bucket cannot be kept:
// nothing is taken and nothing is served.
func (g *Registry) reserve(key string, media time.Duration, once string) (wait time.Duration, ok bool) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.pace[key]
	if b == nil {
		if g.pace == nil {
			g.pace = map[string]*bucket{}
		}
		if len(g.pace) >= bucketsKept {
			// a bucket that has refilled is the same as none
			for k, o := range g.pace {
				if o.credit+now.Sub(o.at) >= ReadAhead {
					delete(g.pace, k)
				}
			}
			if len(g.pace) >= bucketsKept {
				return 0, false
			}
		}
		b = &bucket{credit: ReadAhead, at: now}
		g.pace[key] = b
	}
	if d := now.Sub(b.at); d > 0 {
		b.credit = min(ReadAhead, b.credit+d)
		b.at = now
	}
	if once != "" {
		if _, paid := b.charged[once]; paid {
			return 0, true
		}
	}
	if b.credit < 0 {
		wait = -b.credit
	}
	if wait > MaxPaceWait {
		return wait, false
	}
	b.credit -= media
	if once != "" {
		if b.charged == nil || len(b.charged) >= chargedKept {
			b.charged = map[string]struct{}{}
		}
		b.charged[once] = struct{}{}
	}
	return wait, true
}

// Pacer paces what one request serves. A nil Pacer paces nothing: the request's
// account is not kept.
type Pacer struct {
	g    *Registry
	user string
}

type pacerKey struct{}

// PacerFrom is the request's pacer, set by Guard for a URL that names a session;
// nil for every other request.
func PacerFrom(ctx context.Context) *Pacer {
	p, _ := ctx.Value(pacerKey{}).(*Pacer)
	return p
}

// segmentIndex reads a segment's number from its name (seg-12.ts,
// chunk-stream0-00012.m4s): the digits before the extension.
func segmentIndex(name string) (int, bool) {
	stem, _, _ := strings.Cut(name, ".")
	end := len(stem)
	start := end
	for start > 0 && stem[start-1] >= '0' && stem[start-1] <= '9' {
		start--
	}
	if start == end || end-start > 9 {
		return 0, false
	}
	n, err := strconv.Atoi(stem[start:end])
	return n, err == nil
}

// Segment waits until the account may be served the segment name of sessionID, a
// stream of video, which holds length of media. It reports false when the segment
// is not to be served: the client went away, or it asked for far more than plays
// and was answered 429. An init segment holds no media and is free.
func (p *Pacer) Segment(w http.ResponseWriter, r *http.Request, video, sessionID, name string, length time.Duration) bool {
	if p == nil || strings.HasPrefix(name, "init") {
		return true
	}
	once := ""
	if n, ok := segmentIndex(name); ok {
		once = sessionID + "|" + strconv.Itoa(n)
	}
	wait, ok := p.g.reserve(p.user+"|"+video, length, once)
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(MaxPaceWait/time.Second)))
		httpx.Write(w, http.StatusTooManyRequests, "media-too-fast", "Too fast",
			"this account's media is served as it plays; ask again shortly")
		return false
	}
	return p.g.sleep(r.Context(), wait) == nil
}

// ByteRate is the pace of direct play of a file of size bytes: its average byte
// rate from its length in time, else from its probed bit rate, else a fallback.
func ByteRate(size int64, durationSec float64, bitrateKbps int) float64 {
	switch {
	case size > 0 && durationSec > 0:
		return float64(size) / durationSec
	case bitrateKbps > 0:
		return float64(bitrateKbps) * 125
	}
	return fallbackByteRate
}

// Writer is w, paced: the bytes of video written through it are served at
// bytesPerSecond with the read-ahead. For a nil Pacer it is w itself.
func (p *Pacer) Writer(ctx context.Context, w http.ResponseWriter, video string, bytesPerSecond float64) http.ResponseWriter {
	if p == nil {
		return w
	}
	if bytesPerSecond <= 0 {
		bytesPerSecond = fallbackByteRate
	}
	return &pacedWriter{ResponseWriter: w, ctx: ctx, g: p.g, key: p.user + "|" + video, rate: bytesPerSecond}
}

type pacedWriter struct {
	http.ResponseWriter
	ctx  context.Context
	g    *Registry
	key  string
	rate float64 // bytes of the file per second of media
}

func (w *pacedWriter) Write(b []byte) (int, error) {
	media := time.Duration(float64(len(b)) / w.rate * float64(time.Second))
	wait, ok := w.g.reserve(w.key, media, "")
	if !ok {
		return 0, ErrTooFast
	}
	if err := w.g.sleep(w.ctx, wait); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(b)
}

// Flush and Unwrap keep the wrapped writer's streaming behaviour.
func (w *pacedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *pacedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
