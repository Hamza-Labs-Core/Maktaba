package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

const testHomeIP = "192.168.50.1"

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

type fixture struct {
	t          *testing.T
	hub        *Hub
	store      *cluster.MemoryStore
	srv        *httptest.Server
	dir        string
	homeKey    []byte
	clusterPub []byte
	cancel     context.CancelFunc
	done       chan struct{}
}

type fixtureOpt func(*Config)

// newFixture runs a hub on an httptest server. The X-Test-User header
// picks the principal: "admin", "viewer" or none.
func newFixture(t *testing.T, opts ...fixtureOpt) *fixture {
	t.Helper()
	f := &fixture{t: t, store: cluster.NewMemoryStore(), dir: t.TempDir(), homeKey: make([]byte, 32), done: make(chan struct{})}
	_, _ = rand.Read(f.homeKey)
	clusterPriv, clusterPub, err := cluster.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	f.clusterPub = clusterPub
	cfg := Config{
		Store:           f.store,
		HomeKey:         f.homeKey,
		ClusterKey:      clusterPriv,
		HomeAddr:        testHomeIP,
		Locator:         &Locator{Override: f.dir},
		AgentRatePerMin: -1,
		LLMQueueTimeout: 2 * time.Second,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		// The hub sweeps the share at start by the wall clock, and the
		// tests seed fixed days (2026-10-01, 2026-10-19): with the default
		// 14 days the sweep raced them for any day older than that
		// (TestHistoryFromTheArchive, Oct 2026). The sweep has its own tests.
		RetentionDays: 100 * 365,
	}
	for _, o := range opts {
		o(&cfg)
	}
	f.hub = New(cfg)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch req.Header.Get("X-Test-User") {
			case "admin":
				req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u1", IsAdmin: true}))
			case "viewer":
				req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u2"}))
			case "pat":
				req = req.WithContext(principal.WithPrincipal(req.Context(),
					&principal.Principal{UserID: "u3", Source: principal.SourcePAT}))
			}
			next.ServeHTTP(w, req)
		})
	})
	f.hub.Mount(r)
	f.srv = httptest.NewServer(r)
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	go func() {
		defer close(f.done)
		f.hub.Run(ctx)
	}()
	t.Cleanup(f.close)
	// Run resolves the location before serving; wait for it
	deadline := time.Now().Add(2 * time.Second)
	for f.hub.Location().Dir == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return f
}

func (f *fixture) close() {
	f.cancel()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		f.t.Error("hub did not stop")
	}
	f.srv.Close()
}

func (f *fixture) wsURL(path string) string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + path
}

// approvedNode registers an approved node and returns its id and key.
func (f *fixture) approvedNode(name string) (string, []byte) {
	f.t.Helper()
	priv, pub, err := cluster.GenerateKey()
	if err != nil {
		f.t.Fatal(err)
	}
	id := uuid.NewString()
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)
	if err := f.store.PutPending(ctx, cluster.Node{ID: id, Name: name, NodePubkey: cluster.EncodeKey(pub), ExpiresAt: &exp,
		CreatedAt: time.Now()}); err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.Approve(ctx, id, cluster.RoleName(id), json.RawMessage(`{}`), "test", time.Now()); err != nil {
		f.t.Fatal(err)
	}
	return id, priv
}

// testAgent is a Go client of the agent protocol.
type testAgent struct {
	t       *testing.T
	c       *websocket.Conn
	c2s     *Cipher
	s2c     *Cipher
	Session string
	Welcome map[string]any
}

type helloOpts struct {
	kind, nodeID, workerID string
	nodePriv, homeKey      []byte
	// clusterPub is the home key the machine expects (default: the
	// fixture's); a wrong one makes the welcome undecryptable
	clusterPub []byte
	badProof   bool
	version    int
	ip         string
}

