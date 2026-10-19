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
)

const (
	statusGoingAway       = websocket.StatusGoingAway
	statusPolicyViolation = websocket.StatusPolicyViolation
	statusTryAgainLater   = websocket.StatusTryAgainLater
	statusNormal          = websocket.StatusNormalClosure

	maxAgentFrame = 16 << 20
	maxTextFrame  = 8 << 10
)

// Refusal reasons (the close reason of a refused handshake).
const (
	RefuseBadHello     = "bad_hello"
	RefuseVersion      = "unsupported_version"
	RefuseUnknownNode  = "unknown_node"
	RefuseNotApproved  = "not_approved"
	RefuseRevoked      = "revoked"
	RefuseNoHomeKey    = "home_key_unavailable"
	RefuseBadProof     = "bad_proof"
	RefuseTimeout      = "handshake_timeout"
	RefuseStoreFailure = "registry_unavailable"
)

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
	ConnectedAt time.Time       `json:"connected_at"`
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
	PID         int
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
		WorkerID: s.WorkerID, PID: s.PID, ConnectedAt: s.ConnectedAt, LogLevel: s.logLevel, Metrics: s.metrics,
	}
	if s.runtimes != nil {
		p.LLM = &ProcessLLM{Runtimes: s.runtimes}
	}
	return p
}

// send seals and queues a frame; a session that cannot keep up is closed.
func (s *Session) send(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	select {
	case <-s.ctx.Done():
		return false
	default:
	}
	select {
	case s.out <- outFrame{payload: b}:
		return true
	default:
		s.closeWith(statusTryAgainLater, "slow_consumer")
		return false
	}
}

// sendThenClose queues a frame and a close behind it (the writer sends
// the frame first); when the queue is full it closes at once.
func (s *Session) sendThenClose(v any, code websocket.StatusCode, reason string) {
	b, err := json.Marshal(v)
	if err == nil {
		select {
		case s.out <- outFrame{payload: b}:
			select {
			case s.out <- outFrame{close: true, closeCode: code, closeReason: reason}:
				// let the writer flush, but never wait forever
				time.AfterFunc(5*time.Second, func() { s.closeWith(code, reason) })
				return
			default:
			}
		default:
		}
	}
	s.closeWith(code, reason)
}

