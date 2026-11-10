package homeproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
)

// fakeLink sends straight to a test home (unsigned).
type fakeLink struct {
	homelink.Link
	url string
}

func (l fakeLink) Transport() http.RoundTripper { return http.DefaultTransport }
func (l fakeLink) HomeURL() string              { return l.url }

// A node-local sign-in never reaches the home (it knows nothing of it and would clear
// it): its cookies are dropped and no delegated session goes with the request. A home
// session goes on as the delegated one; other cookies stay.
func TestANodeLocalSessionIsNotForwarded(t *testing.T) {
	var cookie, delegated string
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, delegated = r.Header.Get("Cookie"), r.Header.Get(delegation.HeaderDelegatedSession)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer home.Close()
	p := New(fakeLink{url: home.URL}, nil)
	send := func(session string) {
		req := httptest.NewRequest(http.MethodGet, "/api/videos/v1/stream", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: session})
		req.AddCookie(&http.Cookie{Name: CSRFCookie, Value: "csrf-1"})
		req.AddCookie(&http.Cookie{Name: "mkt_lang", Value: "ar"})
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("the proxied request: %d", rec.Code)
		}
	}
	send(LocalSessionPrefix + "abc")
	if strings.Contains(cookie, SessionCookie) || strings.Contains(cookie, CSRFCookie) || delegated != "" {
		t.Fatalf("a node-local session went to the home: cookie %q, delegated %q", cookie, delegated)
	}
	if !strings.Contains(cookie, "mkt_lang=ar") {
		t.Fatalf("the other cookies were dropped: %q", cookie)
	}
	send("6f1c2b0e-home-session")
	if delegated != "6f1c2b0e-home-session" || !strings.Contains(cookie, SessionCookie) {
		t.Fatalf("a home session: cookie %q, delegated %q", cookie, delegated)
	}
}

// Epic 59 story 59.08: the home gets the client this node resolved, never the chain a
// client wrote (the home believes it only from a node in its trusted_proxies).
func TestTheHomeGetsTheResolvedClient(t *testing.T) {
	var xff, fc, fcSig string
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xff = r.Header.Get("X-Forwarded-For")
		fc, fcSig = r.Header.Get(delegation.HeaderForwardedClient), r.Header.Get(delegation.HeaderForwardedClientSig)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer home.Close()
	p := New(fakeLink{url: home.URL}, nil)
	for remote, want := range map[string]string{
		"192.168.1.20:5000": "192.168.1.20", // no forwarding: the node's peer
		"203.0.113.9":       "203.0.113.9",  // resolved by the node's middleware (a bare address)
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/library", nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-For", "6.6.6.6")
		req.Header.Set(delegation.HeaderForwardedClient, "6.6.6.6")
		req.Header.Set(delegation.HeaderForwardedClientSig, "forged")
		p.ServeHTTP(httptest.NewRecorder(), req)
		if xff != want || fc != want || fcSig != "" {
			t.Errorf("from %s: the home got X-Forwarded-For %q, forwarded client %q (sig %q), want %q", remote, xff, fc, fcSig, want)
		}
	}
}

// End to end over a signed HTTP link: the home (Accept, then its client-address middleware
// with no trusted proxies) takes the client the node signed for, though the node's address
// is in no list.
func TestTheHomeBelievesTheSignedClient(t *testing.T) {
	const node = "11111111-2222-3333-4444-555555555555"
	secret := []byte("0123456789abcdef0123456789abcdef")
	var client, peer string
	v := &delegation.Verifier{Keys: oneKey{node: node, secret: secret}}
	chain := delegation.Accept(v, SessionCookie)(clientip.Middleware(nil)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			client, peer = r.RemoteAddr, clientip.Peer(r)
			w.WriteHeader(http.StatusNoContent)
		})))
	// the node reaches the home from a LAN address (a test server's peer is loopback,
	// whose X-Forwarded-For would be believed anyway)
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "192.168.1.30:40000"
		chain.ServeHTTP(w, r)
	}))
	defer home.Close()
	link := &homelink.HTTPLink{Home: home.URL, NodeID: node}
	link.SetKey(delegation.Key{ID: "dk_1", Secret: secret, NotAfter: time.Now().Add(time.Hour)})
	p := New(link, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/library", nil)
	req.RemoteAddr = "192.168.1.77:5000"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || client != "192.168.1.77" || peer != "192.168.1.30:40000" {
		t.Fatalf("%d: the home saw client %q peer %q", w.Code, client, peer)
	}
}

type oneKey struct {
	node   string
	secret []byte
}

func (k oneKey) Lookup(_ context.Context, node, key string) ([]byte, bool) {
	return k.secret, node == k.node && key == "dk_1"
}
