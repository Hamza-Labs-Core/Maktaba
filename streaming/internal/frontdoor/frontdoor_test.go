package frontdoor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/hdhr"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
)

type settings struct{ s placement.Settings }

func (s settings) Cluster(context.Context) placement.Settings { return s.s }

// fakeNode is a node's streaming port: it checks the signature, serves a playlist,
// a segment and an endless TS (each write flushed).
type fakeNode struct {
	srv      *httptest.Server
	key      *cluster.Keyring
	ts       chan []byte
	requests atomic.Int32
	closeTS  chan struct{}
	mu       sync.Mutex
	gens     []int64
}

func newFakeNode(t *testing.T, key *cluster.Keyring) *fakeNode {
	n := &fakeNode{key: key, ts: make(chan []byte, 16), closeTS: make(chan struct{})}
	r := chi.NewRouter()
	r.Use(cluster.VerifyMiddleware(key, nil))
	r.Get("/stream/channel/{id}/index.m3u8", func(w http.ResponseWriter, r *http.Request) {
		n.record(r)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:4.0,\nseg-100.ts\n")
	})
	r.Get("/stream/channel/{id}/{seg}", func(w http.ResponseWriter, r *http.Request) {
		n.record(r)
		if chi.URLParam(r, "seg") == "live.ts" {
			w.Header().Set("Content-Type", "video/mp2t")
			w.WriteHeader(200)
			fl := w.(http.Flusher)
			fl.Flush()
			for {
				select {
				case b := <-n.ts:
					if _, err := w.Write(b); err != nil {
						return
					}
					fl.Flush()
				case <-n.closeTS:
					return
				case <-r.Context().Done():
					return
				}
			}
		}
		w.Header().Set("Content-Type", "video/mp2t")
		_, _ = w.Write(bytes.Repeat([]byte{0x47}, 188))
	})
	n.srv = httptest.NewServer(r)
	t.Cleanup(n.srv.Close)
	return n
}

func (n *fakeNode) record(r *http.Request) {
	n.requests.Add(1)
	n.mu.Lock()
	n.gens = append(n.gens, cluster.PlacementGen(r))
	n.mu.Unlock()
}

type rig struct {
	fd    *Handler
	store *placement.MemStore
	hosts *placement.Hosts
	local atomic.Int32
	key   *cluster.Keyring
	r     chi.Router
	srv   *httptest.Server
}

func newRig(t *testing.T, s placement.Settings, nodeBase string, homeCap, nodeCap int) *rig {
	g := &rig{store: placement.NewMemStore(), key: cluster.NewKeyring([]byte("cluster-key"))}
	g.store.SetHosts([]placement.Host{
		{ID: "home", Name: "mac", Role: placement.RoleHome, Enabled: true, Capacity: homeCap, Weight: 1, CanLive: true},
		{ID: "pc", Name: "pc", Role: placement.RoleNode, Enabled: true, Capacity: nodeCap, Weight: 1, CanLive: true, BaseURL: nodeBase},
	})
	s.NodesStream = true
	g.hosts = &placement.Hosts{Source: g.store, Settings: settings{s}}
	g.hosts.Refresh(context.Background())
	svc := &placement.Service{Store: g.store, Hosts: g.hosts, Settings: settings{s}, Self: "home"}
	// the home's own channel routes, as a router (like channel.HTTPHandler's)
	local := chi.NewRouter()
	local.Get("/stream/channel/{id}/index.m3u8", func(w http.ResponseWriter, r *http.Request) {
		if _, err := uuid.Parse(chi.URLParam(r, "id")); err != nil {
			http.Error(w, "bad id", 400)
			return
		}
		g.local.Add(1)
		_, _ = io.WriteString(w, "#EXTM3U\nlocal\n")
	})
	g.fd = &Handler{Svc: svc, Settings: settings{s}, Self: "home", Local: local, Keyring: g.key, Tracker: NewTracker()}
	g.r = chi.NewRouter()
	g.fd.Mount(g.r)
	g.srv = httptest.NewServer(g.r)
	t.Cleanup(g.srv.Close)
	return g
}

