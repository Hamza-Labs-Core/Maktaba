package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGranter is the home's side of the machines' database access.
type fakeGranter struct {
	mu      sync.Mutex
	usable  bool
	granted []string
}

func (g *fakeGranter) AccessUsable(context.Context, string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.usable, nil
}

func (g *fakeGranter) GrantAccess(_ context.Context, node string) (json.RawMessage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.granted = append(g.granted, node)
	return json.RawMessage(`{"role":"maktaba_node_x","password":"fresh","db_port":54329,"db_name":"maktaba"}`), nil
}

func (g *fakeGranter) grants() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.granted)
}

// The home owns its machines' database access: a machine whose role does not log in on
// the home's current database (a move carried no roles) and one that says it cannot log
// in (home_db not ok) get their access re-made and pushed; a machine following its new
// access (healing) and one that logs in are left alone; the home says which cannot.
func TestEnsureAccess(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio-gpu")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGranter{usable: false} // its role is gone from the home's new database
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.hub.EnsureAccess(ctx, g, 20*time.Millisecond, nil)
	cmd := a.recvType("cmd")
	args, _ := cmd["args"].(map[string]any)
	if cmd["name"] != "home.access" || args["role"] != "maktaba_node_x" || args["db_port"] != float64(54329) {
		t.Fatalf("cmd = %v", cmd)
	}
	a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true})

	// it says it follows its new access: left alone, but listed as off the database
	a.send(map[string]any{"t": "update.state", "version": "1.5.0",
		"home_db": map[string]any{"ok": false, "code": "auth_failed", "healing": true}})
	waitUntil(t, func() bool { return len(f.hub.MachinesOffDatabase()) == 1 })
	if off := f.hub.MachinesOffDatabase(); off[0].NodeID != id || off[0].Code != "auth_failed" || !off[0].Healing {
		t.Fatalf("off the database: %+v", off)
	}
	before := g.grants()
	time.Sleep(150 * time.Millisecond)
	if g.grants() != before {
		t.Fatal("a machine following its new access had it re-made again")
	}
	// it logs in: nothing off
	a.send(map[string]any{"t": "update.state", "version": "1.5.0", "home_db": map[string]any{"ok": true}})
	waitUntil(t, func() bool { return len(f.hub.MachinesOffDatabase()) == 0 })
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// The re-made access goes to a machine's own agent only, and its password stays out of
// the hub's event log.
func TestHomeAccessPasswordNotLogged(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio-gpu")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		cmd := a.recvType("cmd")
		a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true})
	}()
	args := json.RawMessage(`{"role":"maktaba_node_x","password":"s3cret-pass","db_port":54329}`)
	if res, err := f.hub.CommandWithin(context.Background(), a.Session, "home.access", args, "system", 5*time.Second); err != nil || !res.OK {
		t.Fatalf("home.access: %+v %v", res, err)
	}
	// the hub writes its events in the background
	logged := ""
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if logged = strings.Join(readLines(t, eventsPath(f)), "\n"); strings.Contains(logged, "home.access") {
			break
		}
	}
	if strings.Contains(logged, "s3cret-pass") || !strings.Contains(logged, "not logged") {
		t.Fatalf("the event log: %s", logged)
	}
}
