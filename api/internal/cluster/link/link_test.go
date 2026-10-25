package link

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/ca"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/linkctx"
)

// One port serves browsers plain HTTP and machines TLS (the first byte decides).
func TestTLSSnifferServesBothOnOnePort(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "tls=%v", r.TLS != nil)
	})
	h := newHarness(t, api)
	resp, err := http.Get(h.home + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "tls=false" {
		t.Fatalf("plain: %q", b)
	}
	resp, err = h.tlsClient(nil).Get("https://127.0.0.1:" + fmt.Sprint(h.port) + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "tls=true" {
		t.Fatalf("tls: %q", b)
	}
	// the CA certificate is public, never its key
	resp, err = http.Get(h.home + PathCACert)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(b, h.ca.CertPEM) || bytes.Contains(b, []byte("PRIVATE KEY")) {
		t.Fatalf("ca.crt: %q", b)
	}
}

func linkStatus(t *testing.T, c *http.Client, port int) (int, error) {
	t.Helper()
	resp, err := c.Get("https://127.0.0.1:" + fmt.Sprint(port) + PathLink)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// A control channel needs this machine's current certificate from the cluster CA.
func TestLinkRefusesNoCert(t *testing.T) {
	h := newHarness(t, nil)
	if st, err := linkStatus(t, h.tlsClient(nil), h.port); err != nil || st != http.StatusUnauthorized {
		t.Fatalf("no certificate: %d %v", st, err)
	}
}

func TestLinkRefusesWrongCA(t *testing.T) {
	h := newHarness(t, nil)
	other, _ := ca.New("another home")
	key, csr, _ := NewKeyAndCSR(testNode)
	is, err := other.SignNode(context.Background(), csr, testNode, 0)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(is.PEM, key)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := linkStatus(t, h.tlsClient(&pair), h.port); err == nil && st != http.StatusUnauthorized {
		t.Fatalf("another CA's certificate: %d", st)
	}
}

func TestLinkRefusesRevokedSerial(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.nodePair()
	if err := h.store.RevokeNode(context.Background(), testNode, time.Now()); err != nil {
		t.Fatal(err)
	}
	if st, err := linkStatus(t, h.tlsClient(&pair), h.port); err != nil || st != http.StatusUnauthorized {
		t.Fatalf("revoked: %d %v", st, err)
	}
}

// A renewed machine's old certificate opens no new connection; a machine no longer
// approved none at all.
func TestLinkRefusesStaleCertAndNonMember(t *testing.T) {
	h := newHarness(t, nil)
	old := h.nodePair()
	h.issueNode() // renewed: a new current serial
	if st, _ := linkStatus(t, h.tlsClient(&old), h.port); st != http.StatusUnauthorized {
		t.Fatalf("stale certificate: %d", st)
	}
	cur := h.nodePair()
	h.mu.Lock()
	h.members[testNode] = false
	h.mu.Unlock()
	if st, _ := linkStatus(t, h.tlsClient(&cur), h.port); st != http.StatusUnauthorized {
		t.Fatalf("not a member: %d", st)
	}
}

// The machine checks the home's server certificate against the pinned CA and the
// address it dialled (EC1: an address the certificate does not name is refused).
func TestNodeRefusesHomeCertWrongSAN(t *testing.T) {
	h := newHarness(t, nil, func(h *harness) {
		pair, _, err := h.ca.IssueHome(context.Background(), ca.KindServer, []string{"10.9.9.9"}, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		h.srv.SetServerCertificate(pair)
	})
	c := &Client{Home: h.home, NodeID: testNode, Files: CertFiles{Dir: h.dir}}
	err := c.session(context.Background())
	if err == nil || h.srv.Live(testNode) {
		t.Fatalf("connected to a home whose certificate does not name 127.0.0.1: %v", err)
	}
}

// The machine's requests ride the control channel unchanged: method, headers, cookies,
// status, a streamed body, and the request marked as the machine's.
func TestRoundTripperOverControl(t *testing.T) {
	api := http.NewServeMux()
	api.HandleFunc("/api/echo", func(w http.ResponseWriter, r *http.Request) {
		node, _ := linkctx.Node(r.Context())
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Node", node)
		w.Header().Add("Set-Cookie", "mkt_sess=abc; Path=/; HttpOnly")
		w.Header().Set("X-Got", r.Header.Get("X-Send")+"|"+r.Host)
		w.WriteHeader(http.StatusTeapot)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.URL.RequestURI(), b)
	})
	api.HandleFunc("/api/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := range 3 {
			fmt.Fprintf(w, "data: %d\n\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	})
	h := newHarness(t, api)
	c := h.connect(nil)
	req, _ := http.NewRequest(http.MethodPost, h.home+"/api/echo?x=1", strings.NewReader("body"))
	req.Header.Set("X-Send", "hi")
	req.Host = "studio.local:8080"
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot || string(b) != "POST /api/echo?x=1 body" || resp.Header.Get("X-Node") != testNode ||
		resp.Header.Get("X-Got") != "hi|studio.local:8080" || !strings.Contains(resp.Header.Get("Set-Cookie"), "mkt_sess=abc") {
		t.Fatalf("%d %q %v", resp.StatusCode, b, resp.Header)
	}
	req, _ = http.NewRequest(http.MethodGet, h.home+"/api/events", nil)
	resp, err = c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "data: 0\n\ndata: 1\n\ndata: 2\n\n" {
		t.Fatalf("event stream: %q", b)
	}
}

// A cancelled request ends the home's handler.
func TestRoundTripperCancel(t *testing.T) {
	ended := make(chan struct{})
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(ended)
	})
	h := newHarness(t, api)
	c := h.connect(nil)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.home+"/api/wait", nil)
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the home's handler did not end")
	}
}

