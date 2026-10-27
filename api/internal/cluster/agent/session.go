package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/linkctx"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// Close codes of design-agent.md §4.6.
const (
	CloseNormal             = websocket.StatusNormalClosure // 1000
	CloseGoingAway          = websocket.StatusGoingAway     // 1001
	CloseSlow               = websocket.StatusTryAgainLater // 1013: the process cannot keep up
	CloseProtocolError      = websocket.StatusCode(4000)
	CloseUnsupportedVersion = websocket.StatusCode(4001)
	CloseNotMember          = websocket.StatusCode(4003)
	CloseAuthFailed         = websocket.StatusCode(4004)
	CloseTimeout            = websocket.StatusCode(4008)
	CloseReplaced           = websocket.StatusCode(4009)
	CloseRevoked            = websocket.StatusCode(4010)
	CloseSealError          = websocket.StatusCode(4011)
	CloseTooLarge           = websocket.StatusCode(4012)
	CloseRateLimited        = websocket.StatusCode(4013)
	CloseDisconnect         = websocket.StatusCode(4014)

	// MaxFrame is the largest sealed frame (§4.7); the transport limit is
	// above it so an oversized frame gets 4012 rather than a bare 1009.
	MaxFrame      = 4 << 20
	maxWSMessage  = 8 << 20
	maxTextFrame  = 8 << 10
	maxLogRecords = 1000
)

// closeNames are the close reasons' names.
var closeNames = map[websocket.StatusCode]string{
	CloseNormal: "normal", CloseGoingAway: "going_away", CloseSlow: "slow_consumer",
	CloseProtocolError: "protocol_error", CloseUnsupportedVersion: "unsupported_version",
	CloseNotMember: "not_member", CloseAuthFailed: "auth_failed", CloseTimeout: "timeout",
	CloseReplaced: "replaced", CloseRevoked: "revoked", CloseSealError: "seal_error",
	CloseTooLarge: "too_large", CloseRateLimited: "rate_limited", CloseDisconnect: "disconnect",
}

// closeReason is "<name>[ <detail>]", at most 123 bytes.
func closeReason(code websocket.StatusCode, detail string) string {
	r := closeNames[code]
	if detail != "" {
		r += " " + detail
	}
	if len(r) > 123 {
		r = r[:123]
	}
	return r
}

// Process is a connected process as browsers see it.
type Process struct {
	Session     string          `json:"session"`
	Kind        string          `json:"kind"`
	NodeID      *string         `json:"node_id"`
	NodeName    *string         `json:"node_name"`
	IP          string          `json:"ip"`
	Host        string          `json:"host"`
	WorkerID    string          `json:"worker_id"`
	PID         int             `json:"pid"`
	OS          *string         `json:"os"`
	Version     *string         `json:"version"`
	ConnectedAt time.Time       `json:"connected_at"`
	GoneAt      *time.Time      `json:"gone_at,omitempty"`
	LogLevel    string          `json:"log_level"`
	Metrics     json.RawMessage `json:"metrics"`
	LLM         *ProcessLLM     `json:"llm"`
}

// ProcessLLM is a process's LLM runtimes (its last llm.models).
type ProcessLLM struct {
	Runtimes json.RawMessage `json:"runtimes"`
}

type processEvent struct {
	Event   string   `json:"event"`
	Session *string  `json:"session"`
	Process *Process `json:"process"`
	Reason  *string  `json:"reason"`
}

