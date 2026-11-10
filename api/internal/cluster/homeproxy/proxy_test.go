package homeproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
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
	var xff string
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xff = r.Header.Get("X-Forwarded-For")
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
		p.ServeHTTP(httptest.NewRecorder(), req)
		if xff != want {
			t.Errorf("from %s: the home got X-Forwarded-For %q, want %q", remote, xff, want)
		}
	}
}
