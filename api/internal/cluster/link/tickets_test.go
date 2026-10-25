package link

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// ticketServer is a Server with one fake live session for testNode (no network).
func ticketServer(t *testing.T) (*Server, *ctrl, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)
	s := &Server{}
	s.Now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sess := &ctrl{id: "s1", node: testNode, serial: "aa", srv: s, out: newSender(), ctx: ctx, cancel: cancel}
	s.sessions = map[string]*ctrl{testNode: sess}
	return s, sess, &now
}

const artifact = "/api/cluster/updates/artifacts/1.5.0/x.tar.gz"

func TestTicketSingleUse(t *testing.T) {
	s, sess, _ := ticketServer(t)
	tok, reason := s.issue(sess, PurposeArtifact, artifact, nil)
	if tok == "" {
		t.Fatal(reason)
	}
	if _, err := s.redeem(tok, testNode, "aa"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.redeem(tok, testNode, "aa"); !errors.Is(err, ErrTicketReused) {
		t.Fatalf("second use: %v", err)
	}
	if _, err := s.redeem("not-a-ticket", testNode, "aa"); !errors.Is(err, ErrTicketUnknown) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestTicketExpiry(t *testing.T) {
	s, sess, now := ticketServer(t)
	tok, _ := s.issue(sess, PurposeArtifact, artifact, nil)
	*now = now.Add(31 * time.Second)
	if _, err := s.redeem(tok, testNode, "aa"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestTicketWrongNode(t *testing.T) {
	s, sess, _ := ticketServer(t)
	tok, _ := s.issue(sess, PurposeArtifact, artifact, nil)
	if _, err := s.redeem(tok, "11111111-2222-3333-4444-555555555555", "bb"); !errors.Is(err, ErrTicketForeign) {
		t.Fatalf("another machine: %v", err)
	}
	tok, _ = s.issue(sess, PurposeArtifact, artifact, nil)
	if _, err := s.redeem(tok, testNode, "an-old-serial"); !errors.Is(err, ErrTicketForeign) {
		t.Fatalf("another certificate: %v", err)
	}
}

func TestTicketWrongPurpose(t *testing.T) {
	s, sess, _ := ticketServer(t)
	for _, c := range [][2]string{
		{"shell", "/"},
		{PurposeArtifact, "/api/admin/users"},
		{PurposeArtifact, "/api/cluster/updates/artifacts/../../admin"},
		{PurposeWSProxy, "/api/admin/ws"},
	} {
		if tok, reason := s.issue(sess, c[0], c[1], nil); tok != "" || reason != "purpose" {
			t.Fatalf("%v: %q %q", c, tok, reason)
		}
	}
	// a stream serves only its ticket's resource, and only reads
	h := onlyResource(PurposeArtifact, artifact, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "file")
	}))
	for _, c := range []struct {
		method, path string
		code         int
	}{{"GET", artifact, 200}, {"GET", "/api/cluster/updates/artifacts/1.5.0/y", 403}, {"POST", artifact, 405}} {
		rec := &recorder{header: http.Header{}}
		r, _ := http.NewRequest(c.method, c.path, strings.NewReader(""))
		h.ServeHTTP(rec, r)
		if rec.code != c.code {
			t.Fatalf("%s %s: %d", c.method, c.path, rec.code)
		}
	}
}

type recorder struct {
	header http.Header
	code   int
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = 200
	}
	return len(b), nil
}
func (r *recorder) WriteHeader(c int) { r.code = c }

func TestTicketNoControlSession(t *testing.T) {
	s, sess, _ := ticketServer(t)
	tok, _ := s.issue(sess, PurposeArtifact, artifact, nil)
	s.mu.Lock()
	delete(s.sessions, testNode)
	s.mu.Unlock()
	if _, err := s.redeem(tok, testNode, "aa"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("no live session: %v", err)
	}
	if tok, reason := s.issue(sess, PurposeArtifact, artifact, nil); tok != "" || reason != "no_session" {
		t.Fatalf("issued without a session: %q", reason)
	}
}