func get(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	if c == nil {
		c = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

// TC6 of 32.3: a home placement takes today's in-process path (no proxy hop).
func TestLocalPathUnchanged(t *testing.T) {
	node := newFakeNode(t, cluster.NewKeyring([]byte("cluster-key")))
	g := newRig(t, placement.DefaultSettings(), node.srv.URL, 4, 4)
	ch := uuid.New()
	resp, body := get(t, nil, g.srv.URL+"/stream/channel/"+ch.String()+"/index.m3u8")
	if resp.StatusCode != 200 || !strings.Contains(body, "local") || g.local.Load() != 1 || node.requests.Load() != 0 {
		t.Errorf("local: %d %q local=%d node=%d", resp.StatusCode, body, g.local.Load(), node.requests.Load())
	}
}

// TC2 of 32.3: the playlist is proxied in both modes; segments proxied (200) or
// redirected (307 to a signed node URL the node accepts).
func TestHLSProxyAndRedirectModes(t *testing.T) {
	key := cluster.NewKeyring([]byte("cluster-key"))
	node := newFakeNode(t, key)
	for _, mode := range []string{placement.HLSProxy, placement.HLSRedirect} {
		s := placement.DefaultSettings()
		s.HLSMode = mode
		g := newRig(t, s, node.srv.URL, 0, 4) // the home is full: the node takes it
		ch := uuid.New()
		resp, body := get(t, nil, g.srv.URL+"/stream/channel/"+ch.String()+"/index.m3u8")
		if resp.StatusCode != 200 || !strings.Contains(body, "seg-100.ts") {
			t.Fatalf("%s playlist: %d %q", mode, resp.StatusCode, body)
		}
		resp, body = get(t, nil, g.srv.URL+"/stream/channel/"+ch.String()+"/seg-100.ts")
		switch mode {
		case placement.HLSProxy:
			if resp.StatusCode != 200 || len(body) != 188 {
				t.Errorf("proxied segment: %d len %d", resp.StatusCode, len(body))
			}
		case placement.HLSRedirect:
			loc := resp.Header.Get("Location")
			if resp.StatusCode != http.StatusTemporaryRedirect || !strings.HasPrefix(loc, node.srv.URL) || !strings.Contains(loc, "fx=") {
				t.Fatalf("redirect: %d %q", resp.StatusCode, loc)
			}
			r2, b2 := get(t, http.DefaultClient, loc)
			if r2.StatusCode != 200 || len(b2) != 188 {
				t.Errorf("the node refused the redirected segment: %d", r2.StatusCode)
			}
		}
	}
	// every proxied request carried the placement generation
	node.mu.Lock()
	defer node.mu.Unlock()
	for _, g := range node.gens {
		if g <= 0 {
			t.Errorf("request without a placement generation: %v", node.gens)
		}
	}
}

type oneChannelRepo struct {
	id     uuid.UUID
	tuners int
}

func (r oneChannelRepo) Device(context.Context) (hdhr.Device, error) {
	return hdhr.Device{DeviceID: "ABCD1234", TunerCount: r.tuners, Enabled: true}, nil
}
func (r oneChannelRepo) Lineup(context.Context) ([]hdhr.LineupChannel, error) {
	return []hdhr.LineupChannel{{ID: r.id, Number: 105, Name: "Test"}}, nil
}

// TC1 + TC5 of 32.3: a Plex-like client reads the node's TS from the home's /auto,
// unbuffered (first bytes within 100 ms of the node writing them).
func TestAutoProxiedToNode(t *testing.T) {
	key := cluster.NewKeyring([]byte("cluster-key"))
	node := newFakeNode(t, key)
	g := newRig(t, placement.DefaultSettings(), node.srv.URL, 0, 4)
	ch := uuid.New()
	h := hdhr.New(oneChannelRepo{id: ch, tuners: 2}, g.fd)
	r := chi.NewRouter()
	h.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/auto/v105")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("auto: %d", resp.StatusCode)
	}
	pkt := bytes.Repeat([]byte{0x47}, 188)
	buf := make([]byte, 188)
	for i := 0; i < 3; i++ {
		start := time.Now()
		node.ts <- pkt
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > 100*time.Millisecond {
			t.Errorf("packet %d took %v through the front door (buffered?)", i, d)
		}
	}
	if v := g.fd.Tracker.Snapshot()[ch.String()]; v.Tuners != 1 {
		t.Errorf("tuners counted %+v", v)
	}
}

// TC4 of 32.3: tuner leases are counted on the home: with 2 tuners the third /auto
// gets "all tuners in use" although the node has room.
func TestLeaseCountedOnHome(t *testing.T) {
	key := cluster.NewKeyring([]byte("cluster-key"))
	node := newFakeNode(t, key)
	g := newRig(t, placement.DefaultSettings(), node.srv.URL, 0, 16)
	ch := uuid.New()
	h := hdhr.New(oneChannelRepo{id: ch, tuners: 2}, g.fd)
	r := chi.NewRouter()
	h.Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	var open []*http.Response
	for i := 0; i < 2; i++ {
		resp, err := http.Get(srv.URL + "/auto/v105")
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("tuner %d: %v %v", i, err, resp)
		}
		open = append(open, resp)
	}
	resp, err := http.Get(srv.URL + "/auto/v105")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "all tuners in use") {
		t.Errorf("third tuner: %d %q", resp.StatusCode, b)
	}
	for _, o := range open {
		o.Body.Close()
	}
}

// TC4 of 32.4: upstream A closes mid-stream → the client keeps reading from upstream B
// on the same connection.
func TestSpliceTSOnFailover(t *testing.T) {
	key := cluster.NewKeyring([]byte("cluster-key"))
	a := newFakeNode(t, key)
	b := newFakeNode(t, key)
	g := newRig(t, placement.DefaultSettings(), a.srv.URL, 0, 4)
	// a second node, B
	hs, _ := g.store.Hosts(context.Background())
	hs = append(hs, placement.Host{ID: "pc2", Name: "pc2", Role: placement.RoleNode, Enabled: true, Capacity: 4, Weight: 1, BaseURL: b.srv.URL})
	g.store.SetHosts(hs)
	g.hosts.Refresh(context.Background())
	ch := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- g.fd.StreamChannelTS(ctx, ch, pw) }()
	pkt := bytes.Repeat([]byte{0x47}, 188)
	buf := make([]byte, 188)
	p, _, _ := g.store.Get(context.Background(), ch)
	for p.Host == "" {
		time.Sleep(10 * time.Millisecond)
		p, _, _ = g.store.Get(context.Background(), ch)
	}
	first, second := a, b
	if p.Host == "pc2" {
		first, second = b, a
	}
	first.ts <- pkt
	if _, err := io.ReadFull(pr, buf); err != nil {
		t.Fatal(err)
	}
	// the first node dies: its stream ends and it refuses connections
	close(first.closeTS)
	first.srv.Close()
	// the front door's failures mark it down; the splice resolves onto the second
	go func() {
		for i := 0; i < 200; i++ {
			select {
			case second.ts <- pkt:
			case <-ctx.Done():
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	got := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(pr, buf)
		got <- err
	}()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the client got nothing after the first node died")
	}
	np, _, _ := g.store.Get(context.Background(), ch)
	if np.Host == p.Host || np.Reason != placement.ReasonFailover {
		t.Errorf("placement after the splice: %+v", np)
	}
	cancel()
	_ = pr.Close()
	<-done
}
