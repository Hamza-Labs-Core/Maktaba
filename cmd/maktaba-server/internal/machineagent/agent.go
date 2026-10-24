package machineagent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/curve25519"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/attempts"
)

// Close codes the agent treats as "wait the longest delay" (design-agent §4.6).
const (
	closeUnsupported = websocket.StatusCode(4001)
	closeNotMember   = websocket.StatusCode(4003)
	closeAuthFailed  = websocket.StatusCode(4004)
	closeReplaced    = websocket.StatusCode(4009)
	closeRevoked     = websocket.StatusCode(4010)
	closeRateLimited = websocket.StatusCode(4013)
)

// Timings of design-agent §4.5.
var (
	pingEvery      = 15 * time.Second
	silenceLimit   = 45 * time.Second
	handshakeMax   = 10 * time.Second
	backoffFloor   = time.Second
	backoffCeiling = 30 * time.Second
)

// Identity is what the agent connects as (from node.json; never logged).
type Identity struct {
	NodeID     string
	Node8      string
	PrivateKey string // X25519, standard base64
	ClusterPub string // the home's cluster key, standard base64
	HomeURL    string // the home's API, http(s)://host:port
	Host       string
}

// Key is a delegation key from the home.
type Key struct {
	ID       string    `json:"id"`
	Key      []byte    `json:"key"`
	NotAfter time.Time `json:"not_after"`
}

// Revoked is an auth.revoked frame's body.
type Revoked struct {
	SessionSHA256 []string `json:"session_sha256,omitempty"`
	UserID        string   `json:"user_id,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

// Event is what the agent hands to its local subscribers (the node's API).
type Event struct {
	Kind    string // keys | revoked
	Keys    *Key
	Revoked *Revoked
}

// UpdateState is the agent's update.state (story 35.8 AC5, 35.12 AC1).
type UpdateState struct {
	Version         string `json:"version"`
	PreparedVersion string `json:"prepared_version,omitempty"`
	Platform        string `json:"platform"`
	Layout          string `json:"layout"` // the install kind: layout | runtime | appbundle | flat
	// Run is this machine's update run, when it has one (its state and step).
	Run *RunState `json:"run,omitempty"`
	// Attempts are this machine's update attempts, every failure with its reason
	// (update-attempts.md), newest first: the home's Updates page shows them.
	Attempts []attempts.Attempt `json:"attempts,omitempty"`
}

// RunState is a machine's update run as update.state reports it.
type RunState struct {
	RunID   string `json:"run_id"`
	Version string `json:"version"`
	State   string `json:"state"`
	Step    string `json:"step,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Command runs a command the home sent this machine (nil: none but disconnect): its
// result and detail, or handled false for a command it does not know. It runs in its
// own goroutine: it may take its time (the switchover's promotion).
type Command func(ctx context.Context, name string, args json.RawMessage) (handled, ok bool, detail any)

// Agent is the machine agent.
type Agent struct {
	ID     Identity
	State  func() UpdateState
	Logger *slog.Logger
	// Commands answers the home's commands besides disconnect (the switchover's).
	Commands Command

	mu        sync.Mutex
	nudge     chan struct{}
	key       *Key
	subs      map[chan Event]struct{}
	lastErr   string
	connected atomic.Bool
	since     time.Time
}

// Nudge sends a fresh update.state when connected (this machine's run moved on).
func (a *Agent) Nudge() {
	select {
	case a.nudgeCh() <- struct{}{}:
	default:
	}
}

func (a *Agent) nudgeCh() chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.nudge == nil {
		a.nudge = make(chan struct{}, 1)
	}
	return a.nudge
}

func (a *Agent) log() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