// An answer over 1 MiB moves to a data stream; a request body over 1 MiB rides one.
func TestLargeAnswerMovesToStream(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789abcdef"), 82000) // 1.3 MB: past the 1 MiB inline limit
	api := http.NewServeMux()
	api.HandleFunc("/api/big", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(big) })
	api.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, n)
	})
	h := newHarness(t, api)
	c := h.connect(nil)
	req, _ := http.NewRequest(http.MethodGet, h.home+"/api/big", nil)
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !bytes.Equal(b, big) {
		t.Fatalf("read %d of %d bytes: %v", len(b), len(big), err)
	}
	req, _ = http.NewRequest(http.MethodPost, h.home+"/api/upload", bytes.NewReader(big))
	resp, err = c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != fmt.Sprint(len(big)) {
		t.Fatalf("upload: %q", b)
	}
}

// A proxied WebSocket (the hub socket, an agent session) becomes a ws_proxy stream.
func TestWebSocketOverStream(t *testing.T) {
	api := http.NewServeMux()
	api.HandleFunc("/api/cluster/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		typ, b, err := c.Read(r.Context())
		if err == nil {
			node, _ := linkctx.Node(r.Context())
			_ = c.Write(r.Context(), typ, append(b, []byte(" from "+node)...))
		}
		_ = c.Close(websocket.StatusNormalClosure, "")
	})
	h := newHarness(t, api)
	cl := h.connect(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://127.0.0.1/api/cluster/ws", &websocket.DialOptions{HTTPClient: &http.Client{Transport: cl}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()
	if err := ws.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, b, err := ws.Read(ctx)
	if err != nil || string(b) != "hello from "+testNode {
		t.Fatalf("%q %v", b, err)
	}
}

// The home asks the machine for bytes (stream.offer): the machine dials the stream and
// serves it; the home reads it like an HTTP upstream.
func TestPullOverOffer(t *testing.T) {
	h := newHarness(t, nil)
	h.connect(func(_ context.Context, purpose, resource string, conn net.Conn) {
		// the machine's streaming port, played here by one HTTP answer
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s %s %s", purpose, resource, r.URL.Path)
		})}
		srv.SetKeepAlivesEnabled(false)
		_ = srv.Serve(newOneConn(conn.(*streamConn)))
	})
	req, _ := http.NewRequest(http.MethodGet, "http://node/stream/channel/7/index.m3u8", nil)
	resp, err := h.srv.PullHTTP(context.Background(), testNode, PurposeTVPull, req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "tv_pull /stream/channel/7/index.m3u8 /stream/channel/7/index.m3u8" {
		t.Fatalf("%q", b)
	}
	if _, err := h.srv.Pull(context.Background(), "11111111-2222-3333-4444-555555555555", PurposeTVPull, "/x"); !errors.Is(err, ErrNoLink) {
		t.Fatalf("a machine off the link: %v", err)
	}
}