// Session is one authenticated process connection.
type Session struct {
	ID          string
	Kind        string // "home" | "node"
	NodeID      string // "" for a home process
	NodeName    *string
	IP          string
	Host        string
	WorkerID    string
	Process     string // "supervisor": a node's machine agent (Epic 35)
	PID         int
	OS          string
	Version     string
	ConnectedAt time.Time

	hub    *Hub
	out    chan outFrame
	ctx    context.Context
	cancel context.CancelFunc

	closeOnce   sync.Once
	closeCode   websocket.StatusCode
	closeReason string

	mu       sync.Mutex
	metrics  json.RawMessage
	runtimes json.RawMessage
	logLevel string
	logs     []logMsg // recent log messages
	cmds     map[string]chan CmdResult
	relays   map[string]context.CancelFunc // svc.request ids running
	// updateState is a machine agent's last update.state (Epic 35)
	updateState json.RawMessage
	// viaLink: the session came over its machine's cluster link (story 35.16);
	// certOnly: a machine's session off the link while the link is required, which may
	// only renew its certificate (cert.csr)
	viaLink  bool
	certOnly bool
	// clockSkew is the machine's clock minus the home's, from its last update.state
	clockSkew time.Duration
	hasSkew   bool
	// dbCertSaid is what this machine agent last said of its processes' certificates on
	// the home's database (update.state's db_client_cert), dbCertKept what was kept of it
	// (Config.DBClientCert; nil: nothing yet); dbCertKeep makes the keeps one at a time
	dbCertSaid bool
	dbCertKept *bool
	dbCertKeep sync.Mutex
}

type outFrame struct {
	payload     []byte
	close       bool
	closeCode   websocket.StatusCode
	closeReason string
}

func (s *Session) view() Process {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := Process{
		Session: s.ID, Kind: s.Kind, NodeID: nullable(s.NodeID), NodeName: s.NodeName, IP: s.IP, Host: s.Host,
		WorkerID: s.WorkerID, PID: s.PID, OS: nullable(s.OS), Version: nullable(s.Version),
		ConnectedAt: s.ConnectedAt, LogLevel: s.logLevel, Metrics: s.metrics,
	}
	if s.runtimes != nil {
		p.LLM = &ProcessLLM{Runtimes: s.runtimes}
	}
	return p
}

// Send errors.
var (
	// ErrFrameTooLarge: the frame would seal to more than MaxFrame; it is
	// not sent, and the session stays open.
	ErrFrameTooLarge = errors.New("agent: frame over the size limit")
	// ErrGone: the session is closed (or closing).
	ErrGone = errors.New("agent: session gone")
)

// sealOverhead is the Poly1305 tag a sealed frame adds.
const sealOverhead = 16

// fits reports whether a plaintext frame of n bytes seals within MaxFrame.
func fits(n int) bool { return n+sealOverhead <= MaxFrame }

// sendErr seals and queues a frame. A frame too large for the channel is
// refused (ErrFrameTooLarge) without touching the session; a session that
// cannot keep up is closed (ErrGone).
func (s *Session) sendErr(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if !fits(len(b)) {
		return ErrFrameTooLarge
	}
	select {
	case <-s.ctx.Done():
		return ErrGone
	default:
	}
	select {
	case s.out <- outFrame{payload: b}:
		return nil
	default:
		s.closeWith(CloseSlow, "")
		return ErrGone
	}
}

// send is sendErr reporting only success.
func (s *Session) send(v any) bool { return s.sendErr(v) == nil }

// sendThenClose queues a frame and a close behind it (the writer sends
// the frame first); when the queue is full it closes at once.
func (s *Session) sendThenClose(v any, code websocket.StatusCode, detail string) {
	b, err := json.Marshal(v)
	if err == nil {
		select {
		case s.out <- outFrame{payload: b}:
			select {
			case s.out <- outFrame{close: true, closeCode: code, closeReason: closeReason(code, detail)}:
				// let the writer flush, but never wait forever
				time.AfterFunc(5*time.Second, func() { s.closeWith(code, detail) })
				return
			default:
			}
		default:
		}
	}
	s.closeWith(code, detail)
}

func (s *Session) closeWith(code websocket.StatusCode, detail string) {
	s.closeOnce.Do(func() {
		s.closeCode, s.closeReason = code, closeReason(code, detail)
		s.cancel()
	})
}

// ---------- the endpoint ----------

