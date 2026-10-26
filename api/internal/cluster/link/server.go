package link

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/ca"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/linkctx"
)

// Paths on the home's API.
const (
	PathLink   = "/api/cluster/link"
	PathStream = "/api/cluster/link/stream"
	PathCACert = "/api/cluster/ca.crt"
	// PathPull is the home's loopback route its front door reads a machine's channel from
	// (tv_pull, relay_pull over that machine's link).
	PathPull = "/api/cluster/link/pull"
	// PathLocalAgent and PathLocalFetch are a full node's loopback routes: its
	// supervisor's machine agent and its pipeline processes reach the home's channel
	// through them (ws_proxy), and its updater the home's files (artifact, wheelhouse).
	PathLocalAgent = "/api/cluster/link/local/agent"
	PathLocalFetch = "/api/cluster/link/local/fetch"
	// TicketScheme is the Authorization scheme of a stream connection.
	TicketScheme = "Maktaba-Ticket"
)

// Close codes on the link (the channel's own, design-agent.md §4.6).
const (
	CloseReplaced = websocket.StatusCode(4009)
	CloseRevoked  = websocket.StatusCode(4010)
	CloseTimeout  = websocket.StatusCode(4008)
	CloseGrace    = websocket.StatusCode(4014)
)

// Purposes of a data stream (design §18.5).
const (
	PurposeTVPull     = "tv_pull"
	PurposeRelayPull  = "relay_pull"
	PurposeArtifact   = "artifact"
	PurposeWheelhouse = "wheelhouse"
	PurposeBackup     = "backup"
	PurposeVOD        = "vod"
	PurposeSvcBody    = "svc_body"
	PurposeWSProxy    = "ws_proxy"
)

// homeServed are the purposes a machine opens to read from the home, and the paths each
// may ask for (a stream carries one request, which must be under one of them).
var homeServed = map[string][]string{
	PurposeArtifact:   {"/api/cluster/updates/artifacts/", "/api/cluster/updates/tuf/"},
	PurposeWheelhouse: {"/api/cluster/updates/wheelhouse/", "/api/cluster/updates/wheels/"},
	PurposeBackup:     {"/api/cluster/backups/"},
	PurposeVOD:        {"/stream/"},
	PurposeWSProxy:    {"/api/cluster/agent", "/api/cluster/ws"},
}

// homeAsked are the purposes the home asks a machine for (stream.offer).
var homeAsked = map[string]bool{PurposeTVPull: true, PurposeRelayPull: true}

// Settings are the link's settings (rules cluster.link.*).
type Settings struct {
	Mode        string        `json:"mode"`
	MaxStreams  int           `json:"max_streams"`
	StreamGrace time.Duration `json:"-"`
	// WebHTTPS: every machine serves its pages on 8443 too (AC15): a machine asks for
	// its web certificate (cert.web) while it is on.
	WebHTTPS bool `json:"web_https"`
}

// DefaultSettings: prefer, 32 streams, 60 s grace.
func DefaultSettings() Settings {
	return Settings{Mode: linkctx.ModePrefer, MaxStreams: 32, StreamGrace: 60 * time.Second}
}

