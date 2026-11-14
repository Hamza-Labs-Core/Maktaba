package edge

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
)

func serve(d *Deps, req *http.Request) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	Mount(r, d)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func signed(t *testing.T, priv ed25519.PrivateKey, body []byte, at time.Time) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/_edge/usage", bytes.NewReader(body))
	Sign(priv, req, body, at)
	return req
}

// Every edge request is signed with the edge's key and fresh; without a key the routes are off.
func TestEdgeSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	d := &Deps{Store: &billingpkg.BroadcastStore{}, Key: pub, Now: func() time.Time { return now }}
	empty := []byte(`{"edge":"e1","windows":[]}`)

	if rec := serve(&Deps{Store: d.Store}, signed(t, priv, empty, now)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no key: %d", rec.Code)
	}
	if rec := serve(d, signed(t, other, empty, now)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("another key: %d", rec.Code)
	}
	if rec := serve(d, signed(t, priv, empty, now.Add(-6*time.Minute))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an old timestamp: %d", rec.Code)
	}
	if rec := serve(d, signed(t, priv, empty, now.Add(6*time.Minute))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a future timestamp: %d", rec.Code)
	}
	// the body is part of what is signed
	req := signed(t, priv, empty, now)
	req.Body = http.NoBody
	altered := httptest.NewRequest(http.MethodPost, "/_edge/usage", strings.NewReader(`{"edge":"e1","windows":[{}]}`))
	altered.Header = req.Header
	if rec := serve(d, altered); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an altered body: %d", rec.Code)
	}
	rec := serve(d, signed(t, priv, empty, now))
	if rec.Code != http.StatusOK {
		t.Fatalf("a good post: %d %s", rec.Code, rec.Body)
	}
	var out UsageReply
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Taken == nil || out.States == nil {
		t.Fatalf("reply: %s (%v)", rec.Body, err)
	}
	// too many windows in one post
	many, _ := json.Marshal(UsagePost{Windows: make([]billingpkg.UsageWindow, MaxWindows+1)})
	if rec := serve(d, signed(t, priv, many, now)); rec.Code != http.StatusBadRequest {
		t.Fatalf("too many windows: %d", rec.Code)
	}
}
