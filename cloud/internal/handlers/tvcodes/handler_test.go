package tvcodes

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/homes"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// memStore is Store in memory, with the SQL store's rules (decide is shared).
type memStore struct {
	mu   sync.Mutex
	rows map[string]*memRow
}

type memRow struct {
	Row
	hash      []byte
	serverID  string
	payload   []byte
	claimed   bool
	delivered bool
	lastPoll  sql.NullTime
}

func (m *memStore) Create(_ context.Context, r Row, h []byte, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string]*memRow{}
	}
	if _, ok := m.rows[r.Code]; ok {
		return ErrExists
	}
	m.rows[r.Code] = &memRow{Row: r, hash: h}
	return nil
}

func (m *memStore) Open(_ context.Context, code string, now time.Time) (Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[code]
	if !ok || r.claimed || !now.Before(r.ExpiresAt) {
		return Row{}, ErrNotFound
	}
	return r.Row, nil
}

func (m *memStore) Claim(_ context.Context, code, serverID string, sealed []byte, now time.Time) (Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[code]
	if !ok || !now.Before(r.ExpiresAt) {
		return Row{}, ErrNotFound
	}
	if r.claimed {
		return Row{}, ErrClaimed
	}
	r.claimed, r.serverID, r.payload = true, serverID, sealed
	return r.Row, nil
}

func (m *memStore) Poll(_ context.Context, code string, h []byte, now time.Time, iv time.Duration) (PollResult, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[code]
	if !ok || subtle.ConstantTimeCompare(r.hash, h) != 1 {
		return PollNotFound, nil, nil
	}
	res := decide(now, iv, r.ExpiresAt, r.claimed, r.delivered, r.lastPoll, r.payload != nil)
	if res == PollGone {
		return res, nil, nil
	}
	r.lastPoll = sql.NullTime{Time: now, Valid: true}
	if res == PollDelivered {
		p := r.payload
		r.payload, r.delivered = nil, true
		return res, p, nil
	}
	return res, nil, nil
}

func (m *memStore) Sweep(_ context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for c, r := range m.rows {
		if r.ExpiresAt.Before(cutoff) {
			delete(m.rows, c)
			n++
		}
	}
	return n, nil
}

const homeID = "11111111-2222-3333-4444-555555555555"

type fakeServers struct{}

func (fakeServers) VerifySecret(_ context.Context, id, secret string) error {
	if id != homeID || secret != "good" {
		return errors.New("bad secret")
	}
	return nil
}

func (fakeServers) ByID(_ context.Context, id string) (stores.Server, error) {
	if id != homeID {
		return stores.Server{}, stores.ErrServerNotFound
	}
	return stores.Server{ID: id, Name: "Darwish home", Slug: "darwish"}, nil
}

type fakeReach struct{}

func (fakeReach) ReachFor(_ context.Context, hs []stores.Server) map[string]homes.Reach {
	out := map[string]homes.Reach{}
	for _, h := range hs {
		out[h.ID] = homes.Reach{Connections: []homes.Connection{
			{URL: "https://192-168-1-20.h.maktaba.tv:8443", Kind: "local"},
			{URL: "https://192.168.1.20:8443", Kind: "local_ip", TLSName: "192-168-1-20.h.maktaba.tv"},
			{URL: "https://darwish.relay.maktaba.tv", Kind: "relay"},
		}}
	}
	return out
}

type rig struct {
	t     *testing.T
	now   time.Time
	store *memStore
	deps  *Deps
	h     http.Handler
}

func newRig(t *testing.T) *rig {
	t.Helper()
	box, err := staffauth.NewBox(BoxKey("a-token-secret-for-tests"))
	if err != nil {
		t.Fatal(err)
	}
	rg := &rig{t: t, now: time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC), store: &memStore{}}
	rg.deps = &Deps{Store: rg.store, Servers: fakeServers{}, Reach: fakeReach{}, Box: box,
		Trusted: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/16")}, Now: func() time.Time { return rg.now }}
	r := chi.NewRouter()
	MountPublic(r, rg.deps)
	MountAgent(r, rg.deps)
	rg.h = r
	return rg
}

