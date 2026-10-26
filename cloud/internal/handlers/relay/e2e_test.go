package relay

// End to end through the real pieces: a relay edge (ServeTunnels + this
// proxy) on httptest, the real cloudlink Supervisor dialing it over a
// websocket, and a fake home web port. No database: the servers table is
// behind the TunnelStore and ServerLookup seams.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

const (
	e2eSlug   = "home"
	e2eHost   = "home.relay.test"
	e2eID     = "11111111-1111-1111-1111-111111111111"
	e2eSecret = "the-secret"
)

type fakeTunnelStore struct {
	mu     sync.Mutex
	online []bool
}

func (f *fakeTunnelStore) Authenticate(_ context.Context, id, secret string) (stores.Server, error) {
	if id != e2eID || secret != e2eSecret {
		return stores.Server{}, errors.New("unauthorized")
	}
	return stores.Server{ID: e2eID, Slug: e2eSlug, Plan: "pro"}, nil
}

func (f *fakeTunnelStore) SetOnline(_ context.Context, _ string, online bool) error {
	f.mu.Lock()
	f.online = append(f.online, online)
	f.mu.Unlock()
	return nil
}

func (f *fakeTunnelStore) states() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.online...)
}

type fakeLookup struct{}

func (fakeLookup) BySlug(_ context.Context, slug string) (stores.Server, error) {
	if slug != e2eSlug {
		return stores.Server{}, stores.ErrServerNotFound
	}
	return stores.Server{ID: e2eID, Slug: e2eSlug, Plan: "pro"}, nil
}

type fakeMeter struct {
	mu    sync.Mutex
	calls int
	out   int64
}

func (m *fakeMeter) Allow(context.Context, stores.Server) error { return nil }
func (m *fakeMeter) Record(_ context.Context, _ stores.Server, _, out int64) {
	m.mu.Lock()
	m.calls++
	m.out += out
	m.mu.Unlock()
}

func (m *fakeMeter) snapshot() (int, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, m.out
}

