package link

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// ErrLinkLost: the control channel dropped while a request was in flight.
var ErrLinkLost = errors.New("link: link_lost")

// ErrNotConnected: the machine holds no control channel now.
var ErrNotConnected = errors.New("link: not connected to the home")

// Client is a machine's end of the link (held by a full node's API process).
type Client struct {
	// Home is the home's API address (http://host:port, from node.json): the link dials
	// the same port with TLS.
	Home   string
	NodeID string
	Files  CertFiles
	// Offer serves a stream the home asked for (tv_pull, relay_pull): the machine's
	// streaming port. conn is closed when Offer returns.
	Offer  func(ctx context.Context, purpose, resource string, conn net.Conn)
	Logger *slog.Logger
	Now    func() time.Time
	// OnState is told when the channel comes up or goes (the node's status).
	OnState func(connected bool, detail string)

	cache tls.ClientSessionCache
	once  sync.Once

	mu       sync.Mutex
	sess     *csess
	upCh     chan struct{}
	material *Material
	mode     string
	lastErr  string
	since    time.Time
	nextID   atomic.Uint32
	renewing atomic.Bool
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Client) init() {
	c.once.Do(func() {
		c.cache = tls.NewLRUClientSessionCache(64)
		c.mu.Lock()
		if c.upCh == nil {
			c.upCh = make(chan struct{})
		}
		c.mu.Unlock()
	})
}

// Status is the link as the machine sees it.
type Status struct {
	Connected bool      `json:"connected"`
	Since     time.Time `json:"since,omitzero"`
	Mode      string    `json:"mode,omitempty"`
	HasCert   bool      `json:"has_cert"`
	NotAfter  time.Time `json:"not_after,omitzero"`
	LastError string    `json:"last_error,omitempty"`
}

// Status reports the link.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{Connected: c.sess != nil, Mode: c.mode, LastError: c.lastErr}
	if st.Connected {
		st.Since = c.since
	}
	if c.material != nil {
		st.HasCert, st.NotAfter = true, c.material.Leaf.NotAfter
	}
	return st
}

// Connected reports whether the control channel is up.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess != nil
}

// Mode is the home's link mode as its hello said ("" before the first session).
func (c *Client) Mode() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

func (c *Client) setErr(err error) {
	c.mu.Lock()
	if err != nil {
		c.lastErr = err.Error()
	}
	c.mu.Unlock()
}

// homeHostPort is the home's host and port as node.json names them.
func (c *Client) homeHostPort() (string, string, error) {
	u, err := url.Parse(strings.TrimRight(c.Home, "/"))
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("link: bad home address %q", c.Home)
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return u.Hostname(), port, nil
}

// tlsConfig pins the cluster CA and the address dialled (the home's server certificate
// must name it, EC1) and presents the machine's certificate.
func (c *Client) tlsConfig(host string) (*tls.Config, error) {
	m, err := c.Files.Load()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.material = m
	c.mu.Unlock()
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		RootCAs:            m.CA,
		ServerName:         host,
		NextProtos:         []string{"http/1.1"},
		ClientSessionCache: c.cache,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.material == nil {
				return &tls.Certificate{}, nil
			}
			pair := c.material.Pair
			return &pair, nil
		},
	}, nil
}

func (c *Client) httpClient(cfg *tls.Config) *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: false,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second}}
}

func (c *Client) wsURL(path string) (string, string, error) {
	host, port, err := c.homeHostPort()
	if err != nil {
		return "", "", err
	}
	return "wss://" + net.JoinHostPort(host, port) + path, host, nil
}

