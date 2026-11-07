package storelink

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// fakeStore is the store's pairing protocol (store/web/homes.ts) in Go, checking each signed
// request as the store does, its nonce once.
type fakeStore struct {
	mu        sync.Mutex
	public    []byte
	confirmed bool
	expired   bool
	unlinked  bool
	nonces    map[string]bool
	signed    int
}

func (f *fakeStore) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/homes/pair":
			var in struct {
				PublicKey string `json:"public_key"`
			}
			_ = json.Unmarshal(b, &in)
			f.public, _ = base64.StdEncoding.DecodeString(in.PublicKey)
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "KQ7M.secret", "user_code": "KQ7M-4TXP",
				"verification_uri": "https://plugins.maktaba.tv/link", "expires_in": 600, "interval": 5})
		case r.URL.Path == "/v1/homes/pair/poll":
			if f.expired {
				w.WriteHeader(http.StatusGone)
				_, _ = w.Write([]byte(`{"error":"code_expired"}`))
				return
			}
			if f.confirmed {
				_, _ = w.Write([]byte(`{"status":"linked","home_id":"h_1"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"pending"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/homes/h_1":
			if !f.verify(t, r, b) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.unlinked = true
			_, _ = w.Write([]byte(`{"status":"unlinked"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (f *fakeStore) verify(t *testing.T, r *http.Request, body []byte) bool {
	t.Helper()
	h := strings.TrimPrefix(r.Header.Get("Authorization"), "MKS-Home ")
	parts := map[string]string{}
	for _, p := range strings.Split(h, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		parts[k] = v
	}
	sum := sha256.Sum256(body)
	msg := "MKS-Home\n" + r.Method + "\n" + r.URL.Path + "\n" + parts["ts"] + "\n" + parts["nonce"] + "\n" + hex.EncodeToString(sum[:])
	sig, _ := base64.StdEncoding.DecodeString(parts["sig"])
	if parts["id"] != "h_1" || f.nonces[parts["nonce"]] || !ed25519.Verify(f.public, []byte(msg), sig) {
		return false
	}
	if f.nonces == nil {
		f.nonces = map[string]bool{}
	}
	f.nonces[parts["nonce"]] = true
	f.signed++
	return true
}

// Story 54.15 T12 (the home's half): link, poll, a signed unlink, link again; the key is
// made once and kept sealed.
func TestLinkPollUnlink(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	f := &fakeStore{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	sec := secrets.NewMemory()
	l := &Link{DB: db, Secrets: sec, StoreURL: srv.URL, Version: "0.7.0", Platform: "darwin-arm64", Name: "Home"}
	ctx := context.Background()
	p, err := l.Start(ctx)
	if err != nil || p.UserCode != "KQ7M-4TXP" {
		t.Fatalf("start %+v %v", p, err)
	}
	seed, _ := sec.Get(ctx, KeyName)
	if len(seed) != ed25519.SeedSize {
		t.Fatalf("no home key kept")
	}
	if st, err := l.Poll(ctx); err != nil || st.Linked {
		t.Fatalf("pending %+v %v", st, err)
	}
	f.confirmed = true
	st, err := l.Poll(ctx)
	if err != nil || !st.Linked || st.HomeID != "h_1" || st.Pending != nil {
		t.Fatalf("linked %+v %v", st, err)
	}
	if err := l.Unlink(ctx); err != nil || !f.unlinked || f.signed != 1 {
		t.Fatalf("unlink %v (unlinked %v, signed %d)", err, f.unlinked, f.signed)
	}
	if st, _ := l.Status(ctx); st.Linked {
		t.Fatal("still linked")
	}
	// a second link reuses the key
	f.confirmed = false
	if _, err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	again, _ := sec.Get(ctx, KeyName)
	if string(again) != string(seed) {
		t.Fatal("a second link made a new key")
	}
	// an expired code tells the admin to start again
	f.expired = true
	if _, err := l.Poll(ctx); err == nil || err.(*Error).Code != CodeExpired {
		t.Fatalf("expired: %v", err)
	}
}

// The signature's form, checked by the store's TypeScript too
// (shared/plugins/testdata/signatures/home-request.json, store/test/workers/homes.test.ts).
func TestHomeSignatureVector(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "plugins", "testdata", "signatures", "home-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Seed   string `json:"seed"`
		HomeID string `json:"home_id"`
		Method string `json:"method"`
		Path   string `json:"path"`
		Nonce  string `json:"nonce"`
		Body   string `json:"body"`
		Header string `json:"header"`
		TS     int64  `json:"ts"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	seed, _ := hex.DecodeString(v.Seed)
	s := &signer{key: ed25519.NewKeyFromSeed(seed), homeID: v.HomeID}
	got := s.headerWith(v.Method, v.Path, []byte(v.Body), time.Unix(v.TS, 0), v.Nonce)
	if got != v.Header {
		t.Fatalf("got %s\nwant %s", got, v.Header)
	}
}

// Offline (serve --offline, MAKTABA_OFFLINE=1, #668): every store call is refused before it
// is made: the store sees no request.
func TestOfflineCallsNoStore(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	t.Setenv(ports.EnvOffline, "1")
	l := &Link{DB: db, Secrets: secrets.NewMemory(), StoreURL: srv.URL, Version: "0.7.0", Platform: "darwin-arm64", Name: "Home"}
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"start":  func() error { _, err := l.Start(ctx); return err },
		"poll":   func() error { _, err := l.Poll(ctx); return err },
		"signed": func() error { _, _, err := l.Signed(ctx, http.MethodPost, "/v1/homes/h_1/seen", nil); return err },
	} {
		var e *Error
		if err := call(); err == nil || (errors.As(err, &e) && e.Code != CodeOffline && e.Code != CodeNoPending && e.Code != CodeNotLinked) {
			t.Errorf("%s offline: %v", name, err)
		}
	}
	if _, err := l.Start(ctx); err == nil || err.(*Error).Code != CodeOffline {
		t.Fatalf("start offline: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("offline, the store got %d request(s)", n)
	}
}
