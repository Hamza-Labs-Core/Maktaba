package delegation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

type vectorFile struct {
	Key   string `json:"key_utf8"`
	Cases []struct {
		Name        string `json:"name"`
		Method      string `json:"method"`
		PathQuery   string `json:"path_query"`
		Ts          int64  `json:"ts"`
		Nonce       string `json:"nonce"`
		Body        string `json:"body"`
		Unsigned    bool   `json:"unsigned"`
		Session     string `json:"delegated_session"`
		BodyHash    string `json:"body_sha256"`
		SessionHash string `json:"session_hash"`
		Canonical   string `json:"canonical"`
		Sig         string `json:"sig"`
	} `json:"cases"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "testdata", "cluster", "delegation_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// TC1: the signer (node) and the verifier (home) agree with the shared vectors.
func TestDelegationSignatureVectors(t *testing.T) {
	f := loadVectors(t)
	key := []byte(f.Key)
	for _, c := range f.Cases {
		bh := EmptyBodyHash
		if c.Body != "" {
			bh = hexSHA256([]byte(c.Body))
		}
		if c.Unsigned {
			bh = UnsignedPayload
		}
		if bh != c.BodyHash || SessionHash(c.Session) != c.SessionHash {
			t.Fatalf("%s: hashes differ", c.Name)
		}
		if got := Canonical(c.Method, c.PathQuery, c.Ts, c.Nonce, bh, c.SessionHash); got != c.Canonical {
			t.Fatalf("%s: canonical %q, want %q", c.Name, got, c.Canonical)
		}
		if got := Sign(key, c.Method, c.PathQuery, c.Ts, c.Nonce, bh, c.SessionHash); got != c.Sig {
			t.Fatalf("%s: sig %s, want %s", c.Name, got, c.Sig)
		}
		// the whole request path: SignRequest on a node, Verify on the home
		var body io.Reader
		if c.Body != "" {
			body = strings.NewReader(c.Body)
		}
		r := httptest.NewRequest(c.Method, "http://home:8080"+c.PathQuery, body)
		if c.Session != "" {
			r.Header.Set(HeaderDelegatedSession, c.Session)
		}
		if c.Unsigned {
			r.Header.Set("Upgrade", "websocket")
		}
		nodeID := "11111111-2222-3333-4444-555555555555"
		testNonce = true
		r.Header.Set(HeaderNonce, c.Nonce)
		err := SignRequest(r, nodeID, Key{ID: "dk_1", Secret: key}, time.Unix(c.Ts, 0))
		testNonce = false
		if err != nil {
			t.Fatal(err)
		}
		if r.Header.Get(HeaderSig) != c.Sig {
			t.Fatalf("%s: SignRequest sig %s, want %s", c.Name, r.Header.Get(HeaderSig), c.Sig)
		}
		v := &Verifier{Keys: staticKeys{nodeID: {"dk_1": key}}, Now: func() time.Time { return time.Unix(c.Ts, 0) }}
		if got, err := v.Verify(r); err != nil || got != nodeID {
			t.Fatalf("%s: verify = %q %v", c.Name, got, err)
		}
	}
}

type staticKeys map[string]map[string][]byte

func (s staticKeys) Lookup(_ context.Context, node, key string) ([]byte, bool) {
	k, ok := s[node][key]
	return k, ok
}

func signed(t *testing.T, method, url, body, session string, key []byte, at time.Time) *http.Request {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, url, rd)
	if session != "" {
		r.Header.Set(HeaderDelegatedSession, session)
	}
	if err := SignRequest(r, "11111111-2222-3333-4444-555555555555", Key{ID: "dk_1", Secret: key}, at); err != nil {
		t.Fatal(err)
	}
	return r
}

func reason(err error) string {
	if re, ok := err.(*RejectError); ok {
		return re.Reason
	}
	return ""
}

// TC2: skew, replay, revoked / unknown nodes, a body swapped after signing, a session
// swapped after signing, a forged node id.
func TestDelegationRejectsSkewReplayRevoked(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	node := "11111111-2222-3333-4444-555555555555"
	now := time.Unix(1790000000, 0)
	member := true
	v := &Verifier{Keys: staticKeys{node: {"dk_1": key}}, Now: func() time.Time { return now },
		Member: func(context.Context, string) bool { return member }}

	r := signed(t, "POST", "http://home/api/x", `{"a":1}`, "", key, now)
	if _, err := v.Verify(r); err != nil {
		t.Fatalf("first: %v", err)
	}
	// the same request again, signed anew (a page loading it twice): accepted
	r2 := signed(t, "POST", "http://home/api/x", `{"a":1}`, "", key, now)
	if _, err := v.Verify(r2); err != nil {
		t.Fatalf("a second identical request: %v", err)
	}
	// the first one replayed (its headers and body copied): refused
	replay := httptest.NewRequest("POST", "http://home/api/x", strings.NewReader(`{"a":1}`))
	replay.Header = r.Header.Clone()
	if _, err := v.Verify(replay); reason(err) != ReasonReplay {
		t.Fatalf("replay: %v", err)
	}
	old := signed(t, "GET", "http://home/api/y", "", "", key, now.Add(-61*time.Second))
	if _, err := v.Verify(old); reason(err) != ReasonSkew {
		t.Fatalf("skew: %v", err)
	}
	swapped := signed(t, "POST", "http://home/api/z", `{"a":1}`, "", key, now)
	swapped.Body = io.NopCloser(strings.NewReader(`{"a":2}`))
	if _, err := v.Verify(swapped); reason(err) != ReasonBadSig {
		t.Fatalf("body swap: %v", err)
	}
	sess := signed(t, "GET", "http://home/api/w", "", "sid-a", key, now)
	sess.Header.Set(HeaderDelegatedSession, "sid-b")
	if _, err := v.Verify(sess); reason(err) != ReasonBadSig {
		t.Fatalf("session swap: %v", err)
	}
	forged := signed(t, "GET", "http://home/api/v", "", "", key, now)
	forged.Header.Set(HeaderNode, "99999999-2222-3333-4444-555555555555")
	if _, err := v.Verify(forged); reason(err) != ReasonUnknownKey {
		t.Fatalf("forged node: %v", err)
	}
	missing := httptest.NewRequest("GET", "http://home/api/v", nil)
	missing.Header.Set(HeaderVia, ViaNode)
	if _, err := v.Verify(missing); reason(err) != ReasonMissing {
		t.Fatalf("missing: %v", err)
	}
	member = false
	revoked := signed(t, "GET", "http://home/api/u", "", "", key, now)
	if _, err := v.Verify(revoked); reason(err) != ReasonUnknownNode {
		t.Fatalf("revoked: %v", err)
	}
	member = true
	// an unsigned body only for uploads and upgrades
	up := httptest.NewRequest("POST", "http://home/api/upload", strings.NewReader("x"))
	up.ContentLength = -1
	up.Header.Set("Content-Type", "application/json")
	if err := SignRequest(up, node, Key{ID: "dk_1", Secret: key}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(up); reason(err) != ReasonUnsigned {
		t.Fatalf("unsigned json body: %v", err)
	}
}

func TestAcceptMiddleware(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	node := "11111111-2222-3333-4444-555555555555"
	now := time.Now()
	v := &Verifier{Keys: staticKeys{node: {"dk_1": key}}}
	var gotNode, gotCookie, gotVia string
	h := Accept(v, "mkt_sess")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotNode = NodeFrom(r.Context())
		if c, err := r.Cookie("mkt_sess"); err == nil {
			gotCookie = c.Value
		}
		gotVia = r.Header.Get(HeaderVia)
	}))
	// unsigned, plain browser request: untouched
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/a", nil))
	if w.Code != 200 || gotNode != "" {
		t.Fatalf("plain: %d %q", w.Code, gotNode)
	}
	// a forged Via without a signature: 401
	r := httptest.NewRequest("GET", "/api/a", nil)
	r.Header.Set(HeaderVia, ViaNode)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("forged via: %d", w.Code)
	}
	// signed with a delegated session: the cookie is the session, Via stripped
	r = signed(t, "GET", "/api/a", "", "sid-1", key, now)
	r.Header.Set(HeaderVia, ViaNode)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || gotNode != node || gotCookie != "sid-1" || gotVia != "" {
		t.Fatalf("signed: %d node=%q cookie=%q via=%q", w.Code, gotNode, gotCookie, gotVia)
	}
	// a cookie that disagrees with the signed session: refused
	r = signed(t, "GET", "/api/b", "", "sid-1", key, now)
	r.AddCookie(&http.Cookie{Name: "mkt_sess", Value: "sid-other"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || !strings.Contains(w.Body.String(), ReasonSession) {
		t.Fatalf("mismatch: %d %s", w.Code, w.Body.String())
	}
}

// EC1: after a rotation the previous key verifies for the overlap only; Revoke drops both.
func TestKeyStoreRotateOverlapRevoke(t *testing.T) {
	now := time.Date(2026, 10, 19, 3, 0, 0, 0, time.UTC)
	ks := &KeyStore{Secrets: secrets.NewMemory(), Now: func() time.Time { return now }}
	ctx := context.Background()
	node := "11111111-2222-3333-4444-555555555555"
	k1, rotated, err := ks.Issue(ctx, node)
	if err != nil || !rotated {
		t.Fatalf("issue: %v %v", rotated, err)
	}
	if again, rot, _ := ks.Issue(ctx, node); rot || again.ID != k1.ID {
		t.Fatal("a fresh key is reused")
	}
	now = now.Add(RotateEvery + time.Minute)
	k2, rotated, _ := ks.Issue(ctx, node)
	if !rotated || k2.ID == k1.ID {
		t.Fatal("a day-old key rotates")
	}
	if _, ok := ks.Lookup(ctx, node, k1.ID); !ok {
		t.Fatal("the previous key verifies inside the overlap")
	}
	now = now.Add(Overlap + time.Second)
	if _, ok := ks.Lookup(ctx, node, k1.ID); ok {
		t.Fatal("the previous key stops after the overlap")
	}
	if _, ok := ks.Lookup(ctx, node, k2.ID); !ok {
		t.Fatal("the current key verifies")
	}
	// survives a restart through home_secrets
	ks2 := &KeyStore{Secrets: ks.Secrets, Now: ks.Now}
	if _, ok := ks2.Lookup(ctx, node, k2.ID); !ok {
		t.Fatal("the key is sealed in home_secrets")
	}
	ks.Revoke(ctx, node)
	if _, ok := ks.Lookup(ctx, node, k2.ID); ok {
		t.Fatal("a revoked node's key is gone")
	}
	ks3 := &KeyStore{Secrets: ks.Secrets, Now: ks.Now}
	if _, ok := ks3.Lookup(ctx, node, k2.ID); ok {
		t.Fatal("a revoked node's key is gone from home_secrets")
	}
}