// Run keeps the control channel up until ctx ends: backoff 1 s doubling to 30 s with
// ±20 % jitter between tries; a machine without a certificate waits for one.
func (c *Client) Run(ctx context.Context) {
	c.init()
	delay := backoffFloor
	for ctx.Err() == nil {
		start := c.now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.setErr(err)
		if c.OnState != nil {
			c.OnState(false, fmt.Sprint(err))
		}
		if c.now().Sub(start) > time.Minute {
			delay = backoffFloor
		}
		wait := time.Duration(float64(delay) * (0.8 + 0.4*mrand.Float64())) //nolint:gosec // jitter, not secrets
		if errors.Is(err, ErrNoMaterial) {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		delay = min(delay*2, backoffCeiling)
	}
}

// csess is one control session on the machine.
type csess struct {
	cl     *Client
	conn   *websocket.Conn
	out    *sender
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	calls   map[uint32]*call
	waiters map[string]chan json.RawMessage
	hello   Hello
}

// call is one request in flight on the control channel.
type call struct {
	resp  chan RespHead
	body  *bodyBuf
	moved chan net.Conn // the rest of a large answer's body (nil conn: none)
	errCh chan error
}

func (c *Client) session(ctx context.Context) error {
	c.init()
	u, host, err := c.wsURL(PathLink)
	if err != nil {
		return err
	}
	cfg, err := c.tlsConfig(host)
	if err != nil {
		return err
	}
	dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
	conn, resp, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPClient: c.httpClient(cfg),
		CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		dcancel()
		if resp != nil {
			return fmt.Errorf("link: the home refused the control channel (%d)", resp.StatusCode)
		}
		return err
	}
	conn.SetReadLimit(HeadSize + MaxPayload + 64)
	typ, b, err := conn.Read(dctx)
	dcancel()
	var hello Hello
	if err != nil || typ != websocket.MessageText || json.Unmarshal(b, &hello) != nil || hello.T != "hello" {
		_ = conn.CloseNow()
		return fmt.Errorf("link: no hello from the home: %v", err)
	}
	sctx, cancel := context.WithCancel(ctx)
	s := &csess{cl: c, conn: conn, out: newSender(), ctx: sctx, cancel: cancel, calls: map[uint32]*call{},
		waiters: map[string]chan json.RawMessage{}, hello: hello}
	c.mu.Lock()
	c.sess, c.mode, c.since, c.lastErr = s, hello.Mode, c.now(), ""
	up := c.upCh
	c.upCh = make(chan struct{})
	c.mu.Unlock()
	close(up)
	c.log().Info("node: cluster link up", "session", hello.Session, "mode", hello.Mode, "event", "node_link_up")
	if c.OnState != nil {
		c.OnState(true, "")
	}
	defer func() {
		c.mu.Lock()
		if c.sess == s {
			c.sess = nil
		}
		c.mu.Unlock()
		s.fail()
	}()
	go func() {
		if err := s.out.writeLoop(conn); err != nil {
			cancel()
		}
	}()
	go s.renewWhenDue()
	go s.keepWebCert(hello.WebHTTPS)
	err = s.readLoop()
	cancel()
	s.out.close()
	_ = conn.Close(websocket.StatusNormalClosure, "")
	return err
}

func (s *csess) readLoop() error {
	for {
		typ, data, err := s.conn.Read(s.ctx)
		if err != nil {
			if st := websocket.CloseStatus(err); st != -1 {
				return fmt.Errorf("link: the home closed the channel (%d)", st)
			}
			return err
		}
		if typ != websocket.MessageBinary {
			continue
		}
		f, err := Decode(data)
		if err != nil {
			return err
		}
		s.dispatch(f)
	}
}

