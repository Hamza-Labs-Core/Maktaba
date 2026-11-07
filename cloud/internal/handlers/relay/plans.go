package relay

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CapHeader tells the home the bitrate a relayed request's media must fit (story 50.19 §3): it
// offers only renditions within it, transcoding when the file has none. Only the relay sets it;
// a client's copy is removed.
const CapHeader = "X-Maktaba-Relay-Cap-Kbps"

// Plans are the relay's plan rules (story 50.19 §3).
type Plans struct {
	// Enforce (relay.plans.enforce): paid homes are capped and limited as their plan says.
	// Off, they are forwarded in full (counting goes on). A plan without the relay is refused
	// either way.
	Enforce bool
	// StreamLimit (relay.stream_limit.enabled): refuse a new stream past the plan's limit.
	StreamLimit bool
	// Streams counts the open streams (nil: none counted).
	Streams StreamCounter

	mu      sync.Mutex
	touched map[string]time.Time // admitted streams, by home and session: when last written
	buckets map[string]*bucket   // pacing, by home and session
}

// StreamCounter is billing.Streams.
type StreamCounter interface {
	Open(ctx context.Context, serverID, session string, limit int) (admitted bool, open int, err error)
}

// streamKey is the relayed media stream a request belongs to, and whether it is one. A video's
// session (/stream/{session}/…) is its own key; a channel or a clip has no session of its own, so
// its key adds the client's address. Artwork, playlists and subtitles are not streams.
func streamKey(path, client string) (key string, ok bool) {
	rest, found := strings.CutPrefix(path, "/stream/")
	if !found || !mediaPath(path) {
		return "", false
	}
	parts := strings.Split(rest, "/")
	switch parts[0] {
	case "posters", "sprites", "thumbs":
		return "", false
	case "channel", "clip", "staging":
		id := ""
		if parts[0] == "staging" && len(parts) > 2 {
			id = parts[2]
		} else if len(parts) > 1 {
			id = parts[1]
		}
		if id == "" {
			return "", false
		}
		return parts[0] + ":" + id + ":" + client, true
	}
	if parts[0] == "" {
		return "", false
	}
	return "session:" + parts[0], true
}

// mediaPath says whether a path under /stream/ carries media (segments, a live stream, a file),
// not a playlist, a manifest or subtitles: only media is paced and opens a stream.
func mediaPath(path string) bool {
	if !strings.HasPrefix(path, "/stream/") {
		return false
	}
	for _, ext := range []string{".m3u8", ".mpd", ".vtt", ".srt", ".json", ".jpg", ".jpeg", ".png", ".webp"} {
		if strings.HasSuffix(path, ext) {
			return false
		}
	}
	return true
}

// admit answers whether the stream of r may play (story 50.19 §3): a stream already open plays
// on; a new one past the limit is refused, and how many play is said. A stream admitted in the
// last minute is admitted again without the database (it is touched once a minute). A counting
// error admits: the database is never a reason to stop a stream.
func (p *Plans) admit(ctx context.Context, serverID, key string, limit int) (ok bool, open int) {
	k := serverID + "|" + key
	now := time.Now()
	p.mu.Lock()
	if p.touched == nil {
		p.touched = map[string]time.Time{}
	}
	last, seen := p.touched[k]
	p.mu.Unlock()
	if seen && now.Sub(last) < time.Minute {
		return true, 0
	}
	admitted, open, err := p.Streams.Open(ctx, serverID, key, limit)
	if err != nil {
		return true, 0
	}
	if admitted {
		p.mu.Lock()
		p.touched[k] = now
		if len(p.touched) > 100_000 { // forget streams long silent
			for kk, t := range p.touched {
				if now.Sub(t) > 2*time.Minute {
					delete(p.touched, kk)
				}
			}
		}
		p.mu.Unlock()
	}
	return admitted, open
}

// bucket paces one media stream at its cap plus 20 % (a token bucket in bytes): a home that
// ignores the cap header (an older release) slows to it and is never stopped.
type bucket struct {
	mu     sync.Mutex
	rate   float64 // bytes a second
	burst  float64
	tokens float64
	last   time.Time
	used   time.Time // when its stream last asked for it (under Plans.mu)
}

func newBucket(capKbps int) *bucket {
	rate := float64(capKbps) * 1000 / 8 * 1.2
	return &bucket{rate: rate, burst: rate * 2, tokens: rate * 2, last: time.Now()}
}

// wait blocks until n bytes may be sent, or ctx ends (false).
func (b *bucket) wait(ctx context.Context, n int) bool {
	for {
		b.mu.Lock()
		now := time.Now()
		b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
		b.last = now
		if b.tokens >= float64(n) || b.tokens >= b.burst {
			b.tokens -= float64(n)
			b.mu.Unlock()
			return true
		}
		need := (float64(n) - b.tokens) / b.rate
		b.mu.Unlock()
		t := time.NewTimer(time.Duration(need * float64(time.Second)))
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
	}
}

// pacer is the bucket of a home's media stream at capKbps (one per stream, so the segments of
// one player share its rate).
func (p *Plans) pacer(serverID, key string, capKbps int) *bucket {
	k := serverID + "|" + key
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buckets == nil {
		p.buckets = map[string]*bucket{}
	}
	b, ok := p.buckets[k]
	if !ok || b.rate != newBucket(capKbps).rate {
		b = newBucket(capKbps)
		p.buckets[k] = b
	}
	b.used = time.Now()
	if len(p.buckets) > 100_000 {
		now := time.Now()
		for kk, bb := range p.buckets {
			if now.Sub(bb.used) > 5*time.Minute {
				delete(p.buckets, kk)
			}
		}
	}
	return b
}

// planHeaders sets the cap the forwarded request carries (a client's own copy removed).
func planHeaders(h http.Header, capKbps int) {
	h.Del(CapHeader)
	if capKbps > 0 {
		h.Set(CapHeader, strconv.Itoa(capKbps))
	}
}