type helloFrame struct {
	T        string `json:"t"`
	V        int    `json:"v"`
	Kind     string `json:"kind"`
	NodeID   string `json:"node_id"`
	WorkerID string `json:"worker_id"`
	// Process names a process kind beyond the pipeline's ("supervisor": a node's
	// machine agent, Epic 35); empty for the pipeline.
	Process string `json:"process"`
	PID     int    `json:"pid"`
	Host    string `json:"host"`
	Eph     string `json:"eph"`
	Nonce   string `json:"nonce"`
	IP      string `json:"ip"`
	OS      string `json:"os"`
	Version string `json:"version"`
}

type authFrame struct {
	T     string `json:"t"`
	Proof string `json:"proof"`
}

// refusal ends a handshake with a close code.
type refusal struct {
	code   websocket.StatusCode
	detail string
}

func (r refusal) Error() string { return closeReason(r.code, r.detail) }

// ServeAgent implements GET /api/cluster/agent: the handshake of
// design-agent.md §4.2/§4.3, then sealed frames until either side leaves.
func (h *Hub) ServeAgent(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// authentication is in the protocol (key possession), not ambient
		// credentials, so a cross-origin page gains nothing (this skips the
		// Origin check only; it has nothing to do with TLS)
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	c.SetReadLimit(maxTextFrame)
	remote := remoteIP(r)
	if !h.limiter.allow(remote, h.now()) {
		h.refused(Process{IP: remote}, closeReason(CloseRateLimited, ""))
		_ = c.Close(CloseRateLimited, closeReason(CloseRateLimited, ""))
		return
	}
	// a cancelled read would drop the connection without a close frame, so
	// the time limit closes it (with its code) instead
	var timedOut atomic.Bool
	timer := time.AfterFunc(HandshakeTime, func() {
		timedOut.Store(true)
		_ = c.Close(CloseTimeout, closeReason(CloseTimeout, "handshake"))
	})
	linkNode, viaLink := linkctx.Node(r.Context())
	s, c2s, s2c, err := h.handshake(r.Context(), c, remote, linkNode, viaLink)
	if !timer.Stop() || timedOut.Load() {
		if err == nil {
			s.closeWith(CloseTimeout, "handshake")
			h.unregister(s, closeReason(CloseTimeout, "handshake"))
		}
		return
	}
	if err != nil {
		rf := refusal{code: CloseProtocolError}
		if !errors.As(err, &rf) {
			rf = refusal{code: CloseProtocolError, detail: "handshake"}
		}
		_ = c.Close(rf.code, rf.Error())
		return
	}
	h.afterWelcome(s)
	h.runSession(r.Context(), c, s, c2s, s2c)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.Trim(host, "[]")
}

// readText reads one text frame; it returns the exact bytes.
func readText(ctx context.Context, c *websocket.Conn, v any) ([]byte, error) {
	typ, data, err := c.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, refusal{code: CloseProtocolError, detail: "text frame expected"}
	}
	if err := json.Unmarshal(data, v); err != nil {
		return nil, refusal{code: CloseProtocolError, detail: "malformed JSON"}
	}
	return data, nil
}

// writeText writes v as one text frame and returns the exact bytes.
func writeText(ctx context.Context, c *websocket.Conn, v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, c.Write(ctx, websocket.MessageText, b)
}

// folderIP is the address a process is filed under (and told in
// welcome.settings.ip): a home process always shares the home's own folder,
// the one the hub's files use; a machine's process the address it reports
// in hello (informational, but authenticated by the handshake) when valid,
// else the connection's remote address.
func (h *Hub) folderIP(kind, reported, remote string) string {
	if kind == "home" {
		return h.homeIP
	}
	if reported != "" && len(reported) <= 64 && (net.ParseIP(reported) != nil || ValidIPDir(reported)) {
		return reported
	}
	return remote
}

