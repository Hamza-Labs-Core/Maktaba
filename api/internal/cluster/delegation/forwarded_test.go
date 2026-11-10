package delegation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/linkctx"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
)

// Epic 59 story 59.08: the home believes the client a node forwarded only from the node's
// delegation signature over it (or its authenticated link), never from the node's address.

const fwdNode = "11111111-2222-3333-4444-555555555555"
const nodeAddr = "192.168.1.30:50000"

// fwdHome is the home's chain as api/main.go builds it: Accept around the router's
// client-address middleware (no trusted proxies), answering the client it resolved.
type fwdHome struct {
	h                       http.Handler
	client, peer, hdr, node string
}

func newFwdHome(v *Verifier) *fwdHome {
	f := &fwdHome{}
	f.h = Accept(v, "mkt_sess")(clientip.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.client, f.peer, f.node = r.RemoteAddr, clientip.Peer(r), NodeFrom(r.Context())
		f.hdr = r.Header.Get(HeaderForwardedClient) + r.Header.Get(HeaderForwardedClientSig)
		w.WriteHeader(http.StatusNoContent)
	})))
	return f
}

func (f *fwdHome) serve(r *http.Request) int {
	f.client, f.peer, f.hdr, f.node = "", "", "", ""
	if r.RemoteAddr == "" || r.RemoteAddr == "192.0.2.1:1234" {
		r.RemoteAddr = nodeAddr
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w.Code
}

// nodeRequest is what homeproxy sends: X-Forwarded-For and X-Maktaba-Forwarded-Client set
// to the client, then signed by the link's signer.
func nodeRequest(t *testing.T, client string, key Key) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", "http://home/api/videos?page=2", nil)
	r.Header.Set("X-Forwarded-For", client)
	if client != "" {
		r.Header.Set(HeaderForwardedClient, client)
	}
	if err := SignRequest(r, fwdNode, key, time.Now()); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestForwardedClientAttested(t *testing.T) {
	key := Key{ID: "dk_1", Secret: []byte("0123456789abcdef0123456789abcdef")}
	home := newFwdHome(&Verifier{Keys: staticKeys{fwdNode: {"dk_1": key.Secret}}})

	// a valid attested client is the request's client; the peer stays the node's
	r := nodeRequest(t, "192.168.1.77", key)
	if r.Header.Get(HeaderForwardedClientSig) == "" {
		t.Fatal("the signer signs the forwarded client")
	}
	if c := home.serve(r); c != 204 || home.client != "192.168.1.77" || home.peer != nodeAddr || home.node != fwdNode || home.hdr != "" {
		t.Fatalf("attested: %d client %q peer %q node %q headers %q", c, home.client, home.peer, home.node, home.hdr)
	}

	// a tampered client: the request still verifies (the signed string is unchanged) but
	// the client is the node's address
	r = nodeRequest(t, "192.168.1.77", key)
	r.Header.Set(HeaderForwardedClient, "10.9.9.9")
	if c := home.serve(r); c != 204 || home.client != nodeAddr || home.hdr != "" {
		t.Fatalf("tampered client: %d %q %q", c, home.client, home.hdr)
	}
	// a tampered or missing HMAC
	for _, sig := range []string{"AAAA", "not base64!", ""} {
		r = nodeRequest(t, "192.168.1.77", key)
		r.Header.Set(HeaderForwardedClientSig, sig)
		if c := home.serve(r); c != 204 || home.client != nodeAddr {
			t.Fatalf("tampered hmac %q: %d %q", sig, c, home.client)
		}
	}
	// an HMAC made with another key, or bound to another request's signature
	r = nodeRequest(t, "192.168.1.77", key)
	r.Header.Set(HeaderForwardedClientSig, ForwardedClientSig([]byte("another key"), r.Header.Get(HeaderSig), "192.168.1.77"))
	if c := home.serve(r); c != 204 || home.client != nodeAddr {
		t.Fatalf("another key: %d %q", c, home.client)
	}
	other := nodeRequest(t, "192.168.1.77", key)
	r = nodeRequest(t, "192.168.1.77", key)
	r.Header.Set(HeaderForwardedClientSig, other.Header.Get(HeaderForwardedClientSig))
	if c := home.serve(r); c != 204 || home.client != nodeAddr {
		t.Fatalf("moved from another request: %d %q", c, home.client)
	}
	// text that is no address is never attested, even signed
	r = nodeRequest(t, "", key)
	r.Header.Set(HeaderForwardedClient, "nas.local")
	r.Header.Set(HeaderForwardedClientSig, ForwardedClientSig(key.Secret, r.Header.Get(HeaderSig), "nas.local"))
	if c := home.serve(r); c != 204 || home.client != nodeAddr {
		t.Fatalf("not an address: %d %q", c, home.client)
	}

	// an old node, without the header: it keeps working and shows as itself (its
	// X-Forwarded-For is not believed from its address)
	r = nodeRequest(t, "", key)
	r.Header.Set("X-Forwarded-For", "192.168.1.77")
	if c := home.serve(r); c != 204 || home.client != nodeAddr || home.node != fwdNode {
		t.Fatalf("old node: %d %q %q", c, home.client, home.node)
	}
}

// From a request with no node signature, the headers are anyone's text: dropped, and the
// peer stands.
func TestForwardedClientFromNonNode(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	home := newFwdHome(&Verifier{Keys: staticKeys{fwdNode: {"dk_1": key}}})
	r := httptest.NewRequest("GET", "http://home/api/videos", nil)
	r.Header.Set(HeaderForwardedClient, "192.168.1.77")
	r.Header.Set(HeaderForwardedClientSig, ForwardedClientSig(key, "", "192.168.1.77"))
	r.Header.Set("X-Forwarded-For", "192.168.1.77")
	if c := home.serve(r); c != 204 || home.client != nodeAddr || home.hdr != "" || home.node != "" {
		t.Fatalf("non-node: %d %q %q", c, home.client, home.hdr)
	}
}

// A revoked key, a revoked node and a deleted node lose trust on their very next request:
// the signature is checked against the live key and membership on each one.
func TestForwardedClientRevokedAtOnce(t *testing.T) {
	ctx := context.Background()
	ks := &KeyStore{Secrets: secrets.NewMemory()}
	key, _, err := ks.Issue(ctx, fwdNode)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	state := map[string]string{fwdNode: "approved"}
	member := func(_ context.Context, id string) bool {
		mu.Lock()
		defer mu.Unlock()
		return state[id] == "approved"
	}
	home := newFwdHome(&Verifier{Keys: ks, Member: member})
	attested := func() bool {
		return home.serve(nodeRequest(t, "192.168.1.77", key)) == 204 && home.client == "192.168.1.77"
	}
	if !attested() {
		t.Fatal("approved: not attested")
	}
	// the key revoked (KeyStore.Revoke, as a revoke or removal does)
	ks.Revoke(ctx, fwdNode)
	if c := home.serve(nodeRequest(t, "192.168.1.77", key)); c != 401 || home.client != "" {
		t.Fatalf("revoked key: %d %q", c, home.client)
	}
	key, _, _ = ks.Issue(ctx, fwdNode)
	if !attested() {
		t.Fatal("reissued: not attested")
	}
	// the node revoked, its key not yet deleted: membership refuses it
	mu.Lock()
	state[fwdNode] = "revoked"
	mu.Unlock()
	if c := home.serve(nodeRequest(t, "192.168.1.77", key)); c != 401 {
		t.Fatalf("revoked node: %d", c)
	}
	// the row deleted
	mu.Lock()
	delete(state, fwdNode)
	mu.Unlock()
	if c := home.serve(nodeRequest(t, "192.168.1.77", key)); c != 401 {
		t.Fatalf("deleted node: %d", c)
	}
}

// Over its authenticated link a machine's word is its own: the forwarded client is taken
// without an HMAC. A malformed one is not.
func TestForwardedClientOverTheLink(t *testing.T) {
	home := newFwdHome(&Verifier{})
	for in, want := range map[string]string{"192.168.1.77": "192.168.1.77", "x": nodeAddr, "": nodeAddr} {
		r := httptest.NewRequest("GET", "http://home/api/videos", nil)
		if in != "" {
			r.Header.Set(HeaderForwardedClient, in)
		}
		r.Header.Set("X-Forwarded-For", "10.1.1.1")
		r = r.WithContext(linkctx.WithNode(r.Context(), fwdNode))
		if c := home.serve(r); c != 204 || home.client != want || home.node != fwdNode || home.hdr != "" {
			t.Fatalf("link %q: %d %q", in, c, home.client)
		}
	}
}

// The signer drops a forwarded client that is not one canonical address rather than send
// it unsigned, and never passes on an HMAC it did not make.
func TestSignRequestForwardedClient(t *testing.T) {
	key := Key{ID: "dk_1", Secret: []byte("k")}
	for _, bad := range []string{"nas.local", "fe80::1%eth0", " 1.2.3.4"} {
		r := httptest.NewRequest("GET", "http://home/", nil)
		r.Header.Set(HeaderForwardedClient, bad)
		r.Header.Set(HeaderForwardedClientSig, "forged")
		if err := SignRequest(r, fwdNode, key, time.Now()); err != nil {
			t.Fatal(err)
		}
		if r.Header.Get(HeaderForwardedClient) != "" || r.Header.Get(HeaderForwardedClientSig) != "" {
			t.Fatalf("%q kept", bad)
		}
	}
	r := httptest.NewRequest("GET", "http://home/", nil)
	r.Header.Set(HeaderForwardedClientSig, "forged")
	_ = SignRequest(r, fwdNode, key, time.Now())
	if r.Header.Get(HeaderForwardedClientSig) != "" {
		t.Fatal("a stray HMAC is passed on")
	}
}