func (s *csess) dispatch(f Frame) {
	switch f.Kind {
	case KindEvent:
		s.onEvent(f.Payload)
	case KindResp, KindChunk, KindEnd, KindWhole:
		s.mu.Lock()
		cl := s.calls[f.ID]
		s.mu.Unlock()
		if cl == nil {
			return
		}
		switch f.Kind {
		case KindResp:
			var h RespHead
			if json.Unmarshal(f.Payload, &h) != nil {
				h = RespHead{Status: http.StatusBadGateway}
			}
			select {
			case cl.resp <- h:
			default:
			}
		case KindWhole:
			s.forget(f.ID)
			var h RespHead
			if json.Unmarshal(f.Payload, &h) != nil {
				h = RespHead{Status: http.StatusBadGateway}
			}
			if len(h.Body) > 0 {
				_ = cl.body.write(h.Body)
			}
			h.Body = nil
			cl.body.closeWith(nil)
			cl.moved <- nil
			select {
			case cl.resp <- h:
			default:
			}
		case KindChunk:
			_ = cl.body.write(f.Payload)
		case KindEnd:
			s.forget(f.ID)
			var e EndBody
			if len(f.Payload) > 0 {
				_ = json.Unmarshal(f.Payload, &e)
			}
			switch {
			case e.Moved != nil:
				go func() {
					conn, err := s.cl.dialStream(context.Background(), e.Moved.Ticket)
					if err != nil {
						cl.body.closeWith(err)
						cl.moved <- nil
						return
					}
					cl.body.closeWith(nil)
					cl.moved <- conn
				}()
			case e.Error != "":
				cl.body.closeWith(errors.New(e.Error))
				cl.moved <- nil
			default:
				cl.body.closeWith(nil)
				cl.moved <- nil
			}
		}
	}
}

func (s *csess) onEvent(b []byte) {
	var h eventHead
	if json.Unmarshal(b, &h) != nil {
		return
	}
	switch h.T {
	case "stream.offer":
		var t StreamTicket
		if json.Unmarshal(b, &t) != nil || s.cl.Offer == nil {
			return
		}
		go s.cl.serveOffer(t)
	default:
		if h.ID == "" {
			return
		}
		s.mu.Lock()
		ch := s.waiters[h.ID]
		delete(s.waiters, h.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- append(json.RawMessage(nil), b...)
		}
	}
}

func (s *csess) forget(id uint32) {
	s.mu.Lock()
	delete(s.calls, id)
	s.mu.Unlock()
}

// fail ends every request in flight with link_lost.
func (s *csess) fail() {
	s.mu.Lock()
	calls := s.calls
	s.calls = map[uint32]*call{}
	waiters := s.waiters
	s.waiters = map[string]chan json.RawMessage{}
	s.mu.Unlock()
	for _, cl := range calls {
		select {
		case cl.errCh <- ErrLinkLost:
		default:
		}
		cl.body.closeWith(ErrLinkLost)
	}
	for _, ch := range waiters {
		close(ch)
	}
}

// ask sends an event with an id and waits for the home's answer to it.
func (s *csess) ask(ctx context.Context, v map[string]any) (json.RawMessage, error) {
	id := strconv.FormatUint(uint64(s.cl.nextID.Add(1)), 10)
	v["id"] = id
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	ch := make(chan json.RawMessage, 1)
	s.mu.Lock()
	s.waiters[id] = ch
	s.mu.Unlock()
	if err := s.out.send(ctx, Frame{Kind: KindEvent, Class: ClassControl, Payload: b}); err != nil {
		return nil, err
	}
	select {
	case ans, ok := <-ch:
		if !ok {
			return nil, ErrLinkLost
		}
		return ans, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, ErrLinkLost
	}
}

func (c *Client) current() *csess {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess
}

