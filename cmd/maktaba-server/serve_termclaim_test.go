package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/switchover"
)

// claimMachine is one machine of a term-claim test: its node.json, its state, and its web
// address answering /api/cluster/self and /api/cluster/term-claim as the API does.
type claimMachine struct {
	s    *switching
	n    config.NodeConfig
	pub  []byte
	url  string
	role string // "node", or "home" (it answers as the home of homeTerm)
	term int64
}

func claimCluster(t *testing.T, names ...string) (map[string]*claimMachine, machineKeys) {
	t.Helper()
	ms := map[string]*claimMachine{}
	standby := map[string]bool{}
	for _, name := range names {
		priv, pub := x25519Pair(t)
		cfg := config.Config{}
		cfg.Server.DataDir = filepath.Join(t.TempDir(), name)
		m := &claimMachine{s: newSwitching(cfg), pub: pub, role: "node",
			n: config.NodeConfig{NodeID: uuid.NewString(), Name: name, PrivateKey: base64.StdEncoding.EncodeToString(priv)}}
		ms[name] = m
		standby[m.n.NodeID] = strings.HasPrefix(name, "standby")
	}
	keys := func(_ context.Context, id string, wantStandby bool) ([]byte, error) {
		for _, m := range ms {
			if m.n.NodeID == id {
				if wantStandby && !standby[id] {
					return nil, errors.New("not a standby home")
				}
				return m.pub, nil
			}
		}
		return nil, errors.New("no such machine")
	}
	for _, m := range ms {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/cluster/self":
				_ = json.NewEncoder(w).Encode(map[string]any{"role": m.role, "id": m.n.NodeID, "term": m.term})
			case "/api/cluster/term-claim":
				var req homeRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					http.Error(w, "bad", http.StatusBadRequest)
					return
				}
				out, err := m.s.answerClaim(r.Context(), req, m.n, keys)
				if err != nil {
					http.Error(w, err.Error(), http.StatusConflict)
					return
				}
				_ = json.NewEncoder(w).Encode(out)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		m.url = srv.URL
	}
	for _, m := range ms {
		for _, o := range ms {
			if o == m {
				continue
			}
			if standby[o.n.NodeID] {
				m.n.StandbyHomes = append(m.n.StandbyHomes, o.url)
			} else {
				m.n.Peers = append(m.n.Peers, o.url)
			}
		}
	}
	return ms, keys
}

// Two standby homes never both take an emergency's term: the first one's claim stands at
// every machine, the second is refused there; a machine whose home is of that term already
// refuses it, as does a home that answers; a claim from a machine that is not a standby
// does not open.
func TestTwoStandbysNeverBothTakeTheTerm(t *testing.T) {
	ms, keys := claimCluster(t, "standby-a", "standby-b", "studio")
	a, b, studio := ms["standby-a"], ms["standby-b"], ms["studio"]
	ctx := t.Context()
	if err := a.s.claimAll(ctx, 3, a.n, keys); err != nil {
		t.Fatalf("the first claim: %v", err)
	}
	if err := b.s.claimAll(ctx, 3, b.n, keys); err == nil || !strings.Contains(err.Error(), a.n.NodeID) {
		t.Fatalf("the second standby took the term too: %v", err)
	}
	// asked again, the first goes on (its own claim stands); a newer term is free
	if err := a.s.claimAll(ctx, 3, a.n, keys); err != nil {
		t.Fatalf("the first, asked again: %v", err)
	}

	// a machine that follows a home of the term already
	studio.n.Home.Term = 4
	if err := b.s.claimAll(ctx, 4, b.n, keys); err == nil || !strings.Contains(err.Error(), "term 4") {
		t.Fatalf("a machine following a home of term 4: %v", err)
	}
	studio.n.Home.Term = 0
	// refused, the standby withdrew its own claim: asked again it has the term (the other
	// standby keeps the claim it took for it, of the same standby)
	if err := b.s.claimAll(ctx, 4, b.n, keys); err != nil {
		t.Fatalf("asked again: %v", err)
	}

	// a standby that is the home already
	a.role, a.term = "home", 5
	if err := b.s.claimAll(ctx, 5, b.n, keys); err == nil || !strings.Contains(err.Error(), "is the home already") {
		t.Fatalf("a standby that is the home: %v", err)
	}
	a.role, a.term = "node", 0

	// a machine that is not a standby home: its claim does not open
	priv, _ := base64.StdEncoding.DecodeString(studio.n.PrivateKey)
	msg, _ := json.Marshal(termClaimMsg{Term: 6, From: studio.n.NodeID, To: a.n.NodeID})
	sealed, err := switchover.SealPair(priv, a.pub, "term-6", termClaimInfo, msg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.s.answerClaim(ctx, homeRequest{From: studio.n.NodeID, Term: 6, Sealed: sealed}, a.n, keys); err == nil {
		t.Fatal("a machine that is not a standby home claimed a term")
	}
	// nor does one sealed to another machine
	msg, _ = json.Marshal(termClaimMsg{Term: 6, From: b.n.NodeID, To: studio.n.NodeID})
	bPriv, _ := base64.StdEncoding.DecodeString(b.n.PrivateKey)
	sealed, _ = switchover.SealPair(bPriv, a.pub, "term-6", termClaimInfo, msg)
	if _, err := a.s.answerClaim(ctx, homeRequest{From: b.n.NodeID, Term: 6, Sealed: sealed}, a.n, keys); err == nil {
		t.Fatal("a claim for another machine was taken")
	}
}

// Two standbys claiming the same term at once each find the other's own claim: both are
// refused (neither takes over), and both withdraw theirs, so one asked again has it.
func TestTwoStandbysAtOnceRefuseEachOther(t *testing.T) {
	ms, keys := claimCluster(t, "standby-a", "standby-b")
	a, b := ms["standby-a"], ms["standby-b"]
	ctx := t.Context()
	// each holds its own claim before it asks the other
	if h, err := a.s.claimTerm(7, a.n.NodeID, a.n.NodeID, time.Now()); err != nil || h != "" {
		t.Fatal(h, err)
	}
	if err := b.s.claimAll(ctx, 7, b.n, keys); err == nil {
		t.Fatal("b took the term a holds")
	}
	a.s.withdrawClaim(7, a.n.NodeID)
	if err := b.s.claimAll(ctx, 7, b.n, keys); err != nil {
		t.Fatalf("b asked again once a gave the term up: %v", err)
	}
	if err := a.s.claimAll(ctx, 7, a.n, keys); err == nil {
		t.Fatal("a took the term b holds now")
	}
}
