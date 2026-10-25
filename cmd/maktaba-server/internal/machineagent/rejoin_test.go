package machineagent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

// The join crypto matches the home's and the pipeline's, byte for byte
// (shared/testdata/cluster/vectors.json): the pairing code, the seal key, the envelope.
func TestRejoinCryptoVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "testdata", "cluster", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Sealed struct{ Nonce, Ciphertext string }
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var keys map[string]string
	_ = json.Unmarshal(raw, &keys)
	d := func(k string) []byte {
		b, err := base64.StdEncoding.DecodeString(keys[k])
		if err != nil {
			t.Fatal(k, err)
		}
		return b
	}
	nodePub, homePub, shared := d("node_public"), d("home_public"), d("shared")
	if got, _ := curve25519.X25519(d("node_private"), homePub); string(got) != string(shared) {
		t.Fatal("shared secret")
	}
	if code, err := joinSAS(shared, nodePub, homePub); err != nil || code != keys["sas"] {
		t.Fatalf("sas %s, want %s (%v)", code, keys["sas"], err)
	}
	key, err := joinKey(shared, nodePub, homePub, joinSealInfo, 32)
	if err != nil || base64.StdEncoding.EncodeToString(key) != keys["seal_key"] {
		t.Fatalf("seal key %v", err)
	}
	nonce, _ := base64.StdEncoding.DecodeString(v.Sealed.Nonce)
	ct, _ := base64.StdEncoding.DecodeString(v.Sealed.Ciphertext)
	plain, err := openSealed(key, keys["node_id"], nonce, ct)
	if err != nil || string(plain) != keys["payload"] {
		t.Fatalf("opened %q, %v", plain, err)
	}
	if FormatCode("761353") != "761 353" {
		t.Fatal("format")
	}
}

// fakeJoinHome is the home's side of a join (POST /api/cluster/join, its poll), as the
// home's handlers answer it.
type fakeJoinHome struct {
	mu       sync.Mutex
	member   bool // the home still holds the machine (approved or revoked)
	approved bool
	priv     []byte
	nodePub  []byte
	nodeID   string
	asked    map[string]any
	code     string
}

func (h *fakeJoinHome) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/cluster/join":
		if h.member {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"already_member","detail":"this machine is already known to the home; an admin must remove it before it can ask again"}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&h.asked)
		h.nodeID, _ = h.asked["node_id"].(string)
		h.nodePub, _ = base64.StdEncoding.DecodeString(h.asked["pubkey"].(string))
		h.priv = make([]byte, 32)
		_, _ = rand.Read(h.priv)
		pub, _ := curve25519.X25519(h.priv, curve25519.Basepoint)
		shared, _ := curve25519.X25519(h.priv, h.nodePub)
		h.code, _ = joinSAS(shared, h.nodePub, pub)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"request_id": h.nodeID, "home_pubkey": base64.StdEncoding.EncodeToString(pub),
			"home": map[string]any{"name": "home-mac"}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/cluster/join/"):
		if !h.approved {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "pending"})
			return
		}
		pub, _ := curve25519.X25519(h.priv, curve25519.Basepoint)
		shared, _ := curve25519.X25519(h.priv, h.nodePub)
		key, _ := joinKey(shared, h.nodePub, pub, joinSealInfo, 32)
		aead, _ := chacha20poly1305.New(key)
		nonce := make([]byte, aead.NonceSize())
		_, _ = rand.Read(nonce)
		payload, _ := json.Marshal(map[string]any{"v": 1, "node_id": h.nodeID,
			"database_url": "postgres://maktaba_node_x:new-pass@192.168.50.177:54329/maktaba?sslmode=prefer",
			"database_ca":  "", "home": map[string]any{"api_url": "http://192.168.50.177:8080", "cluster_pub": "Y2x1c3Rlcg==",
				"instance_id": "i-1", "name": "home-mac"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "approved", "sealed": map[string]any{
			"nonce": base64.StdEncoding.EncodeToString(nonce), "ciphertext": base64.StdEncoding.EncodeToString(
				aead.Seal(nil, nonce, payload, []byte(strings.ToLower(h.nodeID))))}})
	default:
		http.NotFound(w, r)
	}
}

// A machine asks its home to take it again as itself: its own node id and key (never a
// new identity), a pairing code the home's admin enters, then the credentials the home
// seals to it once approved. A home that still holds the machine (revoked) says so.
func TestRejoinAsItself(t *testing.T) {
	home := &fakeJoinHome{member: true}
	srv := httptest.NewServer(home)
	defer srv.Close()
	priv := make([]byte, 32)
	_, _ = rand.Read(priv)
	pub, _ := curve25519.X25519(priv, curve25519.Basepoint)
	r := Rejoin{HomeURL: srv.URL, NodeID: "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7", Name: "studio-pc",
		PrivateKey: base64.StdEncoding.EncodeToString(priv), CertDir: t.TempDir(), Version: "0.3.14"}
	ctx := context.Background()
	_, err := r.Ask(ctx)
	var refused *RejoinRefused
	if !errors.As(err, &refused) || refused.Code != "already_member" || !strings.Contains(refused.Error(), "remove it") {
		t.Fatalf("a home that still holds it: %v", err)
	}
	home.mu.Lock()
	home.member = false
	home.mu.Unlock()
	p, err := r.Ask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if home.asked["node_id"] != r.NodeID || home.asked["pubkey"] != base64.StdEncoding.EncodeToString(pub) ||
		home.asked["csr"] == nil || p.Code != home.code || len(p.Code) != 6 || p.HomeName != "home-mac" {
		t.Fatalf("asked %v, code %s (home %s)", home.asked, p.Code, home.code)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		home.mu.Lock()
		home.approved = true
		home.mu.Unlock()
	}()
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	j, err := p.Wait(wctx, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(j.DatabaseURL, ":54329/maktaba") || j.Home.ClusterPub != "Y2x1c3Rlcg==" || j.Home.APIURL == "" {
		t.Fatalf("joined %+v", j)
	}
}
