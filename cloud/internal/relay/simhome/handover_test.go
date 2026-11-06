package simhome

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

var tokenSecret = []byte(strings.Repeat("t", 32))

// proc is one relay process: a registry and its tunnel endpoint.
type proc struct {
	reg *relay.Registry
	ts  *relay.TunnelServer
}

func newProc(store *Store) *proc {
	reg := relay.NewRegistry()
	return &proc{reg: reg, ts: &relay.TunnelServer{
		Registry: reg, Store: store, Logger: slog.New(slog.DiscardHandler),
		Tickets: relay.NewTickets(tokenSecret), Drain: true,
	}}
}

// front is the edge (Caddy in production): tunnels go to the current process,
// which a deploy switches.
type front struct {
	mu  sync.Mutex
	cur *proc
	srv *httptest.Server
}

func newFront(p *proc) *front {
	f := &front{cur: p}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		p := f.cur
		f.mu.Unlock()
		p.ts.ServeHTTP(w, r)
	}))
	return f
}

func (f *front) switchTo(p *proc) {
	f.mu.Lock()
	f.cur = p
	f.mu.Unlock()
}

func (f *front) endpoint() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/v1/relay/ws"
}

func waitUntil(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// get proxies GET path to slug's tunnel in p and returns the body.
func get(ctx context.Context, tn *relay.Tunnel, path string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://x"+path, nil)
	req.Host = tn.Slug + ".relay.test"
	return tn.ProxyWithin(ctx, req, 10*time.Second)
}

func checkPattern(b []byte) error {
	for i, c := range b {
		if c != Pattern(i) {
			return fmt.Errorf("byte %d is %q, want %q", i, c, Pattern(i))
		}
	}
	return nil
}

// T04: two slow streams finish on the old tunnel, every byte delivered; the
// home opens a new tunnel within the window (with its ticket: no bearer
// check); a third request takes the new one; the old one closes once its
// streams end.
func TestDrainMovesTheHomeAndFinishesItsStreams(t *testing.T) {
	api := API()
	defer api.Close()
	store := NewStore()
	p := newProc(store)
	f := newFront(p)
	defer f.srv.Close()
	h := Start(1, f.endpoint(), api.URL, Options{})
	defer h.Stop()
	waitUntil(t, 5*time.Second, "the home online", func() bool { _, err := p.reg.Lookup(h.Slug); return err == nil })
	old, _ := p.reg.Lookup(h.Slug)
	bearers := store.Bearers()
	before := relay.Counters()

	const size = 400_000
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := get(context.Background(), old, fmt.Sprintf("/stream?bytes=%d&ms=1500", size))
			if err != nil {
				errs <- err
				return
			}
			b, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err == nil && len(b) != size {
				err = fmt.Errorf("got %d bytes, want %d", len(b), size)
			}
			if err == nil {
				err = checkPattern(b)
			}
			errs <- err
		}()
	}
	waitUntil(t, 2*time.Second, "two streams on the old tunnel", func() bool { return old.Streams() == 2 })

	start := time.Now()
	old.Drain(relay.DrainOptions{Reason: relay.DrainDeploy, Within: 300 * time.Millisecond, Deadline: 20 * time.Second}, relay.NewTickets(tokenSecret).Mint(h.ID))
	waitUntil(t, 2*time.Second, "a new tunnel", func() bool { cur, err := p.reg.Lookup(h.Slug); return err == nil && cur != old })
	if took := time.Since(start); took > time.Second {
		t.Errorf("the new tunnel took %s, want within the 300 ms window (+ dial)", took)
	}
	if store.Bearers() != bearers {
		t.Errorf("the move did %d bearer checks, want 0 (the ticket)", store.Bearers()-bearers)
	}
	if c := relay.Counters(); c.HandsTicket <= before.HandsTicket || c.Drains[relay.DrainDeploy] <= before.Drains[relay.DrainDeploy] {
		t.Errorf("counters: %+v (before %+v)", c, before)
	}
	// the old tunnel is still serving its two streams
	select {
	case <-old.Done():
		t.Fatal("the old tunnel closed with its streams open")
	default:
	}
	cur, _ := p.reg.Lookup(h.Slug)
	resp, err := get(context.Background(), cur, "/api/health")
	if err != nil {
		t.Fatalf("third request on the new tunnel: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.HasPrefix(b, []byte("ok ")) {
		t.Errorf("third request: %q", b)
	}
	if cur.Streams() != 0 || old.Streams() != 2 {
		t.Errorf("streams: new %d, old %d", cur.Streams(), old.Streams())
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a stream on the drained tunnel: %v", err)
		}
	}
	select {
	case <-old.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the old tunnel did not close after its streams ended")
	}
	if relay.Counters().DrainForced != before.DrainForced {
		t.Error("a drain that finished in time counted as forced")
	}
	// the home is still online on the new tunnel
	if _, err := p.reg.Lookup(h.Slug); err != nil || !h.Online() {
		t.Errorf("the home after the move: %v online=%v", err, h.Online())
	}
}