// Server is the home's end of the link.
type Server struct {
	CA    *ca.CA
	Certs ca.Store
	// Approved reports whether a machine is an approved member.
	Approved func(ctx context.Context, nodeID string) (bool, error)
	// Handler serves the requests machines send: proxied home routes on the control
	// channel and the home-served streams (the API's public handler; requests are marked
	// with linkctx.WithNode).
	Handler http.Handler
	// Targets serve a purpose with something else than Handler (vod: the home's streaming
	// port).
	Targets map[string]http.Handler
	// Settings reads the link's settings (nil: DefaultSettings).
	Settings func() Settings
	// OnSession reports a machine's control session coming up or going (link_since).
	OnSession func(nodeID string, up bool)
	// Alert is told of repeated ticket reuse (3 in 10 minutes).
	Alert func(nodeID, detail string)
	// WebCert signs a machine's web certificate for browser HTTPS (cert.web, AC15) for the
	// addresses the home allows it (remote: the address its link came from); nil: none.
	WebCert func(ctx context.Context, nodeID, remote, csrPEM string) (certPEM string, err error)
	Logger  *slog.Logger
	Now     func() time.Time

	mu       sync.Mutex
	sessions map[string]*ctrl
	nodes    map[string]*nodeStreams
	tickets  map[[32]byte]*ticket
	reuse    []time.Time
	server   atomic.Pointer[tls.Certificate]
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Server) settings() Settings {
	st := DefaultSettings()
	if s.Settings != nil {
		got := s.Settings()
		if got.Mode != "" {
			st.Mode = got.Mode
		}
		if got.MaxStreams > 0 {
			st.MaxStreams = got.MaxStreams
		}
		if got.StreamGrace > 0 {
			st.StreamGrace = got.StreamGrace
		}
		st.WebHTTPS = got.WebHTTPS
	}
	return st
}

// SetServerCertificate installs the home's server certificate (the TLS side of the
// API's port).
func (s *Server) SetServerCertificate(c tls.Certificate) { s.server.Store(&c) }

// TLSConfig is the API port's TLS side (SniffListener): TLS 1.3, the home's server
// certificate, a client certificate verified against the cluster CA when given (browsers
// on 8443 give none; /api/cluster/link and its streams require one).
func (s *Server) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"http/1.1"},
		ClientAuth: tls.VerifyClientCertIfGiven,
		ClientCAs:  s.CA.Pool(),
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if c := s.server.Load(); c != nil {
				return c, nil
			}
			return nil, errors.New("link: the home has no server certificate yet")
		},
	}
}

// Errors of a machine's authentication.
var (
	ErrNoCertificate = errors.New("link: no client certificate")
	ErrNotMember     = errors.New("link: the certificate's machine is not an approved member")
	ErrStaleCert     = errors.New("link: not the machine's current certificate")
	ErrRevokedCert   = errors.New("link: the certificate is revoked")
)

// Authenticate checks the request's client certificate (design §18.4): chained to the
// cluster CA and unexpired (tls checked both at the handshake), naming an approved
// machine whose current certificate it is, not revoked. It returns the machine and the
// serial.
func (s *Server) Authenticate(r *http.Request) (string, string, error) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 {
		return "", "", ErrNoCertificate
	}
	cert := r.TLS.PeerCertificates[0]
	id, err := s.CA.Verify(cert, s.now())
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrNoCertificate, err)
	}
	serial := ca.SerialHex(cert.SerialNumber)
	ctx := r.Context()
	if s.Approved != nil {
		ok, err := s.Approved(ctx, id)
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", ErrNotMember
		}
	}
	if s.Certs != nil {
		if revoked, err := s.Certs.Revoked(ctx, serial); err != nil {
			return "", "", err
		} else if revoked {
			return "", "", ErrRevokedCert
		}
		if cur, err := s.Certs.Current(ctx, id); err != nil {
			return "", "", err
		} else if cur != serial {
			return "", "", ErrStaleCert
		}
	}
	return id, serial, nil
}

func refuse(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "detail": detail, "title": code})
}

