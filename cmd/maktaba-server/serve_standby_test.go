package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/nodecopy"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/switchover"
)

func x25519Pair(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv = make([]byte, 32)
	_, _ = rand.Read(priv)
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

// standbyMachine isolates a joined machine: its node.json (with its key and the
// cluster's public key) and its keys directory.
func standbyMachine(t *testing.T, nodePriv, clusterPub []byte, homeURL string) (*switching, string) {
	t.Helper()
	dir := t.TempDir()
	node := filepath.Join(dir, "node.json")
	t.Setenv("MAKTABA_NODE_CONFIG", node)
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	b, _ := json.Marshal(map[string]any{"node_id": "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7", "name": "studio",
		"private_key":  base64.StdEncoding.EncodeToString(nodePriv),
		"home":         map[string]any{"url": homeURL, "name": "library", "cluster_pub": base64.StdEncoding.EncodeToString(clusterPub), "term": 1},
		"database_url": "postgres://r:p@home:54329/maktaba?sslmode=prefer"})
	if err := os.WriteFile(node, b, 0o600); err != nil {
		t.Fatal(err)
	}
	for len(reloadCh) > 0 {
		<-reloadCh
	}
	s := newSwitching(config.Config{})
	s.set(nil, nil, nil, "", nil)
	return s, dir
}

// A standby takes the home's kit over the channel only when it opens for it (sealed by
// the cluster key to this machine), keeps it beside its keys, says it can take over
// (save its copy), and removes it when the home says so.
func TestAStandbyTakesTheKitOverTheChannel(t *testing.T) {
	clusterPriv, clusterPub := x25519Pair(t)
	nodePriv, nodePub := x25519Pair(t)
	s, dir := standbyMachine(t, nodePriv, clusterPub, "http://127.0.0.1:1")
	ctx := t.Context()
	nodeID := config.LocalNodeConfig().NodeID
	id := switchover.StandbyKitID(nodeID)
	kit := &switchover.Kit{Switchover: id, Term: 2, MadeAt: time.Now().UTC(),
		Files: map[string][]byte{kitSecretsKey: []byte("the secrets key")}, Tables: map[string][]json.RawMessage{}}
	sealed := filepath.Join(dir, "standby.chunks")
	if _, err := sealKit(kit, clusterPriv, nodePub, id, sealed); err != nil {
		t.Fatal(err)
	}
	send := func(as string, path string) {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := switchover.ReadChunks(f, func(i uint64, last bool, chunk []byte) error {
			args, _ := json.Marshal(map[string]any{"id": as, "i": i, "last": last,
				"data": base64.StdEncoding.EncodeToString(chunk)})
			if _, ok, detail := s.command(ctx, "standby.kit", args); !ok {
				t.Fatalf("a chunk: %v", detail)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	commit := func() (bool, any) {
		args, _ := json.Marshal(map[string]any{"id": id, "hash": "h1"})
		_, ok, detail := s.command(ctx, "standby.commit", args)
		return ok, detail
	}
	keys := config.KeysDir()
	// another machine's kit is refused
	other, _ := json.Marshal(map[string]any{"id": switchover.StandbyKitID("someone-else"), "i": 0, "last": true, "data": "AA=="})
	if _, ok, _ := s.command(ctx, "standby.kit", other); ok {
		t.Fatal("another machine's kit was taken")
	}
	// a kit sealed for another machine does not open
	_, strangerPub := x25519Pair(t)
	stray := filepath.Join(dir, "stray.chunks")
	if _, err := sealKit(kit, clusterPriv, strangerPub, id, stray); err != nil {
		t.Fatal(err)
	}
	send(id, stray)
	if ok, _ := commit(); ok {
		t.Fatal("a kit sealed for another machine was kept")
	}
	if _, err := os.Stat(filepath.Join(keys, nodecopy.StandbyKitPart)); err == nil {
		t.Fatal("the stray kit was left behind")
	}
	send(id, sealed)
	if ok, detail := commit(); !ok {
		t.Fatalf("the kit: %v", detail)
	}
	if _, err := os.Stat(filepath.Join(keys, nodecopy.StandbyKitFile)); err != nil {
		t.Fatalf("the kit is not kept: %v", err)
	}
	st := s.emergencyStatus(ctx)
	if !st.Standby || st.KitTerm != 2 || st.KitMadeAt == nil || st.HomeAnswers || st.Ready ||
		!strings.Contains(strings.Join(st.Reasons, ","), emergencyNoCopy) {
		t.Fatalf("the emergency's state: %+v", st)
	}
	if _, err := s.emergency(ctx, homeRequest{Confirm: "library"}); err == nil {
		t.Fatal("an emergency without a copy was taken")
	}
	forget, _ := json.Marshal(map[string]any{"id": id})
	if _, ok, detail := s.command(ctx, "standby.forget", forget); !ok {
		t.Fatalf("forget: %v", detail)
	}
	for _, f := range []string{nodecopy.StandbyKitFile, nodecopy.StandbyKitMeta} {
		if _, err := os.Stat(filepath.Join(keys, f)); err == nil {
			t.Fatalf("%s kept after the mark was removed", f)
		}
	}
	if st := s.emergencyStatus(ctx); st.Standby {
		t.Fatal("still a standby without its kit")
	}
}

// With its home gone, a machine asks the standby homes for its notice of a new home: one
// that opens (the cluster's, for it) and is of a newer term, it follows.
func TestAMachineFindsTheNewHomeAtAStandby(t *testing.T) {
	clusterPriv, clusterPub := x25519Pair(t)
	nodePriv, nodePub := x25519Pair(t)
	s, _ := standbyMachine(t, nodePriv, clusterPub, "http://127.0.0.1:1")
	nodeID := config.LocalNodeConfig().NodeID
	plain, _ := json.Marshal(movedNotice{Term: 2, Home: newHome{APIURL: "http://standby:8082", DBHost: "standby",
		DBPort: 54329, Name: "standby"}})
	notice, err := switchover.SealNotice(clusterPriv, nodePub, "sw-e", plain)
	if err != nil {
		t.Fatal(err)
	}
	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cluster/moved/"+nodeID {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sw-e", "term": 2, "notice": notice})
	}))
	defer standby.Close()
	// where the standbys are, from the home
	args, _ := json.Marshal(map[string]any{"urls": []string{standby.URL + "/", "ftp://nope", "not a url"}})
	if _, ok, detail := s.command(t.Context(), "home.standbys", args); !ok {
		t.Fatalf("the standbys: %v", detail)
	}
	if got := config.LocalNodeConfig().StandbyHomes; len(got) != 1 || got[0] != standby.URL {
		t.Fatalf("node.json's standbys: %v", got)
	}
	if _, ok, detail := s.command(t.Context(), "home.standbys", args); !ok || detail.(map[string]any)["changed"] != false {
		t.Fatalf("the same standbys again: %v %v", ok, detail)
	}
	s.checkMoved(t.Context(), &http.Client{Timeout: 2 * time.Second})
	n := config.LocalNodeConfig()
	if n.Home.APIURL != "http://standby:8082" || n.Home.Term != 2 || !strings.Contains(n.DatabaseURL, "standby:54329") {
		t.Fatalf("the machine did not follow the new home: %+v", n.Home)
	}
	select {
	case <-reloadCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the machine does not start again with its new home")
	}
	// a notice it already followed changes nothing
	s.checkMoved(t.Context(), &http.Client{Timeout: 2 * time.Second})
	select {
	case <-reloadCh:
		t.Fatal("an old notice was followed again")
	case <-time.After(3 * time.Second):
	}
}