// Status is the agent's state for the local /agent/state.
type Status struct {
	Connected bool      `json:"connected"`
	Since     time.Time `json:"since,omitempty"`
	HasKey    bool      `json:"has_key"`
	KeyID     string    `json:"key_id,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

// Status reports the agent's state.
func (a *Agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Status{Connected: a.connected.Load(), LastError: a.lastErr, HasKey: a.key != nil}
	if s.Connected {
		s.Since = a.since
	}
	if a.key != nil {
		s.KeyID = a.key.ID
	}
	return s
}

// CurrentKey is the delegation key, if any.
func (a *Agent) CurrentKey() *Key {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.key == nil {
		return nil
	}
	k := *a.key
	return &k
}

// Subscribe returns a channel of events (the current key first, when there is one)
// and a cancel func.
func (a *Agent) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	a.mu.Lock()
	if a.subs == nil {
		a.subs = map[chan Event]struct{}{}
	}
	a.subs[ch] = struct{}{}
	if a.key != nil {
		k := *a.key
		ch <- Event{Kind: "keys", Keys: &k}
	}
	a.mu.Unlock()
	return ch, func() {
		a.mu.Lock()
		if _, ok := a.subs[ch]; ok {
			delete(a.subs, ch)
			close(ch)
		}
		a.mu.Unlock()
	}
}

func (a *Agent) publish(e Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e.Kind == "keys" && e.Keys != nil {
		k := *e.Keys
		a.key = &k
	}
	for ch := range a.subs {
		select {
		case ch <- e:
		default: // a stuck subscriber misses it; its reconnect gets the key again
		}
	}
}

func (a *Agent) setErr(err error) {
	a.mu.Lock()
	if err != nil {
		a.lastErr = err.Error()
	}
	a.mu.Unlock()
}

// ErrNoClusterKey: a node config written before handshake v2 cannot connect (EC2).
var ErrNoClusterKey = errors.New("machineagent: this machine joined before the channel's handshake v2 (no home.cluster_pub): re-join it")

// Run connects and reconnects until ctx ends.
func (a *Agent) Run(ctx context.Context) {
	if a.ID.ClusterPub == "" {
		a.setErr(ErrNoClusterKey)
		a.log().Warn("node: the machine agent cannot connect: re-join this machine", "event", "node_agent_rejoin")
		return
	}
	delay := backoffFloor
	for ctx.Err() == nil {
		start := time.Now()
		code, err := a.session(ctx)
		a.connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		a.setErr(err)
		if time.Since(start) > time.Minute {
			delay = backoffFloor
		}
		wait := delay
		switch code {
		case closeUnsupported, closeNotMember, closeAuthFailed, closeReplaced, closeRevoked, closeRateLimited:
			wait = backoffCeiling
		}
		jitter := 0.8 + 0.4*mrand.Float64()
		wait = time.Duration(float64(wait) * jitter)
		a.log().Info("node: machine agent disconnected; reconnecting", "in", wait.String(), "code", int(code),
			"err", fmt.Sprint(err), "event", "node_agent_retry")
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		delay = min(delay*2, backoffCeiling)
	}
}

func agentURL(home string) (string, error) {
	u, err := url.Parse(strings.TrimRight(home, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("machineagent: bad home URL %q", home)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/cluster/agent"
	return u.String(), nil
}

// session runs one connection: handshake, then frames until it ends.
func (a *Agent) session(ctx context.Context) (websocket.StatusCode, error) {
	wsURL, err := agentURL(a.ID.HomeURL)
	if err != nil {
		return 0, err
	}
	nodePriv, err1 := base64.StdEncoding.DecodeString(a.ID.PrivateKey)
	homePub, err2 := base64.StdEncoding.DecodeString(a.ID.ClusterPub)
	if err1 != nil || err2 != nil || len(nodePriv) != keySize || len(homePub) != keySize {
		return 0, errors.New("machineagent: the node config's keys are not 32-byte base64")
	}
	dctx, cancel := context.WithTimeout(ctx, handshakeMax)
	c, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		cancel()
		return 0, err
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(8 << 20)
	c2s, s2c, err := a.handshake(dctx, c, nodePriv, homePub)
	cancel()
	if err != nil {
		return websocket.CloseStatus(err), err
	}
	a.mu.Lock()
	a.since = time.Now()
	a.lastErr = ""
	a.mu.Unlock()
	a.connected.Store(true)
	a.log().Info("node: machine agent connected to the home", "event", "node_agent_connected")
	return a.pump(ctx, c, c2s, s2c)
}

func (a *Agent) handshake(ctx context.Context, c *websocket.Conn, nodePriv, homePub []byte) (*cipher, *cipher, error) {
	ephPriv := make([]byte, keySize)
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, ephPriv); err != nil {
		return nil, nil, err
	}
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ephPub, err := curve25519.X25519(ephPriv, curve25519.Basepoint)
	if err != nil {
		return nil, nil, err
	}
	hello := map[string]any{"t": "hello", "v": protocolVersion, "kind": "node", "process": "supervisor",
		"node_id": a.ID.NodeID, "worker_id": "sup-" + a.ID.Node8, "pid": os.Getpid(), "host": a.ID.Host,
		"eph": base64.StdEncoding.EncodeToString(ephPub), "nonce": base64.StdEncoding.EncodeToString(nonce),
		"os": runtime.GOOS}
	if a.State != nil {
		hello["version"] = a.State().Version
	}
	helloBytes, err := json.Marshal(hello)
	if err != nil {
		return nil, nil, err
	}
	if err := c.Write(ctx, websocket.MessageText, helloBytes); err != nil {
		return nil, nil, err
	}
	typ, chBytes, err := c.Read(ctx)
	if err != nil {
		return nil, nil, err
	}
	var ch struct {
		T         string `json:"t"`
		ServerEph string `json:"server_eph"`
		Nonce     string `json:"nonce"`
	}
	if typ != websocket.MessageText || json.Unmarshal(chBytes, &ch) != nil || ch.T != "challenge" {
		return nil, nil, errors.New("machineagent: no challenge")
	}
	serverEph, err1 := base64.StdEncoding.DecodeString(ch.ServerEph)
	serverNonce, err2 := base64.StdEncoding.DecodeString(ch.Nonce)
	if err1 != nil || err2 != nil {
		return nil, nil, errors.New("machineagent: bad challenge")
	}
	th := transcript(helloBytes, chBytes)
	dhEE, err := dh(ephPriv, serverEph)
	if err != nil {
		return nil, nil, err
	}
	dhSE, err := dh(nodePriv, serverEph)
	if err != nil {
		return nil, nil, err
	}
	dhES, err := dh(ephPriv, homePub)
	if err != nil {
		return nil, nil, err
	}
	ikm := append(append(append([]byte{}, dhEE...), dhSE...), dhES...)
	kc2s, ks2c, err := deriveKeys(ikm, nonce, serverNonce)
	if err != nil {
		return nil, nil, err
	}
	auth, _ := json.Marshal(map[string]string{"t": "auth", "proof": base64.StdEncoding.EncodeToString(clientProof(kc2s, th))})
	if err := c.Write(ctx, websocket.MessageText, auth); err != nil {
		return nil, nil, err
	}
	c2s, err := newCipher(kc2s)
	if err != nil {
		return nil, nil, err
	}
	s2c, err := newCipher(ks2c)
	if err != nil {
		return nil, nil, err
	}
	typ, sealed, err := c.Read(ctx)
	if err != nil {
		return nil, nil, err
	}
	pt, err := s2c.open(sealed)
	if typ != websocket.MessageBinary || err != nil {
		_ = c.Close(closeAuthFailed, "auth_failed welcome")
		return nil, nil, errors.New("machineagent: this is not the home I joined (welcome does not open)")
	}
	var w struct {
		T     string `json:"t"`
		Proof string `json:"proof"`
	}
	proof, _ := base64.StdEncoding.DecodeString(func() string { _ = json.Unmarshal(pt, &w); return w.Proof }())
	if w.T != "welcome" || !hmacEqual(proof, serverProof(ks2c, th)) {
		_ = c.Close(closeAuthFailed, "auth_failed proof")
		return nil, nil, errors.New("machineagent: this is not the home I joined (wrong proof)")
	}
	return c2s, s2c, nil
}

func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// pump runs the sealed frames of one session.
func (a *Agent) pump(ctx context.Context, c *websocket.Conn, c2s, s2c *cipher) (websocket.StatusCode, error) {
	out := make(chan []byte, 64)
	send := func(v any) {
		b, err := json.Marshal(v)
		if err == nil {
			select {
			case out <- b:
			default:
			}
		}
	}
	sendState := func() {
		if a.State == nil {
			return
		}
		st := a.State()
		f := map[string]any{"t": "update.state", "version": st.Version, "prepared_version": st.PreparedVersion,
			"platform": st.Platform, "layout": st.Layout}
		if st.Run != nil {
			f["run"] = st.Run
		}
		if len(st.Attempts) > 0 {
			f["attempts"] = st.Attempts
		}
		send(f)
	}
	sendState()
	nudge := a.nudgeCh()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	recv := make(chan struct{}, 1)
	readErr := make(chan error, 1)
	go func() {
		for {
			typ, data, err := c.Read(sctx)
			if err != nil {
				readErr <- err
				return
			}
			if typ != websocket.MessageBinary {
				readErr <- errors.New("machineagent: text after welcome")
				return
			}
			pt, err := s2c.open(data)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case recv <- struct{}{}:
			default:
			}
			a.dispatch(pt, send)
		}
	}()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	silence := time.NewTimer(silenceLimit)
	defer silence.Stop()
	write := func(b []byte) error {
		sealed, err := c2s.seal(b)
		if err != nil {
			return err
		}
		wctx, wc := context.WithTimeout(context.Background(), 10*time.Second)
		defer wc()
		return c.Write(wctx, websocket.MessageBinary, sealed)
	}
	for {
		select {
		case <-ctx.Done():
			_ = c.Close(websocket.StatusNormalClosure, "normal")
			return websocket.StatusNormalClosure, nil
		case err := <-readErr:
			return websocket.CloseStatus(err), err
		case b := <-out:
			if err := write(b); err != nil {
				return 0, err
			}
		case <-nudge:
			sendState()
		case <-recv:
			if !silence.Stop() {
				select {
				case <-silence.C:
				default:
				}
			}
			silence.Reset(silenceLimit)
		case <-silence.C:
			_ = c.Close(websocket.StatusCode(4008), "timeout silence")
			return 4008, errors.New("machineagent: 45 s of silence")
		case <-ping.C:
			b, _ := json.Marshal(map[string]any{"t": "ping", "ts": time.Now().UTC().Format(time.RFC3339Nano)})
			if err := write(b); err != nil {
				return 0, err
			}
		}
	}
}

func (a *Agent) dispatch(pt []byte, send func(any)) {
	var head struct {
		T string `json:"t"`
	}
	if json.Unmarshal(pt, &head) != nil {
		return
	}
	switch head.T {
	case "ping":
		var f struct {
			TS json.RawMessage `json:"ts"`
		}
		_ = json.Unmarshal(pt, &f)
		send(map[string]any{"t": "pong", "ts": f.TS})
	case "keys":
		var f struct {
			Delegation Key `json:"delegation"`
		}
		if json.Unmarshal(pt, &f) == nil && f.Delegation.ID != "" && len(f.Delegation.Key) == keySize {
			a.publish(Event{Kind: "keys", Keys: &f.Delegation})
			a.log().Info("node: delegation key received", "key_id", f.Delegation.ID, "event", "node_agent_keys")
		}
	case "auth.revoked":
		var f Revoked
		if json.Unmarshal(pt, &f) == nil {
			a.publish(Event{Kind: "revoked", Revoked: &f})
		}
	case "cmd":
		var f struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(pt, &f) == nil && f.ID != "" {
			if f.Name == "disconnect" {
				send(map[string]any{"t": "cmd.result", "id": f.ID, "ok": true, "detail": ""})
				return
			}
			if a.Commands != nil {
				var args struct {
					Args json.RawMessage `json:"args"`
				}
				_ = json.Unmarshal(pt, &args)
				go func() {
					handled, ok, detail := a.Commands(context.Background(), f.Name, args.Args)
					if handled {
						send(map[string]any{"t": "cmd.result", "id": f.ID, "ok": ok, "detail": detail})
						return
					}
					send(map[string]any{"t": "cmd.result", "id": f.ID, "ok": false,
						"detail": "the machine agent does not run " + f.Name})
				}()
				return
			}
			// the update commands are story 35.12's; refused plainly until then
			send(map[string]any{"t": "cmd.result", "id": f.ID, "ok": false,
				"detail": "the machine agent does not run " + f.Name + " yet"})
		}
	}
}