// ServeCACert is GET /api/cluster/ca.crt (public): the CA certificate, never its key.
func (s *Server) ServeCACert(w http.ResponseWriter, _ *http.Request) {
	if s.CA == nil {
		refuse(w, http.StatusNotFound, "no_ca", "this home has no cluster CA")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="maktaba-cluster-ca.crt"`)
	w.Header().Set("X-Maktaba-CA-SHA256", s.CA.SHA256)
	_, _ = w.Write(s.CA.CertPEM)
}

// ---------- the control channel ----------

// ctrl is one machine's control session on the home.
type ctrl struct {
	id     string
	node   string
	serial string
	remote string
	srv    *Server
	conn   *websocket.Conn
	out    *sender
	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	reqs map[uint32]*inflight

	closeOnce sync.Once
	code      websocket.StatusCode
	reason    string
}

func (c *ctrl) closeWith(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		c.code, c.reason = code, reason
		c.cancel()
		c.out.close()
		_ = c.conn.Close(code, reason)
	})
}

// event sends a control event.
func (c *ctrl) event(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > MaxPayload {
		return ErrFrameTooLarge
	}
	return c.out.send(c.ctx, Frame{Kind: KindEvent, Class: ClassControl, Payload: b})
}

// Hello is the first (text) message of a control session.
type Hello struct {
	T          string    `json:"t"`
	Session    string    `json:"session"`
	Mode       string    `json:"mode"`
	MaxStreams int       `json:"max_streams"`
	WebHTTPS   bool      `json:"web_https,omitempty"`
	At         time.Time `json:"at"`
}

// ServeLink is GET /api/cluster/link: a machine's control channel.
func (s *Server) ServeLink(w http.ResponseWriter, r *http.Request) {
	st := s.settings()
	if st.Mode == linkctx.ModeOff {
		refuse(w, http.StatusNotFound, "link_off", "the cluster link is off on this home")
		return
	}
	node, serial, err := s.Authenticate(r)
	if err != nil {
		s.log().Warn("link: control channel refused", "remote", r.RemoteAddr, "err", err, "event", "link_refused")
		refuse(w, http.StatusUnauthorized, "link_auth", err.Error())
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// authentication is the TLS client certificate, not ambient credentials, so a
		// page from another origin gains nothing (this skips the Origin check only)
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	c.SetReadLimit(HeadSize + MaxPayload + 64)
	ctx, cancel := context.WithCancel(context.Background())
	sess := &ctrl{id: newID(), node: node, serial: serial, remote: r.RemoteAddr, srv: s, conn: c, out: newSender(),
		ctx: ctx, cancel: cancel, reqs: map[uint32]*inflight{}}
	hello, _ := json.Marshal(Hello{T: "hello", Session: sess.id, Mode: st.Mode, MaxStreams: st.MaxStreams,
		WebHTTPS: st.WebHTTPS, At: s.now().UTC()})
	wctx, wcancel := context.WithTimeout(r.Context(), writeTimeout)
	err = c.Write(wctx, websocket.MessageText, hello)
	wcancel()
	if err != nil {
		cancel()
		_ = c.CloseNow()
		return
	}
	s.attach(sess)
	s.log().Info("link: control channel up", "node_id", node, "session", sess.id, "event", "link_up")
	go func() {
		if err := sess.out.writeLoop(c); err != nil {
			sess.closeWith(websocket.StatusGoingAway, "write failed")
		}
	}()
	go sess.keepalive()
	sess.readLoop()
	sess.closeWith(websocket.StatusNormalClosure, "")
	sess.failInflight()
	s.detach(sess)
	s.log().Info("link: control channel down", "node_id", node, "session", sess.id, "reason", sess.reason, "event", "link_down")
}

// keepalive pings every 15 s; three missed in a row close the channel.
func (c *ctrl) keepalive() {
	missed := 0
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(c.ctx, pingTimeout)
			err := c.conn.Ping(pctx)
			cancel()
			if err == nil {
				missed = 0
				continue
			}
			if c.ctx.Err() != nil {
				return
			}
			missed++
			if missed >= missedPings {
				c.closeWith(CloseTimeout, "timeout pings")
				return
			}
		}
	}
}

func (c *ctrl) readLoop() {
	for {
		typ, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			c.closeWith(websocket.StatusCode(4000), "protocol_error text frame")
			return
		}
		f, err := Decode(data)
		if err != nil {
			c.closeWith(websocket.StatusCode(4000), "protocol_error "+err.Error())
			return
		}
		c.dispatch(f)
	}
}

func (c *ctrl) dispatch(f Frame) {
	switch f.Kind {
	case KindEvent:
		c.onEvent(f.Payload)
	case KindReq:
		c.onReq(f)
	case KindChunk, KindEnd:
		c.mu.Lock()
		in := c.reqs[f.ID]
		c.mu.Unlock()
		if in != nil {
			in.body(f)
		}
	case KindCancel:
		c.mu.Lock()
		in := c.reqs[f.ID]
		c.mu.Unlock()
		if in != nil {
			in.cancel()
		}
	}
}

// eventHead is an event's discriminator and request id.
type eventHead struct {
	T  string `json:"t"`
	ID string `json:"id,omitempty"`
}

// StreamOpen is a machine's stream.open.
type StreamOpen struct {
	T        string `json:"t"`
	ID       string `json:"id"`
	Purpose  string `json:"purpose"`
	Resource string `json:"resource"`
}

// StreamTicket answers stream.open (and rides stream.offer).
type StreamTicket struct {
	T         string    `json:"t"`
	ID        string    `json:"id,omitempty"`
	Purpose   string    `json:"purpose,omitempty"`
	Resource  string    `json:"resource,omitempty"`
	Ticket    string    `json:"ticket,omitempty"`
	URL       string    `json:"url,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	Reason    string    `json:"reason,omitempty"`
}

// CertRenew is cert.renew (and cert.csr on the v2 session).
type CertRenew struct {
	T   string `json:"t"`
	ID  string `json:"id,omitempty"`
	CSR string `json:"csr"`
}

// CertIssued answers it.
type CertIssued struct {
	T        string    `json:"t"`
	ID       string    `json:"id,omitempty"`
	Cert     string    `json:"cert,omitempty"`
	CACert   string    `json:"ca_cert,omitempty"`
	CASHA256 string    `json:"ca_sha256,omitempty"`
	NotAfter time.Time `json:"not_after,omitzero"`
	Reason   string    `json:"reason,omitempty"`
}

func (c *ctrl) onEvent(b []byte) {
	var h eventHead
	if json.Unmarshal(b, &h) != nil {
		return
	}
	switch h.T {
	case "stream.open":
		var o StreamOpen
		if json.Unmarshal(b, &o) != nil {
			return
		}
		t, reason := c.srv.issue(c, o.Purpose, o.Resource, nil)
		if t == "" {
			_ = c.event(StreamTicket{T: "stream.refused", ID: o.ID, Reason: reason})
			return
		}
		_ = c.event(StreamTicket{T: "stream.ticket", ID: o.ID, Ticket: t, URL: PathStream,
			ExpiresAt: c.srv.now().Add(ticketLife).UTC()})
	case "ping":
		// the control channel's own round trip (control frames, no handler)
		_ = c.event(map[string]any{"t": "pong", "id": h.ID})
	case "cert.renew":
		var cr CertRenew
		if json.Unmarshal(b, &cr) != nil {
			return
		}
		go func() { _ = c.event(c.srv.Renew(c.ctx, c.node, cr)) }()
	case "cert.web":
		var cr CertRenew
		if json.Unmarshal(b, &cr) != nil {
			return
		}
		go func() {
			if c.srv.WebCert == nil {
				_ = c.event(CertIssued{T: "cert.refused", ID: cr.ID, Reason: "no web certificates"})
				return
			}
			host, _, err := net.SplitHostPort(c.remote)
			if err != nil {
				host = c.remote
			}
			cert, err := c.srv.WebCert(c.ctx, c.node, host, cr.CSR)
			if err != nil {
				_ = c.event(CertIssued{T: "cert.refused", ID: cr.ID, Reason: err.Error()})
				return
			}
			_ = c.event(CertIssued{T: "cert.issued", ID: cr.ID, Cert: cert, CACert: string(c.srv.CA.CertPEM),
				CASHA256: c.srv.CA.SHA256})
		}()
	}
}

// Renew signs a machine's new certificate from its CSR (cert.renew on the control
// channel, cert.csr on its v2 session).
func (s *Server) Renew(ctx context.Context, nodeID string, cr CertRenew) CertIssued {
	if s.CA == nil {
		return CertIssued{T: "cert.refused", ID: cr.ID, Reason: "no_ca"}
	}
	is, err := s.CA.SignNode(ctx, []byte(cr.CSR), nodeID, 0)
	if err != nil {
		s.log().Warn("link: certificate request refused", "node_id", nodeID, "err", err, "event", "link_cert_refused")
		return CertIssued{T: "cert.refused", ID: cr.ID, Reason: err.Error()}
	}
	s.log().Info("link: certificate issued", "node_id", nodeID, "serial", is.Serial, "not_after", is.NotAfter,
		"event", "link_cert_issued")
	return CertIssued{T: "cert.issued", ID: cr.ID, Cert: string(is.PEM), CACert: string(s.CA.CertPEM),
		CASHA256: s.CA.SHA256, NotAfter: is.NotAfter}
}

// ---------- sessions, streams and grace ----------

type nodeStreams struct {
	open  map[*streamConn]string // purpose
	grace *time.Timer
}

func (s *Server) node(id string) *nodeStreams {
	if s.nodes == nil {
		s.nodes = map[string]*nodeStreams{}
	}
	n := s.nodes[id]
	if n == nil {
		n = &nodeStreams{open: map[*streamConn]string{}}
		s.nodes[id] = n
	}
	return n
}

// attach makes sess the machine's live session: one before it is replaced, its open
// streams re-attach to the new one (their grace ends).
func (s *Server) attach(sess *ctrl) {
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]*ctrl{}
	}
	old := s.sessions[sess.node]
	s.sessions[sess.node] = sess
	n := s.node(sess.node)
	reattached := len(n.open)
	if n.grace != nil {
		n.grace.Stop()
		n.grace = nil
	}
	s.mu.Unlock()
	if old != nil {
		old.closeWith(CloseReplaced, "replaced")
	}
	if reattached > 0 {
		s.log().Info("link: open streams re-attached", "node_id", sess.node, "streams", reattached, "event", "link_reattached")
	}
	if s.OnSession != nil && old == nil {
		s.OnSession(sess.node, true)
	}
}