// With its old home retired and no standby, a machine that was off during a switch finds
// its notice at another of the home's machines (every copy has the notices).
func TestAMachineFindsTheNewHomeAtAPeer(t *testing.T) {
	clusterPriv, clusterPub := x25519Pair(t)
	nodePriv, nodePub := x25519Pair(t)
	s, _ := standbyMachine(t, nodePriv, clusterPub, "http://127.0.0.1:1")
	nodeID := config.LocalNodeConfig().NodeID
	plain, _ := json.Marshal(movedNotice{Term: 2, Home: newHome{APIURL: "http://b:8082", DBHost: "b", DBPort: 54329, Name: "b"}})
	notice, err := switchover.SealNotice(clusterPriv, nodePub, "sw-p", plain)
	if err != nil {
		t.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cluster/moved/"+nodeID {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sw-p", "term": 2, "notice": notice})
	}))
	defer peer.Close()
	args, _ := json.Marshal(map[string]any{"urls": []string{}, "peers": []string{peer.URL}})
	if _, ok, detail := s.command(t.Context(), "home.standbys", args); !ok {
		t.Fatalf("the peers: %v", detail)
	}
	if got := config.LocalNodeConfig().Peers; len(got) != 1 || got[0] != peer.URL {
		t.Fatalf("node.json's peers: %v", got)
	}
	s.checkMoved(t.Context(), &http.Client{Timeout: 2 * time.Second})
	if n := config.LocalNodeConfig(); n.Home.APIURL != "http://b:8082" || n.Home.Term != 2 {
		t.Fatalf("the machine did not follow the new home: %+v", n.Home)
	}
	select {
	case <-reloadCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the machine does not start again with its new home")
	}
}

// An old home's report reaches the new home sealed from the identity its kits gave it
// to the cluster's key, at the report's public address; once taken it is marked sent.
func TestTheOldHomeReportsWhatItKeptAside(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "no-node.json"))
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	clusterPriv, _ := x25519Pair(t)
	keyFile := filepath.Join(dir, "home-cluster.key")
	if err := os.WriteFile(keyFile, clusterPriv, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_HOME_CLUSTER_KEY", keyFile)
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join(dir, "data")
	me, err := identityAt(filepath.Join(cfg.StateDir(), "switchover", "standby-identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	mePub, _ := base64.StdEncoding.DecodeString(me.PublicKey)
	var got keptReport
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/cluster/kept-aside/report" {
			http.Error(w, "not the report's address", http.StatusUnauthorized)
			return
		}
		var req struct {
			NodeID string `json:"node_id"`
			Term   int64  `json:"term"`
			Sealed string `json:"sealed"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		plain, err := switchover.OpenReport(clusterPriv, mePub, req.Term, req.Sealed)
		if err != nil || req.NodeID != me.NodeID || json.Unmarshal(plain, &got) != nil {
			http.Error(w, "does not open", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	defer home.Close()
	r := keptReport{ID: "r1", Term: 2, FromNode: me.NodeID, FromName: "a", Cut: "0/1", HomeAPI: home.URL,
		Aside: switchover.Aside{Counts: map[string]int{"public.app_settings": 1}}}
	if err := saveKept(cfg, r); err != nil {
		t.Fatal(err)
	}
	if err := uploadKept(t.Context(), cfg, 2); err != nil {
		t.Fatal(err)
	}
	if got.ID != "r1" || got.Aside.Counts["public.app_settings"] != 1 {
		t.Fatalf("the new home got %+v", got)
	}
	b, _ := os.ReadFile(keptPath(cfg, 2))
	var after keptReport
	if json.Unmarshal(b, &after) != nil || !after.Uploaded {
		t.Fatalf("the report is not marked sent: %s", b)
	}
	// sent: not sent again
	home.Close()
	if err := uploadKept(t.Context(), cfg, 2); err != nil {
		t.Fatalf("a sent report was sent again: %v", err)
	}
}