// handshake authenticates the process (both ways) and returns its
// registered session with the two directions' ciphers.
func (h *Hub) handshake(ctx context.Context, c *websocket.Conn, remote, linkNode string, viaLink bool) (*Session, *Cipher, *Cipher, error) {
	var hello helloFrame
	helloBytes, err := readText(ctx, c, &hello)
	if err != nil {
		return nil, nil, nil, err
	}
	ident := Process{Kind: hello.Kind, IP: remote, Host: clip(hello.Host, 255), WorkerID: clip(hello.WorkerID, 128),
		PID: hello.PID}
	refuse := func(code websocket.StatusCode, detail string) (*Session, *Cipher, *Cipher, error) {
		rf := refusal{code: code, detail: detail}
		h.refused(ident, rf.Error())
		return nil, nil, nil, rf
	}
	if hello.T != "hello" {
		return refuse(CloseProtocolError, "hello expected")
	}
	if hello.V != ProtocolVersion {
		return refuse(CloseUnsupportedVersion, "2")
	}
	// over a machine's link the session is that machine's (its certificate named it): a
	// home process never comes that way, nor another machine's
	if viaLink && (hello.Kind != "node" || !strings.EqualFold(hello.NodeID, linkNode)) {
		return refuse(CloseNotMember, "not this link's machine")
	}
	clientNonce, err1 := base64.StdEncoding.DecodeString(hello.Nonce)
	clientEph, err2 := base64.StdEncoding.DecodeString(hello.Eph)
	if err1 != nil || err2 != nil || len(clientNonce) != NonceSize || len(clientEph) != KeySize ||
		(hello.Kind != "node" && hello.Kind != "home") || ident.WorkerID == "" {
		return refuse(CloseProtocolError, "bad hello")
	}

	authAt := h.now()
	s := &Session{Kind: hello.Kind, Host: ident.Host, WorkerID: ident.WorkerID, PID: hello.PID,
		OS: clip(hello.OS, 64), Version: clip(hello.Version, 64), Process: clip(hello.Process, 32), viaLink: viaLink}
	s.IP = h.folderIP(hello.Kind, hello.IP, remote)
	ident.IP = s.IP
	var nodePub []byte
	if hello.Kind == "node" {
		u, err := uuid.Parse(hello.NodeID)
		if err != nil {
			return refuse(CloseProtocolError, "bad node_id")
		}
		s.NodeID = u.String()
		ident.NodeID = &s.NodeID
		n, err := h.cfg.Store.Node(ctx, s.NodeID)
		switch {
		case errors.Is(err, cluster.ErrNotFound):
			return refuse(CloseNotMember, "")
		case err != nil:
			h.log().Warn("cluster: agent handshake could not read the registry", "err", err, "event", "cluster_agent_store_failed")
			return refuse(CloseGoingAway, "registry unavailable")
		case n.State != cluster.StateApproved:
			// unknown, pending, rejected and revoked look the same
			return refuse(CloseNotMember, "")
		}
		if !viaLink && linkctx.Require() && h.cfg.HasCert != nil && h.cfg.HasCert(ctx, s.NodeID) {
			// the link is required: a machine that holds a certificate renews it on a plain
			// session (an expired one cannot open the link) and does nothing else there; one
			// that never got one (a worker-only machine, an older release) keeps its channel
			s.certOnly, s.Process = true, "cert-only"
		}
		name := n.Name
		s.NodeName, ident.NodeName = &name, &name
		if nodePub, err = cluster.DecodeKey(n.NodePubkey); err != nil {
			return refuse(CloseNotMember, "")
		}
		if len(h.cfg.ClusterKey) != KeySize {
			return refuse(CloseAuthFailed, "the home has no cluster key")
		}
	} else if len(h.cfg.HomeKey) != KeySize {
		return refuse(CloseAuthFailed, "the home agent key is unavailable")
	}

	serverEphPriv, serverEphPub, err := cluster.GenerateKey()
	if err != nil {
		return nil, nil, nil, err
	}
	serverNonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, serverNonce); err != nil {
		return nil, nil, nil, err
	}
	challengeBytes, err := writeText(ctx, c, map[string]any{"t": "challenge",
		"server_eph": base64.StdEncoding.EncodeToString(serverEphPub),
		"nonce":      base64.StdEncoding.EncodeToString(serverNonce)})
	if err != nil {
		return nil, nil, nil, err
	}
	th := Transcript(helloBytes, challengeBytes)
	dhEE, err := DH(serverEphPriv, clientEph)
	if err != nil {
		return refuse(CloseAuthFailed, "low-order key")
	}
	var ikm []byte
	if hello.Kind == "node" {
		dhSE, err1 := DH(serverEphPriv, nodePub)
		dhES, err2 := DH(h.cfg.ClusterKey, clientEph)
		if err1 != nil || err2 != nil {
			return refuse(CloseAuthFailed, "low-order key")
		}
		ikm = NodeIKM(dhEE, dhSE, dhES)
	} else {
		ikm = HomeIKM(h.cfg.HomeKey, dhEE)
	}
	kc2s, ks2c, err := DeriveKeys(ikm, clientNonce, serverNonce)
	if err != nil {
		return nil, nil, nil, err
	}
	var auth authFrame
	if _, err := readText(ctx, c, &auth); err != nil {
		var rf refusal
		if errors.As(err, &rf) {
			return refuse(rf.code, rf.detail)
		}
		return nil, nil, nil, err
	}
	proof, err := base64.StdEncoding.DecodeString(auth.Proof)
	if auth.T != "auth" || err != nil || !CheckClientProof(kc2s, th, proof) {
		return refuse(CloseAuthFailed, "")
	}
	c2s, err := NewCipher(kc2s)
	if err != nil {
		return nil, nil, nil, err
	}
	s2c, err := NewCipher(ks2c)
	if err != nil {
		return nil, nil, nil, err
	}

	s.ID = newID()
	s.ConnectedAt = h.now()
	s.hub = h
	s.logLevel = h.cfg.LogLevel
	s.out = make(chan outFrame, sessionOutCap)
	s.cmds = map[string]chan CmdResult{}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	welcome := map[string]any{"t": "welcome", "session": s.ID,
		"proof":    base64.StdEncoding.EncodeToString(ServerProof(ks2c, th)),
		"settings": h.settings(s.IP)}
	b, _ := json.Marshal(welcome)
	sealed, err := s2c.Seal(b)
	if err != nil {
		return nil, nil, nil, err
	}
	if !h.register(s, authAt) {
		s.cancel()
		return refuse(CloseNotMember, "")
	}
	if err := c.Write(ctx, websocket.MessageBinary, sealed); err != nil {
		s.cancel()
		h.unregister(s, "handshake write failed")
		return nil, nil, nil, err
	}
	return s, c2s, s2c, nil
}