// detach ends sess: the machine's open streams keep running for the grace, then close
// unless a new session came.
func (s *Server) detach(sess *ctrl) {
	s.mu.Lock()
	if s.sessions[sess.node] != sess {
		s.mu.Unlock()
		return // replaced: the new session holds the streams
	}
	delete(s.sessions, sess.node)
	n := s.node(sess.node)
	grace := s.settings().StreamGrace
	if len(n.open) > 0 && sess.code != CloseRevoked {
		node := sess.node
		n.grace = time.AfterFunc(grace, func() { s.graceOver(node) })
	}
	s.mu.Unlock()
	if s.OnSession != nil {
		s.OnSession(sess.node, false)
	}
}

func (s *Server) graceOver(node string) {
	s.mu.Lock()
	if s.sessions[node] != nil {
		s.mu.Unlock()
		return
	}
	n := s.node(node)
	conns := make([]*streamConn, 0, len(n.open))
	for c := range n.open {
		conns = append(conns, c)
	}
	n.grace = nil
	s.mu.Unlock()
	for _, c := range conns {
		c.closeNow(CloseGrace, "grace over")
	}
	if len(conns) > 0 {
		s.log().Info("link: open streams closed (no control session within the grace)", "node_id", node,
			"streams", len(conns), "event", "link_grace_over")
	}
}