// bigBody is 50 MiB of deterministic, non-repeating-per-chunk bytes.
func bigBody() []byte {
	b := make([]byte, 50<<20)
	var x uint32 = 2463534242
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

type e2e struct {
	edge     *httptest.Server
	home     *httptest.Server
	reg      *relaypkg.Registry
	store    *fakeTunnelStore
	meter    *fakeMeter
	big      []byte
	canceled chan struct{} // closed when /slow's request context ends
	authSeen chan string
	stop     func()
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	e := &e2e{
		reg:      relaypkg.NewRegistry(),
		store:    &fakeTunnelStore{},
		meter:    &fakeMeter{},
		big:      bigBody(),
		canceled: make(chan struct{}),
		authSeen: make(chan string, 4),
	}
	var cancelOnce sync.Once
	homeMux := http.NewServeMux()
	homeMux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "big.mp4", time.Unix(0, 0), bytes.NewReader(e.big))
	})
	homeMux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		// as fast as the tunnel takes it: a paused client must fill every
		// buffer on the way, and only its own stream may stall
		chunk := bytes.Repeat([]byte("s"), 64*1024)
		for {
			select {
			case <-r.Context().Done():
				cancelOnce.Do(func() { close(e.canceled) })
				return
			default:
			}
			if _, err := w.Write(chunk); err != nil {
				cancelOnce.Do(func() { close(e.canceled) })
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	homeMux.HandleFunc("/long", func(w http.ResponseWriter, _ *http.Request) {
		// streams for well past the edge's head timeout
		for i := 0; i < 8; i++ {
			fmt.Fprintf(w, "part %d\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	homeMux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		e.authSeen <- r.Header.Get("Authorization")
		w.Header().Set("X-Seen-Via", r.Header.Get("X-Maktaba-Via"))
		w.Header().Set("X-Seen-For", r.Header.Get("X-Forwarded-For"))
		_, _ = io.WriteString(w, "ok")
	})
	e.home = httptest.NewServer(homeMux)

	proxy := (&Deps{
		Registry:    e.reg,
		Lookup:      fakeLookup{},
		Meter:       e.meter,
		PublicHost:  "relay.test",
		HeadTimeout: 300 * time.Millisecond,
	}).Handler()
	edgeMux := http.NewServeMux()
	edgeMux.Handle("/v1/relay/ws", relaypkg.ServeTunnels(e.reg, e.store, nil))
	edgeMux.Handle("/", proxy)
	// behind the middleware the relay role runs (server.NewRouter): its
	// response recorder must let the tunnel upgrade (Hijack) and the proxy
	// flush, as it once did not
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var edge http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/relay/ws" && r.Host != e2eHost {
			edgeMux.ServeHTTP(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	for _, mw := range []func(http.Handler) http.Handler{
		middleware.CORS(nil), middleware.Logging(logger), middleware.RequestID, middleware.Recover(logger),
	} {
		edge = mw(edge)
	}
	e.edge = httptest.NewServer(edge)

	sup := &cloudlink.Supervisor{
		Dialer: &cloudlink.Dialer{
			Endpoint: "ws" + strings.TrimPrefix(e.edge.URL, "http") + "/v1/relay/ws",
			ServerID: e2eID,
			Secret:   e2eSecret,
		},
		Proxy:    &cloudlink.LocalProxy{BaseURL: e.home.URL},
		BaseWait: 10 * time.Millisecond,
		MaxWait:  50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = sup.Run(ctx); close(done) }()
	e.stop = func() {
		cancel()
		<-done
		e.edge.Close()
		e.home.Close()
	}
	t.Cleanup(e.stop)
	e.waitTunnel(t, nil)
	return e
}

// waitTunnel waits for a live tunnel other than old.
func (e *e2e) waitTunnel(t *testing.T, old *relaypkg.Tunnel) *relaypkg.Tunnel {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tun, err := e.reg.Lookup(e2eSlug); err == nil && tun != old {
			return tun
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the home never connected to the relay")
	return nil
}

func (e *e2e) request(ctx context.Context, t *testing.T, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.edge.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = e2eHost
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func TestE2E_RelayThroughRealTunnel(t *testing.T) {
	e := newE2E(t)
	tun, _ := e.reg.Lookup(e2eSlug)
	if !tun.FlowControl() {
		t.Fatal("a current agent and cloud did not agree on flow control")
	}

	t.Run("a 50 MiB body arrives intact and is metered while it streams", func(t *testing.T) {
		calls0, out0 := e.meter.snapshot()
		resp := e.request(context.Background(), t, "/big", nil)
		defer resp.Body.Close()
		h := sha256.New()
		n, err := io.Copy(h, resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, read %d bytes: %v", resp.StatusCode, n, err)
		}
		if want := sha256.Sum256(e.big); !bytes.Equal(h.Sum(nil), want[:]) {
			t.Fatalf("the body changed on the way (%d bytes)", n)
		}
		// the handler records its last chunk after the client has the body
		deadline := time.Now().Add(2 * time.Second)
		for {
			calls, out := e.meter.snapshot()
			if out-out0 == int64(len(e.big)) {
				if calls-calls0 < 40 {
					t.Fatalf("metered in %d records, want one per MiB while streaming", calls-calls0)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("metered %d bytes, want %d", out-out0, len(e.big))
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	t.Run("a Range request is answered 206 with the range", func(t *testing.T) {
		resp := e.request(context.Background(), t, "/big", map[string]string{"Range": "bytes=1000-1999"})
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body, e.big[1000:2000]) ||
			resp.Header.Get("Content-Range") != fmt.Sprintf("bytes 1000-1999/%d", len(e.big)) {
			t.Fatalf("status %d, %d bytes, Content-Range %q", resp.StatusCode, len(body), resp.Header.Get("Content-Range"))
		}
	})

	t.Run("the Bearer token reaches the home unchanged, marked relayed by the agent", func(t *testing.T) {
		resp := e.request(context.Background(), t, "/whoami", map[string]string{
			"Authorization": "Bearer abc.def.ghi", "X-Forwarded-For": "192.168.1.5",
			"X-Maktaba-Via": "spoofed",
		})
		defer resp.Body.Close()
		if got := <-e.authSeen; got != "Bearer abc.def.ghi" {
			t.Fatalf("Authorization at the home = %q", got)
		}
		if v := resp.Header.Get("X-Seen-Via"); v != "relay" {
			t.Fatalf("X-Maktaba-Via at the home = %q, want relay", v)
		}
		// the edge's peer is loopback (a local proxy), so the rightmost
		// entry is taken; httptest's client sent the only one, and a
		// client's claim is never passed on alongside it
		if v := resp.Header.Get("X-Seen-For"); strings.Contains(v, ",") {
			t.Fatalf("X-Forwarded-For at the home = %q, want one address", v)
		}
	})

	t.Run("a body longer than the head timeout keeps streaming", func(t *testing.T) {
		resp := e.request(context.Background(), t, "/long", nil)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || !strings.Contains(string(body), "part 7") {
			t.Fatalf("the long body was cut: %q (%v)", body, err)
		}
	})

	t.Run("a stalled client does not stall another stream, and its leaving stops the home", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		resp := e.request(ctx, t, "/slow", nil)
		// read a little, then stop reading
		if _, err := io.ReadFull(resp.Body, make([]byte, 1024)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(500 * time.Millisecond) // the home fills its window and the sockets

		start := time.Now()
		r2, err := http.NewRequest(http.MethodGet, e.edge.URL+"/whoami", nil)
		if err != nil {
			t.Fatal(err)
		}
		r2.Host = e2eHost
		c2 := &http.Client{Timeout: 3 * time.Second}
		resp2, err := c2.Do(r2)
		if err != nil {
			t.Fatalf("a second stream stalled behind the paused one: %v", err)
		}
		_, _ = io.ReadAll(resp2.Body)
		resp2.Body.Close()
		<-e.authSeen
		if d := time.Since(start); d > time.Second {
			t.Fatalf("the second stream took %v", d)
		}

		cancel()
		resp.Body.Close()
		select {
		case <-e.canceled:
		case <-time.After(3 * time.Second):
			t.Fatal("the client left but the home kept serving the stream")
		}
	})

	t.Run("the home reconnects after the cloud drops the socket", func(t *testing.T) {
		old, _ := e.reg.Lookup(e2eSlug)
		old.Close()
		e.waitTunnel(t, old)
		resp := e.request(context.Background(), t, "/whoami", nil)
		defer resp.Body.Close()
		<-e.authSeen
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("after the reconnect: %d", resp.StatusCode)
		}
		// up, down, up again (a replaced tunnel would not have gone down)
		st := e.store.states()
		if len(st) < 3 || !st[0] || st[1] || !st[2] {
			t.Fatalf("tunnel states recorded: %v", st)
		}
	})
}