// Renewal over the control channel: a new key and certificate, the old serial then
// refused for new connections.
func TestRenewOverControl(t *testing.T) {
	h := newHarness(t, nil)
	c := h.connect(nil)
	before := h.nodePair().Leaf.SerialNumber.String()
	if err := c.Renew(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := h.nodePair()
	if after.Leaf.SerialNumber.String() == before {
		t.Fatal("no new certificate")
	}
	if cur, _ := h.store.Current(context.Background(), testNode); cur != ca.SerialHex(after.Leaf.SerialNumber) {
		t.Fatalf("current %s", cur)
	}
	if st, err := linkStatus(t, h.tlsClient(&after), h.port); err != nil || st == http.StatusUnauthorized {
		t.Fatalf("the renewed certificate: %d %v", st, err)
	}
}

// A dropped control channel fails what is in flight with link_lost and comes back by
// itself; a GET cut before its answer is tried once more.
func TestReconnectAndLinkLost(t *testing.T) {
	var calls atomic.Int32
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			<-r.Context().Done() // the first try waits until the channel drops
			return
		}
		fmt.Fprint(w, "second")
	})
	h := newHarness(t, api)
	c := h.connect(nil)
	go func() {
		h.waitFor(func() bool { return calls.Load() == 1 }, "the first try")
		h.srv.mu.Lock()
		sess := h.srv.sessions[testNode]
		h.srv.mu.Unlock()
		sess.closeWith(websocket.StatusGoingAway, "test")
	}()
	req, _ := http.NewRequest(http.MethodGet, h.home+"/api/x", nil)
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "second" {
		t.Fatalf("%q", b)
	}
	// a POST is not tried again
	calls.Store(0)
	go func() {
		h.waitFor(func() bool { return calls.Load() == 1 && h.srv.Live(testNode) }, "the POST")
		h.srv.mu.Lock()
		sess := h.srv.sessions[testNode]
		h.srv.mu.Unlock()
		sess.closeWith(websocket.StatusGoingAway, "test")
	}()
	h.waitFor(func() bool { return c.Connected() }, "reconnect")
	req, _ = http.NewRequest(http.MethodPost, h.home+"/api/x", strings.NewReader("x"))
	if _, err := c.RoundTrip(req); !errors.Is(err, ErrLinkLost) {
		t.Fatalf("a POST cut by the drop: %v", err)
	}
}

// An answer the dropped channel cuts after it began is never a complete one: the body
// read ends with link_lost (not io.EOF), whatever the home's handler did once its request
// ended.
func TestAnAnswerCutByTheDropIsLinkLost(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "part")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the channel drops in the middle of the answer
		_, _ = io.WriteString(w, " and the rest")
	})
	h := newHarness(t, api)
	c := h.connect(nil)
	req, _ := http.NewRequest(http.MethodGet, h.home+"/api/x", nil)
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "part" {
		t.Fatalf("the answer's start: %q %v", buf, err)
	}
	h.srv.mu.Lock()
	sess := h.srv.sessions[testNode]
	h.srv.mu.Unlock()
	sess.closeWith(websocket.StatusGoingAway, "test")
	rest, err := io.ReadAll(resp.Body)
	if !errors.Is(err, ErrLinkLost) {
		t.Fatalf("the cut answer read %q, %v; want link_lost", rest, err)
	}
}

// A large answer (moved to a data stream) the machine cancels in the middle, its session
// alive, ends on the home too: the home's stream closes at once, and the machine reads an
// error, never the end of a complete body.
func TestACancelledMovedAnswerClosesItsStream(t *testing.T) {
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for r.Context().Err() == nil { // as long as it is read
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	h := newHarness(t, api)
	c := h.connect(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.home+"/api/big", nil)
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// past the inline part: the rest comes on the data stream
	if _, err := io.CopyN(io.Discard, resp.Body, MaxInline+(256<<10)); err != nil {
		t.Fatal(err)
	}
	open := func() int {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		if n := h.srv.nodes[testNode]; n != nil {
			return len(n.open)
		}
		return 0
	}
	if open() != 1 {
		t.Fatalf("%d streams open on the home, want the answer's", open())
	}
	cancel()
	h.waitFor(func() bool { return open() == 0 }, "the home's stream of the cancelled answer to close")
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("the cancelled answer read to a complete end")
	}
	if !h.srv.Live(testNode) {
		t.Fatal("the session ended with the request")
	}
}

// An answer whose body is shorter than its Content-Length is not a complete one.
func TestAShortBodyIsUnexpectedEOF(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = io.WriteString(w, "five!")
	})
	h := newHarness(t, api)
	c := h.connect(nil)
	req, _ := http.NewRequest(http.MethodGet, h.home+"/api/x", nil)
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, err := io.ReadAll(resp.Body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("a short body read %q, %v; want io.ErrUnexpectedEOF", b, err)
	}
}

// With browser HTTPS on, a machine asks for its web certificate on the control channel
// and keeps it (its supervisor serves 8443 with it); off, it removes it (AC15).
func TestWebCertificateWhileHTTPSIsOn(t *testing.T) {
	h := newHarness(t, nil)
	h.srv.Settings = func() Settings { return Settings{WebHTTPS: true} }
	var asked string
	h.srv.WebCert = func(ctx context.Context, node, remote, csr string) (string, error) {
		asked = remote
		is, err := h.ca.SignWeb(ctx, []byte(csr), node, []string{remote, "studio.local"}, 90)
		if err != nil {
			return "", err
		}
		return string(is.PEM), nil
	}
	h.connect(nil)
	files := CertFiles{Dir: h.dir}
	h.waitFor(func() bool { return !files.webDue(time.Now()) }, "the web certificate")
	if asked != "127.0.0.1" {
		t.Fatalf("signed for %q, not the address the link came from", asked)
	}
	pair, err := tls.LoadX509KeyPair(filepath.Join(h.dir, WebCertFile), filepath.Join(h.dir, WebKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: h.ca.Pool(), DNSName: "studio.local"}); err != nil {
		t.Fatal(err)
	}
}