// Revoke closes a machine's control channel (4010) and every stream at once (no grace)
// and forgets its tickets (design §18.6). The caller revokes its certificates.
func (s *Server) Revoke(node string) {
	s.mu.Lock()
	sess := s.sessions[node]
	delete(s.sessions, node)
	var conns []*streamConn
	if n := s.nodes[node]; n != nil {
		for c := range n.open {
			conns = append(conns, c)
		}
		if n.grace != nil {
			n.grace.Stop()
		}
		delete(s.nodes, node)
	}
	for k, t := range s.tickets {
		if t.node == node {
			delete(s.tickets, k)
		}
	}
	s.mu.Unlock()
	if sess != nil {
		sess.closeWith(CloseRevoked, "revoked")
	}
	for _, c := range conns {
		c.closeNow(CloseRevoked, "revoked")
	}
	s.log().Info("link: machine revoked: its link closed", "node_id", node, "streams", len(conns), "event", "link_revoked")
}

// Live reports whether a machine holds a control session.
func (s *Server) Live(node string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[node] != nil
}

// LiveNodes are the machines holding a control session.
func (s *Server) LiveNodes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		out = append(out, id)
	}
	return out
}

// ---------- tickets ----------

type ticket struct {
	node     string
	serial   string
	session  string
	purpose  string
	resource string
	expires  time.Time
	used     bool
	// deliver hands the stream to the puller that asked for it (stream.offer)
	deliver chan *streamConn
	// body is the rest of a large answer (svc_body), taken closed when the stream opens;
	// cut closes when its request is cancelled before the answer ended: the stream is cut
	// at once, even while a write to a machine that stopped reading is blocked
	body  io.Reader
	cut   <-chan struct{}
	taken chan struct{}
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// issueBody makes the svc_body ticket of a large answer's rest.
func (s *Server) issueBody(sess *ctrl, cut <-chan struct{}, body io.Reader) (string, chan struct{}, string) {
	taken := make(chan struct{})
	tok, reason := s.mint(sess, &ticket{purpose: PurposeSvcBody, body: body, cut: cut, taken: taken})
	return tok, taken, reason
}

// issue makes a ticket for a stream of sess's machine: "" and the reason when refused
// (no live session, a purpose or resource not allowed, the stream limit).
func (s *Server) issue(sess *ctrl, purpose, resource string, deliver chan *streamConn) (string, string) {
	if !allowed(purpose, resource) {
		return "", "purpose"
	}
	return s.mint(sess, &ticket{purpose: purpose, resource: resource, deliver: deliver})
}

// mint fills t for sess's machine and keeps it.
func (s *Server) mint(sess *ctrl, t *ticket) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[sess.node] != sess {
		return "", "no_session"
	}
	now := s.now()
	pending := 0
	for k, t := range s.tickets {
		if now.After(t.expires) {
			delete(s.tickets, k)
			continue
		}
		if t.node == sess.node && !t.used {
			pending++
		}
	}
	if len(s.node(sess.node).open)+pending >= s.settings().MaxStreams {
		return "", "limit"
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "internal"
	}
	if s.tickets == nil {
		s.tickets = map[[32]byte]*ticket{}
	}
	t.node, t.serial, t.session, t.expires = sess.node, sess.serial, sess.id, now.Add(ticketLife)
	s.tickets[sha256.Sum256(raw)] = t
	return base64.RawURLEncoding.EncodeToString(raw), ""
}