// dialAgent runs the v2 handshake as a process does (and checks the hub's
// proof); on refusal it returns the close error.
func (f *fixture) dialAgent(o helloOpts) (*testAgent, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, f.wsURL(AgentPath), nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(maxWSMessage)
	clientNonce := make([]byte, NonceSize)
	_, _ = rand.Read(clientNonce)
	ephPriv, ephPub, err := cluster.GenerateKey()
	if err != nil {
		return nil, err
	}
	if o.workerID == "" {
		o.workerID = "w-" + newID()
	}
	if o.version == 0 {
		o.version = ProtocolVersion
	}
	hello := map[string]any{"t": "hello", "v": o.version, "kind": o.kind, "worker_id": o.workerID, "pid": 4242,
		"host": "test-host", "eph": base64.StdEncoding.EncodeToString(ephPub),
		"nonce": base64.StdEncoding.EncodeToString(clientNonce)}
	if o.nodeID != "" {
		hello["node_id"] = o.nodeID
	}
	if o.ip != "" {
		hello["ip"] = o.ip
	}
	helloBytes, err := writeText(ctx, c, hello)
	if err != nil {
		return nil, err
	}
	var ch struct {
		T         string `json:"t"`
		ServerEph string `json:"server_eph"`
		Nonce     string `json:"nonce"`
	}
	challengeBytes, err := readText(ctx, c, &ch)
	if err != nil {
		return nil, err
	}
	serverNonce, _ := base64.StdEncoding.DecodeString(ch.Nonce)
	serverEph, _ := base64.StdEncoding.DecodeString(ch.ServerEph)
	th := Transcript(helloBytes, challengeBytes)
	ee, err := DH(ephPriv, serverEph)
	if err != nil {
		return nil, err
	}
	var ikm []byte
	if o.kind == "node" {
		home := o.clusterPub
		if home == nil {
			home = f.clusterPub
		}
		se, err1 := DH(o.nodePriv, serverEph)
		es, err2 := DH(ephPriv, home)
		if err1 != nil || err2 != nil {
			return nil, errors.Join(err1, err2)
		}
		ikm = NodeIKM(ee, se, es)
	} else {
		ikm = HomeIKM(o.homeKey, ee)
	}
	kc2s, ks2c, err := DeriveKeys(ikm, clientNonce, serverNonce)
	if err != nil {
		return nil, err
	}
	proof := ClientProof(kc2s, th)
	if o.badProof {
		proof[0] ^= 1
	}
	if _, err := writeText(ctx, c, map[string]any{"t": "auth", "proof": base64.StdEncoding.EncodeToString(proof)}); err != nil {
		return nil, err
	}
	a := &testAgent{t: f.t, c: c}
	a.c2s, _ = NewCipher(kc2s)
	a.s2c, _ = NewCipher(ks2c)
	w, err := a.recvCtx(ctx)
	if err != nil {
		_ = c.CloseNow()
		return nil, err
	}
	if w["t"] != "welcome" || w["proof"] != base64.StdEncoding.EncodeToString(ServerProof(ks2c, th)) {
		_ = c.CloseNow()
		return nil, errors.New("not the home I joined")
	}
	a.Welcome = w
	a.Session, _ = w["session"].(string)
	f.t.Cleanup(func() { _ = c.CloseNow() })
	return a, nil
}

func (f *fixture) node(name string) (*testAgent, string) {
	f.t.Helper()
	id, priv := f.approvedNode(name)
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv})
	if err != nil {
		f.t.Fatalf("node handshake: %v", err)
	}
	return a, id
}

func (a *testAgent) send(v any) {
	a.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		a.t.Fatal(err)
	}
	a.sendRaw(b)
}

func (a *testAgent) sendRaw(b []byte) {
	a.t.Helper()
	sealed, err := a.c2s.Seal(b)
	if err != nil {
		a.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.c.Write(ctx, websocket.MessageBinary, sealed); err != nil {
		a.t.Fatal(err)
	}
}

func (a *testAgent) recvCtx(ctx context.Context) (map[string]any, error) {
	typ, data, err := a.c.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageBinary {
		return nil, errors.New("unsealed frame from the hub")
	}
	pt, err := a.s2c.Open(data)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(pt, &m)
}

// recvType waits for a frame of type t (skipping pings).
func (a *testAgent) recvType(t string) map[string]any {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		m, err := a.recvCtx(ctx)
		if err != nil {
			a.t.Fatalf("waiting for %s: %v", t, err)
		}
		if m["t"] == t {
			return m
		}
	}
}