// waitUp waits up to d for a live control session other than old.
func (c *Client) waitUp(ctx context.Context, old *csess, d time.Duration) *csess {
	c.init()
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		c.mu.Lock()
		s, up := c.sess, c.upCh
		c.mu.Unlock()
		if s != nil && s != old && s.ctx.Err() == nil {
			return s
		}
		select {
		case <-up:
		case <-t.C:
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

// Ping is one control-frame round trip on the control channel (ping, pong: no request
// handler, no data stream).
func (c *Client) Ping(ctx context.Context) (time.Duration, error) {
	s := c.current()
	if s == nil {
		return 0, ErrNotConnected
	}
	start := time.Now()
	ans, err := s.ask(ctx, map[string]any{"t": "ping"})
	if err != nil {
		return 0, err
	}
	var h eventHead
	if json.Unmarshal(ans, &h) != nil || h.T != "pong" {
		return 0, errors.New("link: no pong")
	}
	return time.Since(start), nil
}

// ---------- streams ----------

// OpenStream asks the home for a ticket and opens a data stream with it.
func (c *Client) OpenStream(ctx context.Context, purpose, resource string) (net.Conn, error) {
	s := c.current()
	if s == nil {
		return nil, ErrNotConnected
	}
	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ans, err := s.ask(actx, map[string]any{"t": "stream.open", "purpose": purpose, "resource": resource})
	if err != nil {
		return nil, err
	}
	var t StreamTicket
	if err := json.Unmarshal(ans, &t); err != nil {
		return nil, err
	}
	if t.T != "stream.ticket" || t.Ticket == "" {
		return nil, fmt.Errorf("link: stream refused: %s", t.Reason)
	}
	return c.dialStream(ctx, t.Ticket)
}

// dialStream opens a stream with a ticket.
func (c *Client) dialStream(ctx context.Context, ticket string) (net.Conn, error) {
	u, host, err := c.wsURL(PathStream)
	if err != nil {
		return nil, err
	}
	cfg, err := c.tlsConfig(host)
	if err != nil {
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPClient: c.httpClient(cfg),
		HTTPHeader: http.Header{"Authorization": {TicketScheme + " " + ticket}}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("link: the home refused the stream (%d)", resp.StatusCode)
		}
		return nil, err
	}
	return newStreamConn(ws, nil), nil
}

// StreamHTTP sends req on a new stream of purpose and returns the answer. An upgrade's
// answer (101) has a body that reads and writes the stream (an http.ReverseProxy splices
// it); any other closes the stream with its body.
func (c *Client) StreamHTTP(ctx context.Context, purpose string, req *http.Request) (*http.Response, error) {
	conn, err := c.OpenStream(ctx, purpose, req.URL.RequestURI())
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(req.Context(), func() { _ = conn.Close() })
	out := req.Clone(req.Context())
	out.URL = &url.URL{Path: req.URL.Path, RawQuery: req.URL.RawQuery, RawPath: req.URL.RawPath}
	out.RequestURI = ""
	if err := out.Write(conn); err != nil {
		stop()
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(conn, 32<<10)
	resp, err := http.ReadResponse(br, out)
	if err != nil {
		stop()
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		stop()
		resp.Body = &upgraded{r: br, c: conn}
		return resp, nil
	}
	resp.Body = &closeBoth{ReadCloser: resp.Body, c: conn, stop: stop}
	return resp, nil
}

// upgraded is an upgraded answer's body: the stream itself.
type upgraded struct {
	r *bufio.Reader
	c net.Conn
}

func (u *upgraded) Read(p []byte) (int, error)  { return u.r.Read(p) }
func (u *upgraded) Write(p []byte) (int, error) { return u.c.Write(p) }
func (u *upgraded) Close() error                { return u.c.Close() }

func (c *Client) serveOffer(t StreamTicket) {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	conn, err := c.dialStream(ctx, t.Ticket)
	if err != nil {
		c.log().Warn("node: the home's stream could not be opened", "purpose", t.Purpose, "err", err,
			"event", "node_link_offer_failed")
		return
	}
	defer conn.Close()
	c.Offer(ctx, t.Purpose, t.Resource, conn)
}

// ---------- requests on the control channel ----------

// RoundTrip carries a request to the home: small ones on the control channel (resp and
// chunks; past 1 MiB the answer's rest comes on a data stream), upgrades (a proxied
// WebSocket) and bodies over 1 MiB on a data stream. A GET or HEAD the dropped channel
// cut before any answer is tried once more after it is back.
func (c *Client) RoundTrip(req *http.Request) (*http.Response, error) {
	c.init()
	if strings.EqualFold(req.Header.Get("Connection"), "upgrade") || req.Header.Get("Upgrade") != "" {
		return c.StreamHTTP(req.Context(), PurposeWSProxy, req)
	}
	var body []byte
	if req.Body != nil && req.Body != http.NoBody {
		b, err := io.ReadAll(io.LimitReader(req.Body, MaxInline+1))
		if err != nil {
			return nil, err
		}
		if len(b) > MaxInline {
			// too large for the channel: the whole request on a stream
			rest := req.Body
			out := req.Clone(req.Context())
			out.Body = io.NopCloser(io.MultiReader(bytes.NewReader(b), rest))
			return c.StreamHTTP(req.Context(), PurposeSvcBody, out)
		}
		_ = req.Body.Close()
		body = b
	}
	resp, used, started, err := c.roundTripOnce(req, body)
	if err != nil && errors.Is(err, ErrLinkLost) && !started && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		if c.waitUp(req.Context(), used, 10*time.Second) != nil {
			resp, _, _, err = c.roundTripOnce(req, body)
		}
	}
	return resp, err
}

func (c *Client) roundTripOnce(req *http.Request, body []byte) (*http.Response, *csess, bool, error) {
	s := c.current()
	if s == nil {
		return nil, nil, false, ErrNotConnected
	}
	id := c.nextID.Add(1)
	cl := &call{resp: make(chan RespHead, 1), body: newBodyBuf(0), moved: make(chan net.Conn, 1), errCh: make(chan error, 1)}
	s.mu.Lock()
	s.calls[id] = cl
	s.mu.Unlock()
	head := ReqHead{Method: req.Method, URI: req.URL.RequestURI(), Host: req.Host, Header: req.Header.Clone(),
		Body: len(body) > 0}
	hb, err := json.Marshal(head)
	if err == nil && len(hb) > MaxPayload {
		err = ErrFrameTooLarge
	}
	if err != nil {
		s.forget(id)
		return nil, s, false, err
	}
	ctx := req.Context()
	send := func(f Frame) error { return s.out.send(ctx, f) }
	if err := send(Frame{ID: id, Kind: KindReq, Class: ClassRPC, Payload: hb}); err != nil {
		s.forget(id)
		return nil, s, false, linkErr(err)
	}
	if len(body) > 0 {
		for _, p := range split(body) {
			if err := send(Frame{ID: id, Kind: KindChunk, Class: ClassRPC, Payload: p}); err != nil {
				s.forget(id)
				return nil, s, false, linkErr(err)
			}
		}
		if err := send(Frame{ID: id, Kind: KindEnd, Class: ClassRPC}); err != nil {
			s.forget(id)
			return nil, s, false, linkErr(err)
		}
	}
	cancelRemote := func() {
		_ = s.out.send(context.Background(), Frame{ID: id, Kind: KindCancel, Class: ClassControl})
	}
	var h RespHead
	select {
	case h = <-cl.resp:
	case err := <-cl.errCh:
		return nil, s, false, err
	case <-s.ctx.Done():
		// the session ended (perhaps before this call was registered with it)
		s.forget(id)
		return nil, s, false, ErrLinkLost
	case <-ctx.Done():
		s.forget(id)
		cancelRemote()
		return nil, s, false, ctx.Err()
	}
	stop := context.AfterFunc(ctx, func() {
		cancelRemote()
		cl.body.closeWith(ctx.Err())
	})
	resp := &http.Response{Status: fmt.Sprintf("%d %s", h.Status, http.StatusText(h.Status)), StatusCode: h.Status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: h.Header, Request: req, ContentLength: -1,
		Body: &callBody{cl: cl, stop: stop}}
	if resp.Header == nil {
		resp.Header = http.Header{}
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			resp.ContentLength = n
		}
	}
	if req.Method == http.MethodHead {
		resp.ContentLength = 0
	}
	return resp, s, true, nil
}