func allowed(purpose, resource string) bool {
	if homeAsked[purpose] {
		return true
	}
	if purpose == PurposeSvcBody {
		// a machine's request too large for the control channel: any route it could
		// send there
		return strings.HasPrefix(resource, "/") && !strings.Contains(resource, "..")
	}
	prefixes, ok := homeServed[purpose]
	if !ok {
		return false
	}
	if strings.Contains(resource, "..") {
		return false
	}
	for _, p := range prefixes {
		if resource == strings.TrimSuffix(p, "/") || strings.HasPrefix(resource, p) {
			return true
		}
	}
	return false
}

// Errors of a stream's ticket.
var (
	ErrTicketUnknown = errors.New("link: unknown ticket")
	ErrTicketReused  = errors.New("link: ticket used already")
	ErrTicketExpired = errors.New("link: ticket expired")
	ErrTicketForeign = errors.New("link: ticket issued to another machine")
	ErrNoSession     = errors.New("link: no live control session")
)

// redeem uses a ticket once for node's certificate serial.
func (s *Server) redeem(tok, node, serial string) (*ticket, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(tok))
	if err != nil || len(raw) != 32 {
		return nil, ErrTicketUnknown
	}
	h := sha256.Sum256(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tickets[h]
	switch {
	case t == nil:
		return nil, ErrTicketUnknown
	case t.used:
		s.reused(node, "a ticket presented twice")
		return nil, ErrTicketReused
	case t.node != node || t.serial != serial:
		s.reused(node, "a ticket presented by another machine")
		return nil, ErrTicketForeign
	case s.now().After(t.expires):
		delete(s.tickets, h)
		return nil, ErrTicketExpired
	case s.sessions[node] == nil:
		return nil, ErrNoSession
	}
	// kept, used, until it expires: a second presentation is a reuse (logged), not an
	// unknown ticket
	t.used = true
	if t.taken != nil {
		close(t.taken)
	}
	return t, nil
}