// closed waits for the hub to close the connection and returns the close
// status and reason.
func (a *testAgent) closed() (websocket.StatusCode, string) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, _, err := a.c.Read(ctx)
		if err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				return ce.Code, ce.Reason
			}
			if ctx.Err() != nil {
				a.t.Fatal("connection still open")
			}
			return -1, err.Error()
		}
	}
}

// closeOf is the close code and reason of a refused or closed
// connection (-1 when it was not a close).
func closeOf(err error) (websocket.StatusCode, string) {
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		return ce.Code, ce.Reason
	}
	return -1, fmt.Sprint(err)
}

// testBrowser is a browser on /api/cluster/ws; a goroutine reads its
// messages (a cancelled read would close the socket).
type testBrowser struct {
	t    *testing.T
	c    *websocket.Conn
	msgs chan browserMsg
}

func (f *fixture) browser(user string) *testBrowser {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	if user != "" {
		h.Set("X-Test-User", user)
	}
	c, _, err := websocket.Dial(ctx, f.wsURL("/api/cluster/ws"), &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		f.t.Fatalf("browser dial: %v", err)
	}
	c.SetReadLimit(16 << 20)
	b := &testBrowser{t: f.t, c: c, msgs: make(chan browserMsg, 4096)}
	go func() {
		defer close(b.msgs)
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m browserMsg
			if json.Unmarshal(data, &m) == nil {
				b.msgs <- m
			}
		}
	}()
	f.t.Cleanup(func() { _ = c.CloseNow() })
	return b
}

func (b *testBrowser) send(v any) {
	b.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, _ := json.Marshal(v)
	if err := b.c.Write(ctx, websocket.MessageText, data); err != nil {
		b.t.Fatal(err)
	}
}

type browserMsg struct {
	Type  string          `json:"type"`
	Topic string          `json:"topic"`
	Data  json.RawMessage `json:"data"`
}

// next waits for a message matching pred.
func (b *testBrowser) next(pred func(browserMsg) bool) browserMsg {
	b.t.Helper()
	timeout := time.After(4 * time.Second)
	for {
		select {
		case m, ok := <-b.msgs:
			if !ok {
				b.t.Fatal("browser socket closed")
			}
			if pred(m) {
				return m
			}
		case <-timeout:
			b.t.Fatal("browser: timed out")
		}
	}
}

func (b *testBrowser) nextType(typ string) browserMsg {
	b.t.Helper()
	return b.next(func(m browserMsg) bool { return m.Type == typ })
}

// none asserts no message matching pred arrives within d.
func (b *testBrowser) none(d time.Duration, pred func(browserMsg) bool) {
	b.t.Helper()
	timeout := time.After(d)
	for {
		select {
		case m, ok := <-b.msgs:
			if !ok {
				return
			}
			if pred(m) {
				b.t.Fatalf("unexpected message: %s %s", m.Type, m.Data)
			}
		case <-timeout:
			return
		}
	}
}

// subscribeSnap subscribes to one topic and returns its snapshot.
func (b *testBrowser) subscribeSnap(topic string) json.RawMessage {
	b.t.Helper()
	b.send(map[string]any{"op": "subscribe", "topics": []string{topic}})
	return b.next(func(m browserMsg) bool { return m.Type == "snapshot" && m.Topic == topic }).Data
}

func (b *testBrowser) subscribe(topics ...string) {
	b.t.Helper()
	b.send(map[string]any{"op": "subscribe", "topics": topics})
	for _, tp := range topics {
		b.next(func(m browserMsg) bool { return m.Type == "snapshot" && m.Topic == tp })
	}
}

// waitFor polls cond.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fixture) do(method, path, body, user string) *http.Response {
	f.t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}