// A ticket presented twice is logged; three in ten minutes raise an alert.
func TestTicketReuseAlert(t *testing.T) {
	s, sess, _ := ticketServer(t)
	alerts := make(chan string, 4)
	s.Alert = func(_, detail string) { alerts <- detail }
	for range 3 {
		tok, _ := s.issue(sess, PurposeArtifact, artifact, nil)
		if _, err := s.redeem(tok, testNode, "aa"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.redeem(tok, testNode, "aa"); !errors.Is(err, ErrTicketReused) {
			t.Fatalf("reuse: %v", err)
		}
	}
	select {
	case d := <-alerts:
		if !strings.Contains(d, "3 stream tickets") {
			t.Fatalf("alert: %q", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no alert after three reuses")
	}
}

func TestMaxStreams(t *testing.T) {
	s, sess, _ := ticketServer(t)
	s.Settings = func() Settings { return Settings{MaxStreams: 2} }
	for i := range 2 {
		if tok, reason := s.issue(sess, PurposeArtifact, artifact, nil); tok == "" {
			t.Fatalf("ticket %d: %s", i, reason)
		}
	}
	if tok, reason := s.issue(sess, PurposeArtifact, artifact, nil); tok != "" || reason != "limit" {
		t.Fatalf("over the limit: %q %q", tok, reason)
	}
}

// slowFile is an artifact route that sends a byte every 20 ms until told to stop.
func slowFile(stop *atomic.Bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cluster/updates/artifacts/", func(w http.ResponseWriter, r *http.Request) {
		for !stop.Load() {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	})
	return mux
}

func openArtifact(t *testing.T, c *Client) io.ReadCloser {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://home"+artifact, nil)
	resp, err := c.StreamHTTP(context.Background(), PurposeArtifact, req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.Body
}

// A stream outlives a dropped control channel and re-attaches to the next one.
func TestStreamSurvivesControlReconnect(t *testing.T) {
	var stop atomic.Bool
	defer stop.Store(true)
	h := newHarness(t, slowFile(&stop))
	h.srv.Settings = func() Settings { return Settings{StreamGrace: 5 * time.Second} }
	c := h.connect(nil)
	body := openArtifact(t, c)
	defer body.Close()
	buf := make([]byte, 1)
	if _, err := io.ReadFull(body, buf); err != nil {
		t.Fatal(err)
	}
	h.srv.mu.Lock()
	sess := h.srv.sessions[testNode]
	h.srv.mu.Unlock()
	sess.closeWith(websocket.StatusGoingAway, "the home's API restarting")
	h.waitFor(func() bool {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		return h.srv.sessions[testNode] != nil && h.srv.sessions[testNode] != sess
	}, "the new session")
	for range 5 {
		if _, err := io.ReadFull(body, buf); err != nil {
			t.Fatalf("the stream ended with the control channel: %v", err)
		}
	}
}

// Without a new session within the grace, the home closes the stream.
func TestStreamClosedAfterGrace(t *testing.T) {
	var stop atomic.Bool
	defer stop.Store(true)
	h := newHarness(t, slowFile(&stop))
	h.srv.Settings = func() Settings { return Settings{StreamGrace: 100 * time.Millisecond} }
	c := h.connect(nil)
	body := openArtifact(t, c)
	defer body.Close()
	h.cancel() // the machine does not come back
	h.waitFor(func() bool { return !h.srv.Live(testNode) }, "the session to end")
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, body); done <- err }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream outlived the grace")
	}
}

// Revoking a machine closes its control channel and every stream at once.
func TestRevokeClosesAllStreamsAtOnce(t *testing.T) {
	var stop atomic.Bool
	defer stop.Store(true)
	h := newHarness(t, slowFile(&stop))
	h.srv.Settings = func() Settings { return Settings{StreamGrace: time.Minute} }
	c := h.connect(nil)
	a, b := openArtifact(t, c), openArtifact(t, c)
	defer a.Close()
	defer b.Close()
	_ = h.store.RevokeNode(context.Background(), testNode, time.Now()) // what the home's revoke does first
	h.srv.Revoke(testNode)
	for _, body := range []io.ReadCloser{a, b} {
		done := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, body); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("a stream outlived the revocation")
		}
	}
	h.waitFor(func() bool { return !h.srv.Live(testNode) }, "the control session to end")
}

// TC11, its functional half (design §18.11): under 20 TV pulls (a channel's bitrate each,
// about 4 Mbit/s) and a release download (as fast as it goes) on one machine, every
// control frame and every request on the control channel is answered, none near a stall
// (a 2 s ceiling, which a runner's load never reaches and a stalled channel always does).
// The latency bound itself (50 ms over the runner's own round trip) is the perf tier's:
// TestControlLatencySLO (tickets_perf_test.go, `-tags perf`, make perf-ci), where a shared
// runner's numbers are reported, not every PR's gate.
func TestControlUnderStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("a load test (20 streams): the integration tier runs it")
	}
	m := measureUnderStreams(t)
	t.Log(m.report)
	const ceiling = 2 * time.Second
	if m.ctl[len(m.ctl)-1] > ceiling || m.rpc[len(m.rpc)-1] > ceiling {
		t.Fatalf("a control frame or a request took %v / %v under 20 TV pulls and a release download (a stall: the ceiling is %v)",
			m.ctl[len(m.ctl)-1], m.rpc[len(m.rpc)-1], ceiling)
	}
}