// reused logs a ticket presented twice (or by another machine) and alerts after three in
// ten minutes; s.mu is held.
func (s *Server) reused(node, detail string) {
	now := s.now()
	keep := s.reuse[:0]
	for _, at := range s.reuse {
		if now.Sub(at) < 10*time.Minute {
			keep = append(keep, at)
		}
	}
	s.reuse = append(keep, now)
	s.log().Warn("link: stream ticket reused", "node_id", node, "detail", detail, "event", "link_ticket_reuse")
	if len(s.reuse) >= 3 && s.Alert != nil {
		n := len(s.reuse)
		go s.Alert(node, fmt.Sprintf("%d stream tickets presented twice or by another machine in 10 minutes", n))
		s.reuse = s.reuse[:0]
	}
}

// ServeStream is GET /api/cluster/link/stream: a data stream, opened with a ticket.
func (s *Server) ServeStream(w http.ResponseWriter, r *http.Request) {
	node, serial, err := s.Authenticate(r)
	if err != nil {
		refuse(w, http.StatusUnauthorized, "link_auth", err.Error())
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, TicketScheme+" ") {
		refuse(w, http.StatusUnauthorized, "link_ticket", "a stream needs a ticket")
		return
	}
	t, err := s.redeem(strings.TrimPrefix(auth, TicketScheme+" "), node, serial)
	if err != nil {
		s.log().Warn("link: stream refused", "node_id", node, "err", err, "event", "link_stream_refused")
		refuse(w, http.StatusForbidden, "link_ticket", err.Error())
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true,
		CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	var remote net.Addr
	if ap, err := net.ResolveTCPAddr("tcp", r.RemoteAddr); err == nil {
		remote = ap
	}
	sc := newStreamConn(c, remote)
	s.mu.Lock()
	revoked := s.sessions[node] == nil && s.nodes[node] == nil
	n := s.node(node)
	n.open[sc] = t.purpose
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if n := s.nodes[node]; n != nil {
			delete(n.open, sc)
		}
		s.mu.Unlock()
		_ = sc.Close()
	}()
	if revoked {
		sc.closeNow(CloseRevoked, "revoked")
		return
	}
	switch {
	case t.deliver != nil:
		select {
		case t.deliver <- sc:
			<-sc.Done()
		default:
			sc.closeNow(websocket.StatusNormalClosure, "nobody waits")
		}
	case t.body != nil:
		if t.cut != nil {
			// the request cancelled: the stream ends now, not when a blocked write returns
			copied := make(chan struct{})
			defer close(copied)
			go func() {
				select {
				case <-t.cut:
					sc.closeNow(websocket.StatusInternalError, "the answer was cut")
				case <-copied:
				}
			}()
		}
		if _, err := io.Copy(sc, t.body); err != nil {
			// the answer was cut (its request ended, or the machine went): the stream ends as
			// an error, never as the body's end a reader would take for a complete answer
			sc.closeNow(websocket.StatusInternalError, "the answer was cut")
			return
		}
		_ = sc.Close()
	default:
		h := s.Handler
		if th := s.Targets[t.purpose]; th != nil {
			h = th
		}
		if h == nil {
			sc.closeNow(websocket.StatusInternalError, "nothing serves it")
			return
		}
		serveOne(sc, onlyResource(t.purpose, t.resource, h), func(ctx context.Context) context.Context {
			return linkctx.WithNode(ctx, node)
		})
	}
}

