package link

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/ca"
)

const testNode = "0b6f3c1e-2a4d-4e8f-9c71-5d2a8b3e6f10"

type harness struct {
	t       *testing.T
	ca      *ca.CA
	store   *ca.MemStore
	srv     *Server
	home    string
	port    int
	dir     string
	client  *Client
	cancel  context.CancelFunc
	mu      sync.Mutex
	members map[string]bool
}

// newHarness starts a home (the API port split by SniffListener, the link's routes and
// api) and a machine with a certificate, its client connected.
func newHarness(t *testing.T, api http.Handler, opts ...func(*harness)) *harness {
	t.Helper()
	c, err := ca.New("test")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ca: c, store: &ca.MemStore{}, members: map[string]bool{testNode: true}}
	c.Store = h.store
	h.srv = &Server{CA: c, Certs: h.store, Handler: api,
		Approved: func(_ context.Context, id string) (bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.members[id], nil
		}}
	pair, _, err := c.IssueHome(context.Background(), ca.KindServer, []string{"127.0.0.1"}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	h.srv.SetServerCertificate(pair)
	for _, o := range opts {
		o(h)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(PathLink, h.srv.ServeLink)
	mux.HandleFunc(PathStream, h.srv.ServeStream)
	mux.HandleFunc(PathCACert, h.srv.ServeCACert)
	if api != nil {
		mux.Handle("/", api)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := NewSniffListener(ln, h.srv.TLSConfig())
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(sl) }()
	t.Cleanup(func() { _ = hs.Close() })
	h.port = ln.Addr().(*net.TCPAddr).Port
	h.home = "http://127.0.0.1:" + strconv.Itoa(h.port)
	h.dir = t.TempDir()
	h.issueNode()
	return h
}

// issueNode gives the machine a certificate (key made here, CSR signed by the CA).
func (h *harness) issueNode() {
	h.t.Helper()
	key, csr, err := NewKeyAndCSR(testNode)
	if err != nil {
		h.t.Fatal(err)
	}
	is, err := h.ca.SignNode(context.Background(), csr, testNode, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := (CertFiles{Dir: h.dir}).Save(key, is.PEM, h.ca.CertPEM, h.ca.SHA256); err != nil {
		h.t.Fatal(err)
	}
}

// connect starts the machine's client and waits for its control channel.
func (h *harness) connect(offer func(context.Context, string, string, net.Conn)) *Client {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.client = &Client{Home: h.home, NodeID: testNode, Files: CertFiles{Dir: h.dir}, Offer: offer}
	go h.client.Run(ctx)
	h.t.Cleanup(cancel)
	h.waitFor(func() bool { return h.client.Connected() && h.srv.Live(testNode) }, "the control channel")
	return h.client
}

func (h *harness) waitFor(cond func() bool, what string) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tlsClient is an HTTPS client of the home's port trusting its CA, presenting cert (nil:
// none).
func (h *harness) tlsClient(cert *tls.Certificate) *http.Client {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: h.ca.Pool(), ServerName: "127.0.0.1"}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 10 * time.Second}
}

func (h *harness) nodePair() tls.Certificate {
	h.t.Helper()
	m, err := (CertFiles{Dir: h.dir}).Load()
	if err != nil {
		h.t.Fatal(err)
	}
	return m.Pair
}
