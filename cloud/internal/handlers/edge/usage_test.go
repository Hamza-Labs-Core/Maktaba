package edge

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// fakeStore takes each window once, by its billing key, and answers one state per publisher.
type fakeStore struct {
	mu     sync.Mutex
	taken  map[string]billingpkg.UsageWindow
	states map[string]billingpkg.PublisherState
}

func newStore(states ...billingpkg.PublisherState) *fakeStore {
	s := &fakeStore{taken: map[string]billingpkg.UsageWindow{}, states: map[string]billingpkg.PublisherState{}}
	for _, st := range states {
		s.states[st.Publisher] = st
	}
	return s
}

func (s *fakeStore) RecordWindow(_ context.Context, w billingpkg.UsageWindow) (bool, error) {
	if err := w.Valid(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.states[w.Publisher]; !ok {
		return false, billingpkg.ErrUnknownPublisher
	}
	k := billingpkg.WindowKey(w.Publisher, w.Window)
	if _, ok := s.taken[k]; ok {
		return true, nil
	}
	s.taken[k] = w
	return false, nil
}

func (s *fakeStore) State(_ context.Context, id string) (billingpkg.PublisherState, error) {
	st, ok := s.states[id]
	if !ok {
		return st, billingpkg.ErrUnknownPublisher
	}
	return st, nil
}

// vectors is the part of internal/broadcast's shared vectors (the edge's tests read the same
// file) this package checks: a usage post exactly as the edge signs it, and the cloud's reply.
type vectors struct {
	Edge struct {
		Public, Method, Path, Body, Signature string
		Ms                                    int64
		UsageReply                            string `json:"usage_reply"`
	} `json:"edge"`
	State billingpkg.PublisherState `json:"state"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("../../broadcast/testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func serve(d *Deps, req *http.Request) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	Mount(r, d)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func signedAt(priv ed25519.PrivateKey, method, path string, body []byte, at time.Time) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	broadcast.SignEdgeRequest(priv, req.Header, method, path, at.UnixMilli(), body)
	return req
}

func signed(priv ed25519.PrivateKey, body []byte, at time.Time) *http.Request {
	return signedAt(priv, http.MethodPost, broadcast.UsagePath, body, at)
}

// The edge's own request, byte for byte from the shared vectors, is accepted: its signature,
// its body's shape and its key; the reply is the vectors' reply, byte for byte.
func TestEdgeVectorRequestAccepted(t *testing.T) {
	v := loadVectors(t)
	pub, err := base64.RawURLEncoding.DecodeString(v.Edge.Public)
	if err != nil {
		t.Fatal(err)
	}
	store := newStore(v.State)
	d := &Deps{Store: store, Key: pub, Now: func() time.Time { return time.UnixMilli(v.Edge.Ms).Add(4 * time.Minute) }}
	req := httptest.NewRequest(v.Edge.Method, v.Edge.Path, strings.NewReader(v.Edge.Body))
	req.Header.Set(broadcast.HeaderEdgeTime, fmt.Sprint(v.Edge.Ms))
	req.Header.Set(broadcast.HeaderEdgeSignature, v.Edge.Signature)
	rec := serve(d, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the edge's request: %d %s", rec.Code, rec.Body)
	}
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != v.Edge.UsageReply {
		t.Fatalf("reply\n got %s\nwant %s", got, v.Edge.UsageReply)
	}
	if len(store.taken) != 1 {
		t.Fatalf("taken %+v", store.taken)
	}
	for k, w := range store.taken {
		if k != v.State.Publisher+":2026-11-12T23:55:00Z" || w.MinutesFull != 12 || w.MinutesLowest != 3 {
			t.Fatalf("billed %s %+v", k, w)
		}
	}
	// the same request again is a duplicate, counted once
	req = httptest.NewRequest(v.Edge.Method, v.Edge.Path, strings.NewReader(v.Edge.Body))
	req.Header.Set(broadcast.HeaderEdgeTime, fmt.Sprint(v.Edge.Ms))
	req.Header.Set(broadcast.HeaderEdgeSignature, v.Edge.Signature)
	var out broadcast.UsageReply
	if rec := serve(d, req); json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Duplicates) != 1 || len(out.Taken) != 0 || len(store.taken) != 1 {
		t.Fatalf("again: %s", rec.Body)
	}
	// 6 minutes on, the same signature is too old
	d.Now = func() time.Time { return time.UnixMilli(v.Edge.Ms).Add(6 * time.Minute) }
	req = httptest.NewRequest(v.Edge.Method, v.Edge.Path, strings.NewReader(v.Edge.Body))
	req.Header.Set(broadcast.HeaderEdgeTime, fmt.Sprint(v.Edge.Ms))
	req.Header.Set(broadcast.HeaderEdgeSignature, v.Edge.Signature)
	if rec := serve(d, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("past the skew: %d", rec.Code)
	}
}

// Every edge request is signed with the edge's key and fresh; without a key the routes are off.
func TestEdgeSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	d := &Deps{Store: newStore(), Key: pub, Now: func() time.Time { return now }}
	empty := []byte(`{"edge":"e1","windows":[]}`)

	if rec := serve(&Deps{Store: d.Store}, signed(priv, empty, now)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no key: %d", rec.Code)
	}
	if rec := serve(d, signed(other, empty, now)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("another key: %d", rec.Code)
	}
	if rec := serve(d, signed(priv, empty, now.Add(-6*time.Minute))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an old time: %d", rec.Code)
	}
	if rec := serve(d, signed(priv, empty, now.Add(6*time.Minute))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a future time: %d", rec.Code)
	}
	// the old format (unix seconds, X-Maktaba-Edge-Timestamp) is not accepted
	old := httptest.NewRequest(http.MethodPost, broadcast.UsagePath, bytes.NewReader(empty))
	old.Header.Set("X-Maktaba-Edge-Timestamp", fmt.Sprint(now.Unix()))
	old.Header.Set(broadcast.HeaderEdgeSignature, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, empty)))
	if rec := serve(d, old); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the old headers: %d", rec.Code)
	}
	// the body is part of what is signed
	altered := httptest.NewRequest(http.MethodPost, broadcast.UsagePath, strings.NewReader(`{"edge":"e1","windows":[{}]}`))
	altered.Header = signed(priv, empty, now).Header
	if rec := serve(d, altered); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an altered body: %d", rec.Code)
	}
	// so are the method and the path: a usage signature does not open a publisher's state
	moved := httptest.NewRequest(http.MethodGet, "/_edge/publishers/x", nil)
	moved.Header = signedAt(priv, http.MethodGet, broadcast.UsagePath, nil, now).Header
	if rec := serve(d, moved); rec.Code != http.StatusUnauthorized {
		t.Fatalf("another path: %d", rec.Code)
	}
	rec := serve(d, signed(priv, empty, now))
	if rec.Code != http.StatusOK {
		t.Fatalf("a good post: %d %s", rec.Code, rec.Body)
	}
	var out broadcast.UsageReply
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Taken == nil || out.States == nil || out.Plans == nil {
		t.Fatalf("reply: %s (%v)", rec.Body, err)
	}
}

// At most 2,000 windows in a post, and 2,000 of the largest windows fit the body limit.
func TestEdgeUsageCap(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	d := &Deps{Store: newStore(), Key: pub, Now: func() time.Time { return now }}
	big := func(n int) []byte {
		ws := make([]broadcast.UsageWindow, n)
		start := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
		p := "6f1c0a52-0d3e-4c55-9a51-3b1f0e7d2a10"
		for i := range ws {
			s := start.Add(time.Duration(i) * billingpkg.WindowLength)
			ws[i] = broadcast.UsageWindow{Key: billingpkg.WindowKey(p, s), Publisher: p, WindowStart: s, WindowEnd: s.Add(billingpkg.WindowLength),
				MinutesFull: 100_000, MinutesLowest: 100_000, MinutesStream: 100_000, SegmentsIn: math.MaxInt32, BytesIn: math.MaxInt64, PeakSessions: math.MaxInt32}
		}
		b, _ := json.Marshal(broadcast.UsagePost{Edge: "broadcast-edge", Windows: ws})
		return b
	}
	full := big(MaxWindows)
	if len(full) > MaxBody {
		t.Fatalf("2,000 windows are %d bytes, over the %d limit", len(full), MaxBody)
	}
	// every window here names an unknown publisher: rejected, never a 400 or 413
	var out broadcast.UsageReply
	if rec := serve(d, signed(priv, full, now)); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Rejected) != MaxWindows {
		t.Fatalf("2,000 windows: %d", rec.Code)
	}
	if rec := serve(d, signed(priv, big(MaxWindows+1), now)); rec.Code != http.StatusBadRequest {
		t.Fatalf("2,001 windows: %d", rec.Code)
	}
}

// Windows whose key, end or counters disagree with their start, or that name a publisher the
// cloud doesn't know, are rejected under the key the edge sent (so it drops them); the good
// ones in the same post are taken; each publisher's plan comes back.
func TestEdgeUsageWindows(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	p := "6f1c0a52-0d3e-4c55-9a51-3b1f0e7d2a10"
	st := billingpkg.PublisherState{Publisher: p, Mode: "full", MaxRungs: 3, At: now}
	store := newStore(st)
	d := &Deps{Store: store, Key: pub, Now: func() time.Time { return now }}
	start := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	win := func(i int) broadcast.UsageWindow {
		s := start.Add(time.Duration(i) * billingpkg.WindowLength)
		return broadcast.UsageWindow{Key: billingpkg.WindowKey(p, s), Publisher: p, WindowStart: s, WindowEnd: s.Add(billingpkg.WindowLength), MinutesFull: 1}
	}
	ms := win(1)
	ms.Key = p + ":" + ms.WindowStart.Format("2006-01-02T15:04:05.000Z")
	long := win(2)
	long.WindowEnd = long.WindowEnd.Add(time.Minute)
	stranger := win(3)
	stranger.Publisher = "00000000-0000-0000-0000-000000000000"
	stranger.Key = billingpkg.WindowKey(stranger.Publisher, stranger.WindowStart)
	body, _ := json.Marshal(broadcast.UsagePost{Edge: "e1", Windows: []broadcast.UsageWindow{win(0), ms, long, stranger, win(0)}})
	rec := serve(d, signed(priv, body, now))
	var out broadcast.UsageReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := map[string]string{ms.Key: "bad_window", long.Key: "bad_window", stranger.Key: "unknown_publisher"}
	if len(out.Taken) != 1 || out.Taken[0] != win(0).Key || len(out.Duplicates) != 1 || len(out.Rejected) != 3 {
		t.Fatalf("reply %+v", out)
	}
	for _, r := range out.Rejected {
		if want[r.Key] != r.Error {
			t.Errorf("rejected %+v", r)
		}
	}
	if len(out.Plans) != 1 || out.Plans[0] != broadcast.PlanFromState(st) {
		t.Fatalf("plans %+v", out.Plans)
	}
}