// onlyResource answers only the ticket's resource on a stream.
func onlyResource(purpose, resource string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != resource && r.URL.Path != resource {
			refuse(w, http.StatusForbidden, "link_resource", "the stream's ticket is for another resource")
			return
		}
		if purpose != PurposeWSProxy && purpose != PurposeSvcBody && r.Method != http.MethodGet && r.Method != http.MethodHead {
			refuse(w, http.StatusMethodNotAllowed, "link_method", "a stream reads")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ---------- the home asks a machine for bytes (stream.offer) ----------

// ErrNoLink: the machine holds no control session.
var ErrNoLink = errors.New("link: the machine is not on the link")

// Pull asks a machine for a stream (stream.offer: tv_pull, relay_pull) and returns it
// once the machine dialled it (within 10 s). The caller writes an HTTP request on it and
// reads the answer, then closes it.
func (s *Server) Pull(ctx context.Context, node, purpose, resource string) (net.Conn, error) {
	if !homeAsked[purpose] {
		return nil, fmt.Errorf("link: %s is not a purpose the home asks for", purpose)
	}
	s.mu.Lock()
	sess := s.sessions[node]
	s.mu.Unlock()
	if sess == nil {
		return nil, ErrNoLink
	}
	deliver := make(chan *streamConn)
	tok, reason := s.issue(sess, purpose, resource, deliver)
	if tok == "" {
		return nil, fmt.Errorf("link: stream refused: %s", reason)
	}
	if err := sess.event(StreamTicket{T: "stream.offer", Purpose: purpose, Resource: resource, Ticket: tok,
		URL: PathStream, ExpiresAt: s.now().Add(ticketLife).UTC()}); err != nil {
		return nil, err
	}
	wait := time.NewTimer(offerWait)
	defer wait.Stop()
	select {
	case c := <-deliver:
		return c, nil
	case <-wait.C:
		return nil, fmt.Errorf("link: %s did not open the %s stream", node, purpose)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PullHTTP sends req over a pulled stream and returns the answer (its body reads the
// stream; closing it closes the stream).
func (s *Server) PullHTTP(ctx context.Context, node, purpose string, req *http.Request) (*http.Response, error) {
	c, err := s.Pull(ctx, node, purpose, req.URL.RequestURI())
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	if err := req.Write(c); err != nil {
		stop()
		_ = c.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReaderSize(c, 32<<10), req)
	if err != nil {
		stop()
		_ = c.Close()
		return nil, err
	}
	resp.Body = &closeBoth{ReadCloser: resp.Body, c: c, stop: stop}
	return resp, nil
}

type closeBoth struct {
	io.ReadCloser
	c    io.Closer
	stop func() bool
}

// Close closes the stream first: the HTTP body's Close drains what is left, which on a
// stream that goes on (a TV pull, a slow file) would never end.
func (b *closeBoth) Close() error {
	b.stop()
	_ = b.c.Close()
	_ = b.ReadCloser.Close()
	return nil
}
