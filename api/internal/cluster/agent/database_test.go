package agent

import (
	"context"
	"os"
	"path/filepath"
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
	if err := os.WriteFile(ca, []byte("CA one"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(ca, []byte("CA two"), 0o600); err != nil {
		t.Fatal(err)
	}
	take("CA two")
}

func TestDatabaseArgs(t *testing.T) {
	if b, err := databaseArgs(""); err != nil || string(b) != `{"tls_off":true}` {
		t.Fatalf("TLS off: %s %v", b, err)
	}
	if _, err := databaseArgs(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a CA that cannot be read")
	}
}
