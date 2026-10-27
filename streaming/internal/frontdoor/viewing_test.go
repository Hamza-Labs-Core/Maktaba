package frontdoor

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/viewing"
)

// appViewers waits for ch's app viewers to be want and returns them: a segment's
// viewing is recorded as its handler returns (segmentServed is deferred), which a
// client that has read the whole segment may see a moment later.
func appViewers(vt *viewing.Tracker, ch uuid.UUID, want int) int {
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := vt.AppViewers()[ch]
		if n == want || time.Now().After(deadline) {
			return n
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Epic 44 story 44.4: a segment served (proxied, or redirected to its node) opens
// and moves its viewer's viewing; a playlist (the web player prefetches its
// neighbours') and a failed segment do not. The channel's viewers are its TS
// clients and its app viewers.
func TestSegmentsMakeViewings(t *testing.T) {
	key := cluster.NewKeyring([]byte("cluster-key"))
	node := newFakeNode(t, key)
	for _, mode := range []string{placement.HLSProxy, placement.HLSRedirect} {
		s := placement.DefaultSettings()
		s.HLSMode = mode
		g := newRig(t, s, node.srv.URL, 0, 4) // the node takes the channel
		vt := viewing.NewTracker(nil)
		g.fd.Viewings = vt
		ch, u := uuid.New(), uuid.New()
		fetch := func(path string) int {
			t.Helper()
			req, _ := http.NewRequest(http.MethodGet, g.srv.URL+"/stream/channel/"+ch.String()+"/"+path, nil)
			req.Header.Set(viewing.ViewerHeader, u.String()) // the gate's, on loopback
			req.Header.Set(viewing.ClientHeader, "web/0.3.25 (macos; desktop)")
			c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			return resp.StatusCode
		}
		if code := fetch("index.m3u8"); code != 200 {
			t.Fatalf("%s playlist: %d", mode, code)
		}
		if n := vt.AppViewers()[ch]; n != 0 {
			t.Fatalf("%s: a playlist made %d viewers", mode, n)
		}
		for i := 0; i < 3; i++ {
			if code := fetch("seg-100.ts"); code != 200 && code != http.StatusTemporaryRedirect {
				t.Fatalf("%s segment: %d", mode, code)
			}
		}
		if n := appViewers(vt, ch, 1); n != 1 {
			t.Fatalf("%s: viewers %d, want 1 (one person, one app)", mode, n)
		}
		g.fd.Tracker.AttachTS(ch) // a tuner on the same channel
		if n := g.fd.ChannelViewers()[ch]; n != 2 {
			t.Fatalf("%s: channel viewers %d, want 2", mode, n)
		}
	}

	// the node gone: the segment fails (502) and counts for no one
	g := newRig(t, placement.DefaultSettings(), "http://127.0.0.1:1", 0, 4)
	vt := viewing.NewTracker(nil)
	g.fd.Viewings = vt
	ch := uuid.New()
	resp, _ := get(t, nil, g.srv.URL+"/stream/channel/"+ch.String()+"/seg-100.ts")
	if resp.StatusCode < 500 {
		t.Fatalf("segment from a dead node: %d", resp.StatusCode)
	}
	if n := vt.AppViewers()[ch]; n != 0 {
		t.Fatalf("a failed segment made %d viewers", n)
	}
}

// With TV slots the stitched playlist's s/{slot}/seg-N.ts are the segments: each
// one served is a viewer's (here with no person: counted, never written).
func TestSlotSegmentsMakeViewings(t *testing.T) {
	g := newSlotRig(t)
	vt := viewing.NewTracker(nil)
	g.fd.Viewings = vt
	code, body := g.get(t, "/stream/channel/"+g.ch.String()+"/index.m3u8")
	if code != 200 {
		t.Fatalf("playlist: %d", code)
	}
	if n := vt.AppViewers()[g.ch]; n != 0 {
		t.Fatalf("a playlist made %d viewers", n)
	}
	uri := ""
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "s/blue/") {
			uri = l
		}
	}
	if code, _ := g.get(t, "/stream/channel/"+g.ch.String()+"/"+uri); code != 200 {
		t.Fatalf("segment %s: %d", uri, code)
	}
	if n := appViewers(vt, g.ch, 1); n != 1 {
		t.Fatalf("viewers %d, want 1", n)
	}
	if code, _ := g.get(t, "/stream/channel/"+g.ch.String()+"/s/blue/seg-1.ts.x"); code == 200 {
		t.Fatal("a bad path served")
	}
	if n := vt.AppViewers()[g.ch]; n != 1 {
		t.Fatalf("viewers %d after a bad path, want 1", n)
	}
}
