package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
)

func TestStreamKeys(t *testing.T) {
	for path, want := range map[string]string{
		"/stream/abc123/720p/seg-00001.ts":        "session:abc123",
		"/stream/abc123/seg-00001.m4s":            "session:abc123",
		"/stream/channel/c1/live.ts":              "channel:c1:1.2.3.4",
		"/stream/channel/c1/s/blue/seg-1.ts":      "channel:c1:1.2.3.4",
		"/stream/clip/v9":                         "clip:v9:1.2.3.4",
		"/stream/staging/channel/c2/s/b/seg-1.ts": "staging:c2:1.2.3.4",
	} {
		if got, ok := streamKey(path, "1.2.3.4"); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", path, got, ok, want)
		}
	}
	for _, path := range []string{
		"/stream/abc123/manifest.m3u8", "/stream/abc123/720p/index.m3u8", "/stream/abc123/subs/sidecar/en.vtt",
		"/stream/posters/x.jpg", "/stream/sprites/x", "/stream/thumbs/v1", "/api/videos", "/stream/",
	} {
		if k, ok := streamKey(path, "1.2.3.4"); ok {
			t.Errorf("%s is a stream (%q)", path, k)
		}
	}
}

// Story 50.19 T03: a home that ignores the cap header is held to the cap plus 20 %, and every
// byte arrives (slower, never stopped).
func TestTheBucketHoldsTheCap(t *testing.T) {
	b := newBucket(8000) // 8 Mbit/s + 20 %: 1.2 MB/s, a burst of 2.4 MB
	start := time.Now()
	sent := 0
	for sent < 3_600_000 {
		if !b.wait(context.Background(), 32*1024) {
			t.Fatal("stopped")
		}
		sent += 32 * 1024
	}
	took := time.Since(start)
	// 2.4 MB of burst, then 1.2 MB at 1.2 MB/s: about a second
	if took < 800*time.Millisecond || took > 2*time.Second {
		t.Fatalf("3.6 MB took %s at 8 Mbit/s + 20 %%", took)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b.wait(ctx, 10_000_000) {
		t.Fatal("a cancelled wait sent")
	}
}

type fakeStreams struct {
	mu   sync.Mutex
	open map[string]bool
}

func (f *fakeStreams) Open(_ context.Context, _, session string, limit int) (bool, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.open == nil {
		f.open = map[string]bool{}
	}
	if f.open[session] {
		return true, len(f.open), nil
	}
	if len(f.open) >= limit {
		return false, len(f.open), nil
	}
	f.open[session] = true
	return true, len(f.open), nil
}

// Story 50.19 T02 and §3: a plan without the relay is refused (402 relay_needs_plan) with the
// tunnel kept; a capped plan's requests carry the cap and its media is paced, its playlists
// and API never; a client's own cap header never reaches the home; with the plans not enforced
// a paid home plays in full.
func TestThePlansOnTheRelay(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		refused := billing.Decision{Mode: billing.ModeRefused}
		e := newE2EWith(t, &refused, &Plans{})
		resp := e.request(context.Background(), t, "/whoami", map[string]string{"Accept": "application/json"})
		var body map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusPaymentRequired || body["error"] != "relay_needs_plan" {
			t.Fatalf("%d %v", resp.StatusCode, body)
		}
		select {
		case <-e.authSeen:
			t.Fatal("a refused request reached the home")
		default:
		}
		if _, err := e.reg.Lookup(e2eSlug); err != nil {
			t.Fatal("the tunnel went with the refusal")
		}
	})
	t.Run("capped", func(t *testing.T) {
		capped := billing.Decision{Mode: billing.ModeCapped, CapKbps: 8000}
		e := newE2EWith(t, &capped, &Plans{Enforce: true})
		resp := e.request(context.Background(), t, "/stream/s1/720p/index.m3u8", map[string]string{CapHeader: "99999"})
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.Header.Get("X-Seen-Cap") != "8000" {
			t.Fatalf("the home was asked to fit %q", resp.Header.Get("X-Seen-Cap"))
		}
		start := time.Now()
		resp = e.request(context.Background(), t, "/stream/s1/720p/seg-1.ts", nil)
		n, _ := io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if took := time.Since(start); n != 3_600_000 || took < 700*time.Millisecond {
			t.Fatalf("a capped segment: %d bytes in %s (not paced)", n, took)
		}
		e.meter.mu.Lock()
		defer e.meter.mu.Unlock()
		if e.meter.modes[billing.ModeCapped] == 0 {
			t.Fatalf("not counted as capped: %v", e.meter.modes)
		}
	})
	t.Run("not enforced", func(t *testing.T) {
		capped := billing.Decision{Mode: billing.ModeCapped, CapKbps: 3000}
		e := newE2EWith(t, &capped, &Plans{Enforce: false})
		start := time.Now()
		resp := e.request(context.Background(), t, "/stream/s1/720p/seg-1.ts", map[string]string{CapHeader: "1"})
		n, _ := io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.Header.Get("X-Seen-Cap") != "" || n != 3_600_000 || time.Since(start) > 2*time.Second {
			t.Fatalf("not enforced: cap %q, %d bytes in %s", resp.Header.Get("X-Seen-Cap"), n, time.Since(start))
		}
	})
}

// Story 50.19 §3: a new relayed stream past the plan's limit is 429 stream_limit; the ones
// playing go on; a playlist or the API never opens a stream.
func TestTheStreamLimitOnTheRelay(t *testing.T) {
	full := billing.Decision{Mode: billing.ModeFull}
	streams := &fakeStreams{}
	e := newE2EWith(t, &full, &Plans{Enforce: true, StreamLimit: true, Streams: streams})
	old := billing.Tiers
	t.Cleanup(func() { billing.Tiers = old })
	set := map[string]billing.Tier{}
	for k, v := range old {
		set[k] = v
	}
	pro := set[billing.PlanPro] // the e2e home is on pro
	two := 2
	pro.MaxRelayStreams = &two
	set[billing.PlanPro] = pro
	billing.Tiers = set

	get := func(path string) int {
		resp := e.request(context.Background(), t, path, map[string]string{"Accept": "application/json"})
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for _, s := range []string{"a", "b"} {
		if code := get("/stream/" + s + "/seg-1.ts"); code != http.StatusOK {
			t.Fatalf("stream %s: %d", s, code)
		}
	}
	if code := get("/stream/c/seg-1.ts"); code != http.StatusTooManyRequests {
		t.Fatalf("a third stream: %d", code)
	}
	if code := get("/stream/a/seg-2.ts"); code != http.StatusOK {
		t.Fatalf("a playing stream stopped: %d", code)
	}
	if code := get("/stream/c/manifest.m3u8"); code != http.StatusOK {
		t.Fatalf("a playlist was limited: %d", code)
	}
}