func linkErr(err error) error {
	if errors.Is(err, ErrClosed) {
		return ErrLinkLost
	}
	return err
}

// callBody reads an answer's chunks, then the rest from its data stream when it moved.
type callBody struct {
	cl     *call
	stop   func() bool
	stream net.Conn
	done   bool
	once   sync.Once
}

func (b *callBody) Read(p []byte) (int, error) {
	if b.stream != nil {
		return b.stream.Read(p)
	}
	if b.done {
		return 0, io.EOF
	}
	n, err := b.cl.body.Read(p)
	if err == io.EOF {
		if conn := <-b.cl.moved; conn != nil {
			b.stream = conn
			if n > 0 {
				return n, nil
			}
			return conn.Read(p)
		}
		b.done = true
	}
	return n, err
}

func (b *callBody) Close() error {
	b.once.Do(func() {
		b.stop()
		b.cl.body.closeWith(io.ErrClosedPipe)
		if b.stream != nil {
			_ = b.stream.Close()
		}
	})
	return nil
}

// ---------- certificates ----------

// renewWhenDue renews the machine's certificate over the channel once a third of its
// life is left (design §18.3), checked hourly while the session lasts.
func (s *csess) renewWhenDue() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.cl.mu.Lock()
		m := s.cl.material
		s.cl.mu.Unlock()
		if m != nil && m.Remaining(s.cl.now()) < 1.0/3 {
			if err := s.cl.Renew(s.ctx); err != nil {
				s.cl.log().Warn("node: certificate renewal failed", "err", err, "event", "node_link_renew_failed")
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// keepWebCert keeps the machine's web certificate for browser HTTPS while the home says
// so (cert.web), and removes it when it does not (the supervisor then closes 8443).
func (s *csess) keepWebCert(on bool) {
	if !on {
		_ = s.cl.Files.SaveWeb(nil, nil)
		return
	}
	if !s.cl.Files.webDue(s.cl.now()) {
		return
	}
	keyPEM, csrPEM, err := NewKeyAndCSR(s.cl.NodeID)
	if err != nil {
		return
	}
	actx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	ans, err := s.ask(actx, map[string]any{"t": "cert.web", "csr": string(csrPEM)})
	if err != nil {
		return
	}
	var is CertIssued
	if json.Unmarshal(ans, &is) != nil || is.T != "cert.issued" {
		s.cl.log().Warn("node: the home refused this machine's web certificate", "reason", is.Reason, "event", "node_web_cert_refused")
		return
	}
	if err := s.cl.Files.SaveWeb([]byte(is.Cert), keyPEM); err != nil {
		s.cl.log().Warn("node: the web certificate could not be kept", "err", err, "event", "node_web_cert_failed")
		return
	}
	s.cl.log().Info("node: web certificate for HTTPS on 8443 received", "event", "node_web_cert")
}

// Renew asks the home for a new certificate (cert.renew) with a new key and saves it;
// the next connections present it.
func (c *Client) Renew(ctx context.Context) error {
	if !c.renewing.CompareAndSwap(false, true) {
		return nil
	}
	defer c.renewing.Store(false)
	s := c.current()
	if s == nil {
		return ErrNotConnected
	}
	keyPEM, csrPEM, err := NewKeyAndCSR(c.NodeID)
	if err != nil {
		return err
	}
	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ans, err := s.ask(actx, map[string]any{"t": "cert.renew", "csr": string(csrPEM)})
	if err != nil {
		return err
	}
	var is CertIssued
	if err := json.Unmarshal(ans, &is); err != nil {
		return err
	}
	if is.T != "cert.issued" {
		return fmt.Errorf("link: renewal refused: %s", is.Reason)
	}
	if err := c.Files.Save(keyPEM, []byte(is.Cert), []byte(is.CACert), is.CASHA256); err != nil {
		return err
	}
	m, err := c.Files.Load()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.material = m
	c.mu.Unlock()
	c.log().Info("node: cluster certificate renewed", "not_after", is.NotAfter, "event", "node_link_renewed")
	return nil
}