func (s *Session) closeWith(code websocket.StatusCode, reason string) {
	s.closeOnce.Do(func() {
		s.closeCode, s.closeReason = code, reason
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
	PID      int    `json:"pid"`
	Host     string `json:"host"`
	Nonce    string `json:"nonce"`
}

type authFrame struct {
	T     string `json:"t"`
	Proof string `json:"proof"`
}

type refusal struct{ reason string }

func (r refusal) Error() string { return r.reason }

// ServeAgent implements GET /api/cluster/agent: the handshake of
// design-agent.md §2, then sealed frames until either side leaves.
func (h *Hub) ServeAgent(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// authentication is in the protocol (key possession), not ambient
		// credentials, so a cross-origin page gains nothing
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	c.SetReadLimit(maxTextFrame)
	remote := remoteIP(r)
	// a cancelled read would drop the connection without a close frame, so
	// the time limit closes it (with its reason) instead
	var timedOut atomic.Bool
	timer := time.AfterFunc(HandshakeTime, func() {
		timedOut.Store(true)
		_ = c.Close(statusPolicyViolation, RefuseTimeout)
	})
	s, c2s, s2c, err := h.handshake(r.Context(), c, remote)
	if !timer.Stop() || timedOut.Load() {
		if err == nil {
			s.closeWith(statusPolicyViolation, RefuseTimeout)
			h.unregister(s, RefuseTimeout)
		}
		return
	}
	if err != nil {
		reason := RefuseBadHello
		var rf refusal
		if errors.As(err, &rf) {
			reason = rf.reason
		}
		_ = c.Close(statusPolicyViolation, reason)
		return
	}
	h.runSession(r.Context(), c, s, c2s, s2c)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.Trim(host, "[]")
}

func readText(ctx context.Context, c *websocket.Conn, v any) error {
	typ, data, err := c.Read(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageText {
		return refusal{RefuseBadHello}
	}
	if err := json.Unmarshal(data, v); err != nil {
		return refusal{RefuseBadHello}
	}
	return nil
}

func writeText(ctx context.Context, c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, b)
}

// handshake authenticates the process and returns its registered session
// with the two directions' ciphers.
func (h *Hub) handshake(ctx context.Context, c *websocket.Conn, remote string) (*Session, *Cipher, *Cipher, error) {
	var hello helloFrame
	if err := readText(ctx, c, &hello); err != nil {
		return nil, nil, nil, err
	}
	ident := Process{Kind: hello.Kind, IP: remote, Host: clip(hello.Host, 255), WorkerID: clip(hello.WorkerID, 128), PID: hello.PID}
	refuse := func(reason string) (*Session, *Cipher, *Cipher, error) {
		h.refused(ident, reason)
		return nil, nil, nil, refusal{reason}
	}
	clientNonce, err := base64.StdEncoding.DecodeString(hello.Nonce)
	switch {
	case hello.T != "hello" || err != nil || len(clientNonce) != NonceSize ||
		(hello.Kind != "node" && hello.Kind != "home"):
		return refuse(RefuseBadHello)
	case hello.V != ProtocolVersion:
		return refuse(RefuseVersion)
	}

	serverNonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, serverNonce); err != nil {
		return nil, nil, nil, err
	}
	authAt := h.now()
	s := &Session{Kind: hello.Kind, IP: remote, Host: ident.Host, WorkerID: ident.WorkerID, PID: hello.PID}
	var shared []byte
	challenge := map[string]any{"t": "challenge", "nonce": base64.StdEncoding.EncodeToString(serverNonce)}
	if hello.Kind == "node" {
		u, err := uuid.Parse(hello.NodeID)
		if err != nil {
			return refuse(RefuseBadHello)
		}
		s.NodeID = u.String()
		ident.NodeID = &s.NodeID
		n, err := h.cfg.Store.Node(ctx, s.NodeID)
		switch {
		case errors.Is(err, cluster.ErrNotFound):
			return refuse(RefuseUnknownNode)
		case err != nil:
			h.log().Warn("cluster: agent handshake could not read the registry", "err", err, "event", "cluster_agent_store_failed")
			return refuse(RefuseStoreFailure)
		case n.State == cluster.StateRevoked:
			return refuse(RefuseRevoked)
		case n.State != cluster.StateApproved:
			return refuse(RefuseNotApproved)
		}
		name := n.Name
		s.NodeName, ident.NodeName = &name, &name
		nodePub, err := cluster.DecodeKey(n.NodePubkey)
		if err != nil {
			return refuse(RefuseNotApproved)
		}
		serverPriv, serverPub, err := cluster.GenerateKey()
		if err != nil {
			return nil, nil, nil, err
		}
		shared, err = cluster.SharedSecret(serverPriv, nodePub)
		if err != nil || allZero(shared) {
			return refuse(RefuseNotApproved)
		}
		challenge["server_pub"] = base64.StdEncoding.EncodeToString(serverPub)
	} else {
		if len(h.cfg.HomeKey) != KeySize {
			return refuse(RefuseNoHomeKey)
		}
		shared = h.cfg.HomeKey
		// the home's own processes are filed under the home's address
		if ip := net.ParseIP(remote); ip == nil || isLocalAddress(ip) {
			s.IP = h.homeIP
			ident.IP = h.homeIP
		}
	}
	if err := writeText(ctx, c, challenge); err != nil {
		return nil, nil, nil, err
	}
	kc2s, ks2c, err := DeriveKeys(shared, clientNonce, serverNonce)
	if err != nil {
		return nil, nil, nil, err
	}
	var auth authFrame
	if err := readText(ctx, c, &auth); err != nil {
		var rf refusal
		if errors.As(err, &rf) {
			return refuse(rf.reason)
		}
		return nil, nil, nil, err
	}
	proof, err := base64.StdEncoding.DecodeString(auth.Proof)
	if auth.T != "auth" || err != nil || !CheckProof(kc2s, serverNonce, proof) {
		return refuse(RefuseBadProof)
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
		"settings": map[string]any{"log_level": h.cfg.LogLevel, "metrics_every": h.cfg.MetricsEvery}}
	b, _ := json.Marshal(welcome)
	sealed, err := s2c.Seal(b)
	if err != nil {
		return nil, nil, nil, err
	}
	if !h.register(s, authAt) {
		s.cancel()
		return refuse(RefuseRevoked)
	}
	if err := c.Write(ctx, websocket.MessageBinary, sealed); err != nil {
		s.cancel()
		h.unregister(s, "handshake write failed")
		return nil, nil, nil, err
	}
	return s, c2s, s2c, nil
}

func allZero(b []byte) bool {
	var acc byte
	for _, x := range b {
		acc |= x
	}
	return acc == 0
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
	c.SetReadLimit(maxAgentFrame)
	stop := context.AfterFunc(reqCtx, func() { s.closeWith(statusGoingAway, "request ended") })
	defer stop()

	// the reader's context is not the session's: cancelling a read closes
	// the connection without a close frame, and the agent must learn why
	readCtx, readCancel := context.WithCancel(context.Background())
	defer readCancel()
	lastRecv := make(chan struct{}, 1)
	readDone := make(chan string, 1)
	go func() { readDone <- h.readLoop(readCtx, s, c, c2s, lastRecv) }()

	reason := h.writeLoop(s, c, s2c, lastRecv, readDone)
	s.closeWith(statusNormal, reason)
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
		wctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
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
			code, reason := s.closeCode, s.closeReason
			if code == 0 {
				code, reason = statusNormal, "closed"
			}
			return closeConn(code, reason)
		case f := <-s.out:
			if f.close {
				s.closeWith(f.closeCode, f.closeReason)
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
			return closeConn(statusPolicyViolation, "timeout")
		case <-ping.C:
			if err := write([]byte(`{"t":"ping"}`)); err != nil {
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
			if websocket.CloseStatus(err) != -1 {
				return "agent closed"
			}
			if errors.Is(err, io.EOF) {
				return "agent closed"
			}
			return "read failed"
		}
		if typ != websocket.MessageBinary {
			s.closeWith(statusPolicyViolation, "unsealed frame")
			return "unsealed frame"
		}
		pt, err := c2s.Open(data)
		if err != nil {
			s.closeWith(statusPolicyViolation, "bad frame")
			return "bad frame"
		}
		select {
		case lastRecv <- struct{}{}:
		default:
		}
		var head frameHead
		if err := json.Unmarshal(pt, &head); err != nil {
			s.closeWith(statusPolicyViolation, "bad frame")
			return "bad frame"
		}
		h.dispatch(s, head.T, pt)
	}
}

// dispatch handles one opened frame (design-agent.md §3). Unknown types
// are ignored (a newer agent may send more).
func (h *Hub) dispatch(s *Session, t string, raw []byte) {
	switch t {
	case "ping":
		s.send(map[string]any{"t": "pong"})
	case "pong":
	case "metrics":
		m := stripT(raw)
		s.mu.Lock()
		s.metrics = m
		s.mu.Unlock()
		h.onMetrics(s, m)
	case "logs":
		var f struct {
			Records []json.RawMessage `json:"records"`
		}
		if json.Unmarshal(raw, &f) == nil {
			h.onLogs(s, f.Records)
		}
	case "job":
		h.onJob(s, stripT(raw))
	case "dropped":
		h.event(map[string]any{"event": "agent_dropped", "session": s.ID, "ip": s.IP, "worker_id": s.WorkerID,
			"counts": stripT(raw)})
	case "llm.models":
		var f struct {
			Runtimes json.RawMessage `json:"runtimes"`
		}
		if json.Unmarshal(raw, &f) != nil {
			return
		}
		if len(f.Runtimes) == 0 || string(f.Runtimes) == "null" {
			f.Runtimes = json.RawMessage("[]")
		}
		s.mu.Lock()
		s.runtimes = f.Runtimes
		s.mu.Unlock()
		h.router.UpdateHost(s.ID, s.IP, s.ConnectedAt, f.Runtimes)
		h.publish(TopicProcesses, "process", processEvent{Event: "updated", Session: &s.ID, Process: ptr(s.view())})
	case "llm.request":
		var f LLMRequestFrame
		if json.Unmarshal(raw, &f) != nil {
			return
		}
		h.router.Submit(&LLMRequest{
			ClientID: f.ID, Model: f.Model, Op: f.Op, Body: f.Body, Stream: f.Stream,
			FromSession: s.ID, FromIP: s.IP, Sink: &sessionSink{s: s, id: f.ID},
		})
	case "llm.chunk":
		var f struct {
			ID   string          `json:"id"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &f) == nil {
			h.router.Chunk(s.ID, f.ID, f.Data)
		}
	case "llm.done":
		var f struct {
			ID    string          `json:"id"`
			Usage json.RawMessage `json:"usage"`
			Error *string         `json:"error"`
		}
		if json.Unmarshal(raw, &f) == nil {
			errCode := ""
			if f.Error != nil {
				errCode = *f.Error
			}
			h.router.Done(s.ID, f.ID, f.Usage, errCode)
		}
	case "cmd.result":
		var f CmdResult
		if json.Unmarshal(raw, &f) == nil {
			s.deliverCmd(f)
		}
	}
}

// stripT drops the "t" key of a frame (what is left is its content).
func stripT(raw []byte) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return json.RawMessage(raw)
	}
	delete(m, "t")
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(raw)
	}
	return b
}

func (h *Hub) onLogs(s *Session, records []json.RawMessage) {
	if len(records) == 0 {
		return
	}
	msgs := make([]logMsg, 0, len(records))
	for _, rec := range records {
		var lv struct {
			Level string `json:"level"`
		}
		_ = json.Unmarshal(rec, &lv)
		rank := levelRank(lv.Level)
		if rank == 0 {
			rank = levelRank("info")
		}
		data, err := json.Marshal(map[string]any{"session": s.ID, "ip": s.IP, "record": rec})
		if err != nil {
			continue
		}
		msgs = append(msgs, logMsg{rank: rank, data: data})
	}
	s.mu.Lock()
	s.logs = append(s.logs, msgs...)
	if len(s.logs) > sessionLogs {
		s.logs = append([]logMsg(nil), s.logs[len(s.logs)-sessionLogs:]...)
	}
	s.mu.Unlock()
	h.publishLogs(s, msgs)
}

type logMsg struct {
	rank int
	data json.RawMessage
}
