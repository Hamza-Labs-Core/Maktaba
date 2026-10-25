package delegation

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/linkctx"
)

// Over its link a machine needs no signature (its certificate proved it); a signed
// request over plain HTTP is refused once the home requires the link (story 35.16).
func TestAcceptOverTheLinkAndLinkRequired(t *testing.T) {
	const node = "0b6f3c1e-2a4d-4e8f-9c71-5d2a8b3e6f10"
	var got string
	var cookie string
	h := Accept(&Verifier{}, "mkt_sess")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = NodeFrom(r.Context())
		cookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/videos", nil)
	req.Header.Set(HeaderDelegatedSession, "sess-1")
	req = req.WithContext(linkctx.WithNode(req.Context(), node))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || got != node || !strings.Contains(cookie, "mkt_sess=sess-1") {
		t.Fatalf("over the link: %d node %q cookie %q", rec.Code, got, cookie)
	}

	t.Cleanup(func() { linkctx.SetMode(linkctx.ModePrefer) })
	linkctx.SetMode(linkctx.ModeRequire)
	req = httptest.NewRequest(http.MethodGet, "/api/videos", nil)
	if err := SignRequest(req, node, Key{ID: "dk_1", Secret: make([]byte, KeySize), NotAfter: time.Now().Add(time.Hour)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), ReasonLinkRequired) {
		t.Fatalf("a signed request in require: %d %s", rec.Code, rec.Body.String())
	}
}