// T05: a stream still open at the deadline: the relay closes the tunnel
// (forced), and the home is back on a new tunnel.
func TestDrainDeadlineForcesTheClose(t *testing.T) {
	api := API()
	defer api.Close()
	p := newProc(NewStore())
	f := newFront(p)
	defer f.srv.Close()
	h := Start(2, f.endpoint(), api.URL, Options{})
	defer h.Stop()
	waitUntil(t, 5*time.Second, "online", func() bool { _, err := p.reg.Lookup(h.Slug); return err == nil })
	old, _ := p.reg.Lookup(h.Slug)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := get(ctx, old, "/hang")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	readErr := make(chan error, 1)
	go func() { _, err := io.ReadAll(resp.Body); readErr <- err }()
	forced := relay.Counters().DrainForced
	start := time.Now()
	old.Drain(relay.DrainOptions{Reason: relay.DrainDeploy, Within: 100 * time.Millisecond, Deadline: 700 * time.Millisecond}, "")
	select {
	case <-old.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("not closed at the deadline")
	}
	if took := time.Since(start); took < 600*time.Millisecond {
		t.Errorf("closed after %s, before the 700 ms deadline", took)
	}
	if err := <-readErr; err == nil {
		t.Error("the hanging stream's client got a clean end, want a broken connection")
	}
	if got := relay.Counters().DrainForced - forced; got != 1 {
		t.Errorf("tunnel_drain_forced_total += %d, want 1", got)
	}
	waitUntil(t, 3*time.Second, "the home on a new tunnel", func() bool { cur, err := p.reg.Lookup(h.Slug); return err == nil && cur != old })
}

// T06: an older home (no drain feature) gets no DRAIN: the relay closes its
// idle tunnel at once and the home reconnects with its bearer.
func TestDrainOfAnOlderHomeClosesAndItReconnectsByBearer(t *testing.T) {
	api := API()
	defer api.Close()
	store := NewStore()
	p := newProc(store)
	f := newFront(p)
	defer f.srv.Close()
	h := Start(3, f.endpoint(), api.URL, Options{Legacy: true})
	defer h.Stop()
	waitUntil(t, 5*time.Second, "online", func() bool { _, err := p.reg.Lookup(h.Slug); return err == nil })
	old, _ := p.reg.Lookup(h.Slug)
	if h.Sup.Dialer.Ticket() != "" {
		t.Fatal("an older home was given a ticket")
	}
	bearers := store.Bearers()
	old.Drain(relay.DrainOptions{Reason: relay.DrainDeploy, Within: time.Second, Deadline: 10 * time.Second}, "")
	select {
	case <-old.Done():
	case <-time.After(time.Second):
		t.Fatal("an idle older home's tunnel was not closed at once")
	}
	waitUntil(t, 5*time.Second, "the older home back", func() bool { cur, err := p.reg.Lookup(h.Slug); return err == nil && cur != old })
	if store.Bearers() <= bearers {
		t.Error("the older home did not reconnect by bearer")
	}
}

