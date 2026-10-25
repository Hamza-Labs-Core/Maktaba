package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/machineagent"
)

// A machine whose sessions the home refuses again and again (it removed or revoked it)
// goes back to the join flow by itself, as itself: its own node id and key, a pairing code
// for the home's admin, and a home that still holds it (revoked) asked again later; one
// refusal (a home in the middle of a switch) or a session the home takes again starts
// nothing.
func TestRejoinAfterTheHomeRefusesThisMachine(t *testing.T) {
	defer func(a int, s, r, p time.Duration) { rejoinAfter, rejoinSpan, rejoinRetry, rejoinPoll = a, s, r, p }(
		rejoinAfter, rejoinSpan, rejoinRetry, rejoinPoll)
	rejoinAfter, rejoinSpan, rejoinRetry, rejoinPoll = 3, time.Minute, 20*time.Millisecond, 10*time.Millisecond

	var mu sync.Mutex
	member, asks := true, []map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "pending"})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		asks = append(asks, body)
		if member {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"already_member","detail":"an admin must remove it before it can ask again"}`))
			return
		}
		priv := make([]byte, 32)
		_, _ = rand.Read(priv)
		pub, _ := curve25519.X25519(priv, curve25519.Basepoint)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"home_pubkey": base64.StdEncoding.EncodeToString(pub),
			"home": map[string]any{"name": "home-mac"}})
	}))
	defer srv.Close()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	node := config.NodeConfig{NodeID: "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7", Name: "studio-pc",
		PrivateKey: base64.StdEncoding.EncodeToString(key), Home: config.HomeRef{URL: srv.URL}}
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	w := &rejoinWatch{node: func() config.NodeConfig { return node }, clock: func() time.Time { return now },
		joined: func(config.NodeConfig, machineagent.Joined) error { return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.refused(ctx, 4003) // one refusal: a home in the middle of a switch
	now = now.Add(30 * time.Second)
	w.refused(ctx, 4003)
	w.connected() // the home takes it again
	now = now.Add(30 * time.Second)
	w.refused(ctx, 4003)
	now = now.Add(30 * time.Second)
	w.refused(ctx, 4003)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(asks) != 0 {
		t.Fatal("a re-join before the home refused this machine for long enough")
	}
	mu.Unlock()
	now = now.Add(31 * time.Second)
	w.refused(ctx, 4010) // the third in a row, over a minute: the join flow
	waitFor(t, "held", func() bool { st := w.State(); return st != nil && st.State == "held" })
	mu.Lock()
	ask := asks[0]
	member = false // an admin removed it on the home
	mu.Unlock()
	pub, _ := curve25519.X25519(key, curve25519.Basepoint)
	if ask["node_id"] != node.NodeID || ask["pubkey"] != base64.StdEncoding.EncodeToString(pub) || ask["name"] != "studio-pc" {
		t.Fatalf("asked as another identity: %v", ask)
	}
	waitFor(t, "waiting", func() bool { st := w.State(); return st != nil && st.State == "waiting" })
	if st := w.State(); len(strings.ReplaceAll(st.Code, " ", "")) != 6 || st.Home != "home-mac" {
		t.Fatalf("waiting: %+v", st)
	}
	// the home takes this machine again (another start): the re-join stops
	w.connected()
	if w.State() != nil {
		t.Fatal("a re-join outlived the home taking this machine")
	}
}
