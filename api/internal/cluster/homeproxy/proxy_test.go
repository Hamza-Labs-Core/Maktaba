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
