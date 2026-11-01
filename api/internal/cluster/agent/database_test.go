package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A machine's own agent is handed this home's database CA over the channel: once, and
// again when the CA changes.
func TestPushDatabase(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.hub.MachineAgents(); got[id] != a.Session {
		t.Fatalf("the machine agents: %v (session %s)", got, a.Session)
	}
	ca := filepath.Join(t.TempDir(), "postgres-ca.crt")
	writeCA := func(body string) { // as the bundle writes it: whole, then renamed
		t.Helper()
		if err := os.WriteFile(ca+".tmp", []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(ca+".tmp", ca); err != nil {
			t.Fatal(err)
		}
	}
	writeCA("CA one")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.hub.PushDatabase(ctx, ca, 50*time.Millisecond, nil)
	take := func(want string) {
		t.Helper()
		cmd := a.recvType("cmd")
		args, _ := cmd["args"].(map[string]any)
		if cmd["name"] != "home.database" || args["ca_pem"] != want {
			t.Fatalf("cmd = %v, want the CA %q", cmd, want)
		}
		a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true})
	}
	take("CA one")
	// told once: the same CA is not sent again
	time.Sleep(300 * time.Millisecond)
	writeCA("CA two")
	take("CA two")
}

// A machine whose agent connects after a pass is told at once, not at the next interval
// (the replica e2e flake: it waited 30 s unable to verify its home's database, its copy
// idle, then started again in the middle of the test).
func TestPushDatabaseTellsAMachineAsItConnects(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.hub.PushDatabase(ctx, "", time.Hour, nil)
	time.Sleep(100 * time.Millisecond) // its first pass runs with no machine connected
	for _, name := range []string{"studio", "den"} {
		id, priv := f.approvedNode(name)
		a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
		if err != nil {
			t.Fatal(err)
		}
		// within recvType's 3 s, where the interval is an hour
		cmd := a.recvType("cmd")
		args, _ := cmd["args"].(map[string]any)
		if cmd["name"] != "home.database" || args["tls_off"] != true {
			t.Fatalf("%s: cmd = %v, want the home's word that its database has no TLS", name, cmd)
		}
		a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true})
	}
}

// A machine's agent that reconnects under the same worker id (its supervisor's channel
// dropped and came back) is told on its new session at once, even when a pass ran while
// the session it replaces was still registered and named for the machine.
func TestPushDatabaseTellsAReplacedAgentAtOnce(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.hub.PushDatabase(ctx, "", time.Hour, nil)
	id, priv := f.approvedNode("studio")
	hello := helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor", workerID: "maktaba-supervisor"}
	// which of the two sessions a pass in between names is the map's order: several
	// reconnects make the old one come up
	for i := range 12 {
		a, err := f.dialAgent(hello)
		if err != nil {
			t.Fatal(err)
		}
		cmd := a.recvType("cmd") // within 3 s, where the interval is an hour
		if cmd["name"] != "home.database" {
			t.Fatalf("reconnect %d: cmd = %v", i, cmd)
		}
		a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true})
	}
}

func TestDatabaseArgs(t *testing.T) {
	if b, err := databaseArgs(""); err != nil || string(b) != `{"tls_off":true}` {
		t.Fatalf("TLS off: %s %v", b, err)
	}
	if _, err := databaseArgs(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a CA that cannot be read")
	}
	empty := filepath.Join(t.TempDir(), "empty.crt")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := databaseArgs(empty); err == nil {
		t.Fatal("an empty CA is not one to hand a machine")
	}
}

// A standby home's kit goes over the channel whole, and its sealed bytes stay out of the
// hub's event log (their size is noted).
func TestAStandbyKitChunkIsSentAndNotLogged(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	data := strings.Repeat("c2VhbGVk", 64)
	args := []byte(`{"id":"standby:` + id + `","i":0,"last":true,"data":"` + data + `"}`)
	done := make(chan error, 1)
	go func() {
		_, err := f.hub.CommandWithin(context.Background(), a.Session, "standby.kit", args, "switchover", 5*time.Second)
		done <- err
	}()
	cmd := a.recvType("cmd")
	got, _ := cmd["args"].(map[string]any)
	if cmd["name"] != "standby.kit" || got["data"] != data {
		t.Fatalf("the chunk at the machine: %v", cmd)
	}
	a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// the hub writes its events in the background
	logged := ""
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if logged = strings.Join(readLines(t, eventsPath(f)), "\n"); strings.Contains(logged, "standby.kit") {
			break
		}
	}
	if strings.Contains(logged, data) || !strings.Contains(logged, "bytes, sealed") {
		t.Fatalf("the event log: %s", logged)
	}
	for _, name := range []string{"standby.commit", "standby.forget", "home.standbys"} {
		if !commandNames[name] {
			t.Errorf("the channel refuses %s", name)
		}
	}
}

// A command to a session that closes while it waits (replaced by its reconnect) ends at
// once, not at its timeout: the push to that machine's new session was held back 30 s.
func TestACommandToAClosingSessionEndsAtOnce(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.hub.CommandWithin(context.Background(), a.Session, "home.database", []byte(`{"tls_off":true}`), "system", 30*time.Second)
		done <- err
	}()
	a.recvType("cmd") // sent, and never answered
	f.hub.Session(a.Session).closeWith(CloseReplaced, "")
	select {
	case err := <-done:
		if !errors.Is(err, ErrNoSession) {
			t.Fatalf("err = %v, want ErrNoSession", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the command waited on a closed session")
	}
}

// While a machine's agent reconnects, the session it replaces stays listed until its
// connection has shut (seconds when the far end does not answer the close). The machine is
// named by its live, newer session, never the closed one: naming the old one left the new
// one untold until the old one was removed (TestPushDatabaseTellsAReplacedAgentAtOnce
// flaked on CI that way).
func TestMachineAgentsNamesTheLiveNewerSession(t *testing.T) {
	f := newFixture(t)
	node := "0b6f3c1e-2a4d-4e8f-9c71-5d2a8b3e6f10"
	at := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	agent := func(id string, connected time.Time) *Session {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		return &Session{ID: id, Kind: "node", NodeID: node, Process: "supervisor", ConnectedAt: connected,
			out: make(chan outFrame, 8), ctx: ctx, cancel: cancel}
	}
	put := func(ss ...*Session) {
		f.hub.mu.Lock()
		defer f.hub.mu.Unlock()
		for _, s := range ss {
			f.hub.sessions[s.ID] = s
		}
	}
	// "z-old" sorts after "a-new", so a pick by id or map order alone would name it
	old, fresh := agent("z-old", at), agent("a-new", at.Add(time.Second))
	put(old, fresh)
	for range 50 {
		if got := f.hub.MachineAgents()[node]; got != "a-new" {
			t.Fatalf("both live: named %q, want the newer a-new", got)
		}
	}
	// the new one closes first (it dropped too): the old one, still live, is the machine's
	fresh.closeWith(CloseReplaced, "")
	if got := f.hub.MachineAgents()[node]; got != "z-old" {
		t.Fatalf("newer closed: named %q, want the live z-old", got)
	}
	old.closeWith(CloseReplaced, "")
	if got, ok := f.hub.MachineAgents()[node]; ok {
		t.Fatalf("both closed: named %q, want no agent", got)
	}
}