// settings is welcome.settings (§5.1). `ip` is the IP folder the hub files
// this session under (IPv6 with '-'), so a process writes its files where
// the hub looks for them: for a machine, hello.ip when valid, else its
// remote address; for a home process, the home's own folder (the one its
// hub.jsonl files use). Left out when empty.
func (h *Hub) settings(ip string) map[string]any {
	loc := h.Location()
	var shareRoot any
	if loc.ShareRoot != "" {
		shareRoot = loc.ShareRoot
	}
	out := map[string]any{"log_level": h.cfg.LogLevel, "metrics_every": h.cfg.MetricsEvery, "share_root": shareRoot,
		"retention_days": h.cfg.RetentionDays, "file_mb": h.cfg.FileMaxBytes >> 20}
	if dir := IPDir(ip); dir != "" {
		out["ip"] = dir
	}
	return out
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// runSession pumps frames until the connection ends.
func (h *Hub) runSession(reqCtx context.Context, c *websocket.Conn, s *Session, c2s, s2c *Cipher) {
	c.SetReadLimit(maxWSMessage)
	stop := context.AfterFunc(reqCtx, func() { s.closeWith(CloseGoingAway, "") })
	defer stop()

	// the reader's context is not the session's: cancelling a read closes
	// the connection without a close frame, and the agent must learn why
	readCtx, readCancel := context.WithCancel(context.Background())
	defer readCancel()
	lastRecv := make(chan struct{}, 1)
	readDone := make(chan string, 1)
	go func() {
		reason := "read_panic"
		defer func() { readDone <- reason }()
		defer safego.Recover(h.log(), "cluster_session_read")
		reason = h.readLoop(readCtx, s, c, c2s, lastRecv)
	}()

	reason := h.writeLoop(s, c, s2c, lastRecv, readDone)
	s.closeWith(CloseNormal, "")
	h.unregister(s, reason)
	s.failPendingCmds()
}

// writeLoop owns the sending side: sealed frames in order, pings every
// 15 s, the silence limit, and the close.
func (h *Hub) writeLoop(s *Session, c *websocket.Conn, s2c *Cipher, lastRecv <-chan struct{}, readDone <-chan string) string {
	ping := time.NewTicker(PingEvery)
	defer ping.Stop()
	silence := time.NewTimer(SilenceLimit)
	defer silence.Stop()
	write := func(payload []byte) error {
		sealed, err := s2c.Seal(payload)
		if err != nil {
			return err
		}
		// Not s.ctx: the websocket library hard-closes the connection when
		// a write's context ends, even after the frame is flushed and the
		// write is only returning. A session closing then (the reader hit a
		// bad frame just as a pong went out) would drop the socket without
		// the close frame that tells the agent why. This loop owns the
		// close; a write stuck on the peer still ends at the timeout.
		wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return c.Write(wctx, websocket.MessageBinary, sealed)
	}
	closeConn := func(code websocket.StatusCode, reason string) string {
		// leave the registry first: the close handshake may wait on the peer
		h.unregister(s, reason)
		_ = c.Close(code, reason)
		return reason
	}
	for {
		select {
		case reason := <-readDone:
			if s.ctx.Err() != nil && s.closeCode != 0 {
				return closeConn(s.closeCode, s.closeReason)
			}
			_ = c.CloseNow()
			return reason
		case <-s.ctx.Done():
			return closeConn(s.closeCode, s.closeReason)
		case f := <-s.out:
			if f.close {
				s.closeWith(f.closeCode, strings.TrimPrefix(strings.TrimPrefix(f.closeReason, closeNames[f.closeCode]), " "))
				return closeConn(f.closeCode, f.closeReason)
			}
			if err := write(f.payload); err != nil {
				_ = c.CloseNow()
				return "write failed"
			}
		case <-lastRecv:
			if !silence.Stop() {
				select {
				case <-silence.C:
				default:
				}
			}
			silence.Reset(SilenceLimit)
		case <-silence.C:
			s.closeWith(CloseTimeout, "silence")
			return closeConn(s.closeCode, s.closeReason)
		case <-ping.C:
			b, _ := json.Marshal(map[string]any{"t": "ping", "ts": h.now().Format(time.RFC3339Nano)})
			if err := write(b); err != nil {
				_ = c.CloseNow()
				return "write failed"
			}
		}
	}
}

type frameHead struct {
	T string `json:"t"`
}

// readLoop opens and dispatches frames; it returns why it stopped.
func (h *Hub) readLoop(ctx context.Context, s *Session, c *websocket.Conn, c2s *Cipher, lastRecv chan<- struct{}) string {
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				return "closed"
			}
			if st := websocket.CloseStatus(err); st != -1 {
				return "agent closed: " + closeNames[st]
			}
			if errors.Is(err, io.EOF) {
				return "agent closed"
			}
			if strings.Contains(err.Error(), "read limited") {
				s.closeWith(CloseTooLarge, "")
				return "too large"
			}
			return "read failed"
		}
		if typ != websocket.MessageBinary {
			s.closeWith(CloseProtocolError, "text after welcome")
			return "text after welcome"
		}
		if len(data) > MaxFrame {
			s.closeWith(CloseTooLarge, "")
			return "too large"
		}
		pt, err := c2s.Open(data)
		if err != nil {
			s.closeWith(CloseSealError, "")
			return "seal error"
		}
		select {
		case lastRecv <- struct{}{}:
		default:
		}
		var head frameHead
		if err := json.Unmarshal(pt, &head); err != nil || head.T == "" {
			s.closeWith(CloseProtocolError, "malformed frame")
			return "malformed frame"
		}
		h.dispatch(s, head.T, pt)
	}
}