func (rg *rig) do(method, path, ip string, body any, auth bool) *httptest.ResponseRecorder {
	rg.t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	if ip == "" {
		ip = "203.0.113.7"
	}
	req.RemoteAddr = ip + ":40000"
	req.Header.Set("CF-IPCountry", "JO")
	if auth {
		req.SetBasicAuth(homeID, "good")
	}
	w := httptest.NewRecorder()
	rg.h.ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func (rg *rig) create(ip string) createResp {
	rg.t.Helper()
	w := rg.do("POST", "/v1/tv/codes", ip, map[string]string{"platform": "tizen", "app_version": "1.0.0"}, false)
	if w.Code != http.StatusCreated {
		rg.t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	return decode[createResp](rg.t, w)
}

func (rg *rig) poll(c createResp) *httptest.ResponseRecorder {
	return rg.do("POST", "/v1/tv/codes/poll", "", pollReq{Code: c.Code, PollSecret: c.PollSecret}, false)
}

// T01: create, poll pending, slow_down, approve, delivered once, then 410; expiry.
func TestCodeLifecycle(t *testing.T) {
	rg := newRig(t)
	c := rg.create("10.9.0.5") // through the trusted edge: the country is believed
	if len(Normalize(c.Code)) != CodeLen || c.Code[4] != '-' || !Valid(Normalize(c.Code)) {
		t.Fatalf("code %q", c.Code)
	}
	if strings.ContainsAny(c.Code, "01ILOU") {
		t.Fatalf("code %q uses a character a TV shows ambiguously", c.Code)
	}
	if c.ExpiresIn != 600 || c.Interval != 5 || c.PollSecret == "" {
		t.Fatalf("create: %+v", c)
	}
	if c.VerifyURL != "https://maktaba.tv/tv?c="+Normalize(c.Code) {
		t.Fatalf("verify url %q", c.VerifyURL)
	}

	if w := rg.poll(c); w.Code != http.StatusAccepted || decode[map[string]any](t, w)["status"] != "pending" {
		t.Fatalf("first poll: %d %s", w.Code, w.Body.String())
	}
	rg.now = rg.now.Add(2 * time.Second)
	if w := rg.poll(c); w.Code != http.StatusTooManyRequests || decode[map[string]any](t, w)["error"] != "slow_down" {
		t.Fatalf("quick poll: %d %s", w.Code, w.Body.String())
	}
	rg.now = rg.now.Add(5 * time.Second)
	if w := rg.poll(c); w.Code != http.StatusAccepted {
		t.Fatalf("poll after the interval: %d %s", w.Code, w.Body.String())
	}
	// the wrong secret learns nothing
	if w := rg.do("POST", "/v1/tv/codes/poll", "", pollReq{Code: c.Code, PollSecret: "nope"}, false); w.Code != http.StatusNotFound {
		t.Fatalf("wrong secret: %d", w.Code)
	}

	// the home reads it, then claims it
	if w := rg.do("GET", "/v1/homes/"+homeID+"/tv-codes/"+c.Code, "", nil, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("lookup without the secret: %d", w.Code)
	}
	w := rg.do("GET", "/v1/homes/"+homeID+"/tv-codes/"+strings.ToLower(c.Code), "", nil, true)
	if w.Code != http.StatusOK {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	if v := decode[lookupResp](t, w); v.Platform != "tizen" || v.Country != "JO" || v.AppVersion != "1.0.0" {
		t.Fatalf("lookup: %+v", v)
	}
	grant := "AAAAAABBBBBBCCCCCCDDDDDDEEEEEE"
	if w := rg.do("POST", "/v1/homes/"+homeID+"/tv-codes/"+c.Code+"/claim", "", claimReq{Grant: grant}, true); w.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", w.Code, w.Body.String())
	}
	if w := rg.do("POST", "/v1/homes/"+homeID+"/tv-codes/"+c.Code+"/claim", "", claimReq{Grant: grant}, true); w.Code != http.StatusConflict {
		t.Fatalf("second claim: %d", w.Code)
	}
	if w := rg.do("GET", "/v1/homes/"+homeID+"/tv-codes/"+c.Code, "", nil, true); w.Code != http.StatusNotFound {
		t.Fatalf("lookup of a claimed code: %d", w.Code)
	}
	// the payload is sealed at rest, not the grant in clear
	rg.store.mu.Lock()
	sealed := rg.store.rows[Normalize(c.Code)].payload
	rg.store.mu.Unlock()
	if bytes.Contains(sealed, []byte(grant)) {
		t.Fatal("the payload is stored in clear")
	}

	// delivered at once, even sooner than the interval
	rg.now = rg.now.Add(time.Second)
	w = rg.poll(c)
	if w.Code != http.StatusOK {
		t.Fatalf("approved poll: %d %s", w.Code, w.Body.String())
	}
	p := decode[pollResp](t, w)
	if p.Status != "approved" || p.Grant != grant || p.HomeID != homeID || p.HomeName != "Darwish home" ||
		len(p.Connections) != 3 || p.Connections[0].Kind != "local" || p.Connections[2].Kind != "relay" {
		t.Fatalf("payload: %+v", p)
	}
	// once: then 410 with no secret
	rg.now = rg.now.Add(10 * time.Second)
	w = rg.poll(c)
	if w.Code != http.StatusGone || strings.Contains(w.Body.String(), grant) {
		t.Fatalf("poll after delivery: %d %s", w.Code, w.Body.String())
	}
	rg.store.mu.Lock()
	left := rg.store.rows[Normalize(c.Code)].payload
	rg.store.mu.Unlock()
	if left != nil {
		t.Fatal("the payload is kept after delivery")
	}
}

func TestCodeExpires(t *testing.T) {
	rg := newRig(t)
	c := rg.create("")
	rg.now = rg.now.Add(CodeTTL)
	if w := rg.poll(c); w.Code != http.StatusGone {
		t.Fatalf("expired poll: %d", w.Code)
	}
	if w := rg.do("POST", "/v1/homes/"+homeID+"/tv-codes/"+c.Code+"/claim", "", claimReq{Grant: "AAAAAABBBBBBCCCCCCDDDD"}, true); w.Code != http.StatusNotFound {
		t.Fatalf("claim of an expired code: %d", w.Code)
	}
	// an untrusted peer's country header is ignored
	rg.now = rg.now.Add(time.Minute)
	c2 := rg.create("198.51.100.4")
	w := rg.do("GET", "/v1/homes/"+homeID+"/tv-codes/"+c2.Code, "", nil, true)
	if v := decode[lookupResp](t, w); v.Country != "" {
		t.Fatalf("country from an untrusted peer: %q", v.Country)
	}
	// the sweep deletes rows an hour past expiry, no sooner
	if n, _ := rg.store.Sweep(context.Background(), rg.now.Add(-SweepAfter)); n != 0 {
		t.Fatalf("swept %d too early", n)
	}
	if n, _ := rg.store.Sweep(context.Background(), rg.now.Add(SweepAfter+CodeTTL)); n != 2 {
		t.Fatalf("swept %d, want 2", n)
	}
}

// Two TVs: approving one never approves the other.
func TestTwoCodesApart(t *testing.T) {
	rg := newRig(t)
	a, b := rg.create(""), rg.create("")
	if a.Code == b.Code {
		t.Fatal("same code twice")
	}
	if w := rg.do("POST", "/v1/homes/"+homeID+"/tv-codes/"+a.Code+"/claim", "", claimReq{Grant: "AAAAAABBBBBBCCCCCCDDDD"}, true); w.Code != http.StatusOK {
		t.Fatalf("claim: %d", w.Code)
	}
	if w := rg.poll(b); w.Code != http.StatusAccepted {
		t.Fatalf("the other TV: %d", w.Code)
	}
	if w := rg.poll(a); w.Code != http.StatusOK {
		t.Fatalf("the approved TV: %d", w.Code)
	}
}

// T05: the 11th code from one address is refused; another address still gets one.
func TestCodesPerAddress(t *testing.T) {
	rg := newRig(t)
	for i := range MaxOpenPerIP {
		w := rg.do("POST", "/v1/tv/codes", "203.0.113.9", createReq{Platform: "webos"}, false)
		if w.Code != http.StatusCreated {
			t.Fatalf("code %d: %d", i+1, w.Code)
		}
	}
	if w := rg.do("POST", "/v1/tv/codes", "203.0.113.9", createReq{Platform: "webos"}, false); w.Code != http.StatusTooManyRequests {
		t.Fatalf("11th code: %d", w.Code)
	}
	if w := rg.do("POST", "/v1/tv/codes", "203.0.113.10", createReq{Platform: "webos"}, false); w.Code != http.StatusCreated {
		t.Fatalf("another address: %d", w.Code)
	}
}

func TestBadInput(t *testing.T) {
	rg := newRig(t)
	c := rg.create("")
	for _, g := range []string{"", "short", strings.Repeat("a", 200), "has spaces in it......"} {
		if w := rg.do("POST", "/v1/homes/"+homeID+"/tv-codes/"+c.Code+"/claim", "", claimReq{Grant: g}, true); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("grant %q: %d", g, w.Code)
		}
	}
	if w := rg.do("POST", "/v1/tv/codes/poll", "", pollReq{Code: "0000-0000", PollSecret: "x"}, false); w.Code != http.StatusNotFound {
		t.Fatalf("code outside the alphabet: %d", w.Code)
	}
	long := strings.Repeat("é", 40)
	w := rg.do("POST", "/v1/tv/codes", "", createReq{Platform: long + "\x01"}, false)
	created := decode[createResp](t, w)
	lw := rg.do("GET", "/v1/homes/"+homeID+"/tv-codes/"+created.Code, "", nil, true)
	if v := decode[lookupResp](t, lw); len(v.Platform) > 32 || strings.ContainsRune(v.Platform, 1) {
		t.Fatalf("platform kept as %q", v.Platform)
	}
}

// A home trying codes it was not given runs out of lookups; someone without its secret spends
// none of them.
func TestAHomesLookupsAreCapped(t *testing.T) {
	rg := newRig(t)
	path := "/v1/homes/" + homeID + "/tv-codes/ABCD2345"
	for range 40 {
		req := httptest.NewRequest("GET", path, nil)
		req.SetBasicAuth(homeID, "wrong")
		w := httptest.NewRecorder()
		rg.h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong secret: %d", w.Code)
		}
	}
	for i := range 30 {
		if w := rg.do("GET", path, "", nil, true); w.Code != http.StatusNotFound {
			t.Fatalf("lookup %d: %d", i+1, w.Code)
		}
	}
	w := rg.do("GET", path, "", nil, true)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("lookup 31: %d %v", w.Code, w.Header())
	}
	if w := rg.do("POST", path+"/claim", "", map[string]string{"grant": "aaaaaaaaaaaaaaaaaaaaaa"}, true); w.Code != http.StatusTooManyRequests {
		t.Fatalf("a claim over the cap: %d", w.Code)
	}
}