// T07: a revoked server's valid ticket is refused (ticket_invalid), and its
// bearer too.
func TestRevokedServerTicketRefused(t *testing.T) {
	api := API()
	defer api.Close()
	store := NewStore()
	p := newProc(store)
	f := newFront(p)
	defer f.srv.Close()
	h := Start(4, f.endpoint(), api.URL, Options{BaseWait: 20 * time.Millisecond, MaxWait: 50 * time.Millisecond})
	defer h.Stop()
	waitUntil(t, 5*time.Second, "online", func() bool { _, err := p.reg.Lookup(h.Slug); return err == nil })
	if h.Sup.Dialer.Ticket() == "" {
		t.Fatal("no ticket at AUTH_OK")
	}
	refused := relay.Counters().TicketRefused
	store.Revoke(h.ID)
	old, _ := p.reg.Lookup(h.Slug)
	old.Close()
	waitUntil(t, 3*time.Second, "the ticket refused", func() bool { return relay.Counters().TicketRefused > refused })
	waitUntil(t, 3*time.Second, "the ticket dropped", func() bool { return h.Sup.Dialer.Ticket() == "" })
	time.Sleep(200 * time.Millisecond)
	if cur, err := p.reg.Lookup(h.Slug); err == nil && cur != old {
		t.Fatal("a revoked server got a tunnel back")
	}
	if st := h.Sup.State(); st.Status == "online" {
		t.Errorf("revoked home state %+v", st)
	}
}

// T08: 1,000 homes on one relay process; it gets SIGTERM (Shutdown) while a
// new process takes the edge: every home is on the new process in time, no
// client request fails, and the old process is left with no tunnel.
func TestShutdownMovesAThousandHomes(t *testing.T) {
	n := 1000
	if testing.Short() {
		n = 100
	}
	api := API()
	defer api.Close()
	store := NewStore()
	a, b := newProc(store), newProc(store)
	f := newFront(a)
	defer f.srv.Close()
	homes := make([]*Home, n)
	for i := range homes {
		homes[i] = Start(1000+i, f.endpoint(), api.URL, Options{PingEvery: 30 * time.Second})
	}
	defer func() {
		for _, h := range homes {
			h.Stop()
		}
	}()
	waitUntil(t, 60*time.Second, "every home on process A", func() bool { return a.reg.Live() == n })

	// clients keep requesting random homes during the move; a request goes to
	// the process that has the home (the tunnel directory's job, 50.04)
	stop := make(chan struct{})
	var ok, failed atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				h := homes[rand.IntN(n)]
				tn, err := b.reg.Lookup(h.Slug)
				if err != nil {
					tn, err = a.reg.Lookup(h.Slug)
				}
				if err != nil {
					failed.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Errorf("%s: no tunnel", h.Slug))
					continue
				}
				resp, err := get(context.Background(), tn, "/stream?bytes=20000&ms=200")
				if err == nil {
					var body []byte
					body, err = io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if err == nil && len(body) != 20000 {
						err = fmt.Errorf("short body %d", len(body))
					}
				}
				if err != nil {
					failed.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Errorf("%s: %w", h.Slug, err))
					continue
				}
				ok.Add(1)
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)

	// the deploy: the edge sends new tunnels to B, then A gets SIGTERM
	f.switchTo(b)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	a.reg.Shutdown(ctx, relay.DrainOptions{Reason: relay.DrainShutdown, Within: time.Second, Deadline: 30 * time.Second}, 1000, a.ts.Tickets.Mint)
	took := time.Since(start)
	waitUntil(t, 15*time.Second, "every home on process B", func() bool { return b.reg.Live() == n })
	moved := time.Since(start)
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	t.Logf("%d homes moved in %s (A empty after %s); %d requests ok, %d failed", n, moved, took, ok.Load(), failed.Load())
	if moved > 15*time.Second {
		t.Errorf("the move took %s, want ≤ 15 s", moved)
	}
	if a.reg.Live() != 0 {
		t.Errorf("process A still holds %d tunnels", a.reg.Live())
	}
	if failed.Load() > 0 {
		t.Errorf("%d client requests failed during the move; first: %v", failed.Load(), firstErr.Load())
	}
	if ok.Load() == 0 {
		t.Error("no request ran during the move")
	}
	// a refused tunnel: A takes none while closing
	direct := httptest.NewServer(a.ts)
	defer direct.Close()
	resp, err := http.Get(direct.URL + "/v1/relay/ws")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("a closing process answered a tunnel %d, want 503", resp.StatusCode)
	}
}