// streamLoad is what measureUnderStreams measured, sorted: the control frames' round trips
// (a ping on the priority lane, no handler), the requests' (an RPC to the home), and the
// runner's own (a one-byte loopback echo, no TLS, no link) in the same window.
type streamLoad struct {
	ctl, rpc, base []time.Duration
	report         string
}

// measureUnderStreams opens 20 TV pulls and a release download on one machine's link and
// times 100 control frames, requests and loopback echoes under them; a frame or a request
// not answered within 5 s fails the test (a stall, not a slow runner). The frames' queue
// waits, which the sender keeps, are reported with the latencies (on a CPU-starved runner
// they are the writer goroutine waiting for a CPU, not traffic queued ahead of them).
func measureUnderStreams(t *testing.T) streamLoad {
	t.Helper()
	var stop atomic.Bool
	t.Cleanup(func() { stop.Store(true) })
	mux := http.NewServeMux()
	chunk := make([]byte, 32<<10)
	mux.HandleFunc("/api/cluster/updates/artifacts/", func(w http.ResponseWriter, r *http.Request) {
		// a TV pull: 32 KiB every 64 ms; the release download: no pause
		pace := 64 * time.Millisecond
		if strings.HasSuffix(r.URL.Path, "/release.tar.gz") {
			pace = 0
		}
		for !stop.Load() && r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			if pace > 0 {
				time.Sleep(pace)
			}
		}
	})
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "pong") })
	h := newHarness(t, mux)
	h.srv.Settings = func() Settings { return Settings{MaxStreams: 32} }
	c := h.connect(nil)
	open := func(name string) {
		req, _ := http.NewRequest(http.MethodGet, "http://home/api/cluster/updates/artifacts/1.5.0/"+name, nil)
		resp, err := c.StreamHTTP(context.Background(), PurposeArtifact, req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		go func() { _, _ = io.Copy(io.Discard, resp.Body) }()
	}
	for i := range 20 {
		open(fmt.Sprintf("channel-%d.ts", i))
	}
	open("release.tar.gz")
	time.Sleep(100 * time.Millisecond) // the streams flowing
	echo := echoServer(t)
	m := streamLoad{ctl: make([]time.Duration, 0, 100), rpc: make([]time.Duration, 0, 100),
		base: make([]time.Duration, 0, 100)}
	for range 100 {
		m.base = append(m.base, echo())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		d, err := c.Ping(ctx)
		if err != nil {
			cancel()
			t.Fatalf("a control frame was not answered: %v", err)
		}
		m.ctl = append(m.ctl, d)
		start := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.home+"/api/ping", nil)
		resp, err := c.RoundTrip(req)
		if err != nil {
			cancel()
			t.Fatalf("a request was not answered: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cancel()
		m.rpc = append(m.rpc, time.Since(start))
	}
	slices.Sort(m.ctl)
	slices.Sort(m.rpc)
	slices.Sort(m.base)
	h.srv.mu.Lock()
	sess := h.srv.sessions[testNode]
	h.srv.mu.Unlock()
	cs := c.current()
	m.report = fmt.Sprintf("control p50 %v p99 %v max %v; rpc p50 %v p99 %v max %v; runner (loopback echo) p50 %v p99 %v; queue waits: machine rpc %v control %v, home rpc %v control %v",
		m.ctl[49], m.ctl[98], m.ctl[99], m.rpc[49], m.rpc[98], m.rpc[99], m.base[49], m.base[98],
		time.Duration(cs.out.maxRPCWait.Load()), time.Duration(cs.out.maxControlWait.Load()),
		time.Duration(sess.out.maxRPCWait.Load()), time.Duration(sess.out.maxControlWait.Load()))
	return m
}

// echoServer is a one-byte echo over plain loopback TCP: the runner's own round trip.
func echoServer(t *testing.T) func() time.Duration {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		b := make([]byte, 1)
		for {
			if _, err := c.Read(b); err != nil {
				return
			}
			if _, err := c.Write(b); err != nil {
				return
			}
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	b := make([]byte, 1)
	return func() time.Duration {
		start := time.Now()
		if _, err := c.Write(b); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Read(b); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
}
