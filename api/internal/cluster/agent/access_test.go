package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// fakeGranter is the home's side of the machines' database access.
type fakeGranter struct {
	mu      sync.Mutex
	usable  bool
	granted []string
	err     error // what stops GrantAccess
}

func (g *fakeGranter) AccessUsable(context.Context, string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.usable, nil
}

func (g *fakeGranter) GrantAccess(_ context.Context, node string) (json.RawMessage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return nil, g.err
	}
	g.granted = append(g.granted, node)
	return json.RawMessage(`{"role":"maktaba_node_x","password":"fresh","db_port":54329,"db_name":"maktaba"}`), nil
}

func (g *fakeGranter) AccessTarget(context.Context, string) (string, int, string, error) {
	return "", 54329, "maktaba", nil
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

	// one failed try (a home in the middle of a switch): neither re-made nor shown
	before := g.grants()
	a.send(map[string]any{"t": "update.state", "version": "1.5.0",
		"home_db": map[string]any{"ok": false, "code": "refused", "fails": 1}})
	time.Sleep(150 * time.Millisecond)
	if g.grants() != before || len(f.hub.MachinesOffDatabase()) != 0 {
		t.Fatalf("one failed try acted on: %d grants, off %+v", g.grants()-before, f.hub.MachinesOffDatabase())
	}

	// it says it follows its new access: left alone, but listed as off the database
	a.send(map[string]any{"t": "update.state", "version": "1.5.0",
		"home_db": map[string]any{"ok": false, "code": "auth_failed", "healing": true, "fails": 2}})
	waitUntil(t, func() bool { return len(f.hub.MachinesOffDatabase()) == 1 })
	if off := f.hub.MachinesOffDatabase(); off[0].NodeID != id || off[0].Code != "auth_failed" || !off[0].Healing {
		t.Fatalf("off the database: %+v", off)
	}
	before = g.grants()
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
		f.hub.files.Flush()
		if logged = strings.Join(readLines(t, eventsPath(f)), "\n"); strings.Contains(logged, "home.access") {
			break
		}
	}
	if strings.Contains(logged, "s3cret-pass") || !strings.Contains(logged, "not logged") {
		t.Fatalf("the event log: %s", logged)
	}
}

// A machine's log lines (logs.files) go to the admin who asked, not into the hub's event
// log: the answer is noted by its size; a worker's session does not take the command.
func TestLogFilesAnswerNotLogged(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio-gpu")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		cmd := a.recvType("cmd")
		a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true,
			"detail": map[string]any{"entries": []map[string]string{{"msg": "a-private-log-line"}}}})
	}()
	res, err := f.hub.CommandWithin(context.Background(), a.Session, "logs.files", json.RawMessage(`{"files":"server"}`), "admin", 5*time.Second)
	if err != nil || !res.OK || !strings.Contains(string(res.Detail), "a-private-log-line") {
		t.Fatalf("logs.files: %+v %v", res, err)
	}
	logged := ""
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		f.hub.files.Flush()
		if logged = strings.Join(readLines(t, eventsPath(f)), "\n"); strings.Contains(logged, `"cmd.result"`) {
			break
		}
	}
	if strings.Contains(logged, "a-private-log-line") || !strings.Contains(logged, "bytes)") {
		t.Fatalf("the event log: %s", logged)
	}
}

// A re-made access only where it mends the login: a refused password, or a database the
// machine reaches somewhere else than the access the home hands out (it moved); not TLS,
// not a database that refuses or cannot take a login at the right address. Each one more
// waits twice as long, up to an hour.
func TestEnsureAccessOnlyWhenItMends(t *testing.T) {
	for _, c := range []struct {
		db   homeDBState
		want string
	}{
		{homeDBState{Code: "auth_failed", Fails: 2, Target: "h:54329/maktaba"}, "auth_failed"},
		{homeDBState{Code: "refused", Fails: 2, Target: "192.168.50.177:5432/maktaba"}, "refused_moved"},
		{homeDBState{Code: "no_database", Fails: 2, Target: "h:54329/maktaba_old"}, "no_database_moved"},
		{homeDBState{Code: "refused", Fails: 2, Target: "h:54329/maktaba"}, ""},
		{homeDBState{Code: "no_hba", Fails: 2, Target: "h:54329/maktaba"}, ""},
		{homeDBState{Code: "tls", Fails: 2, Target: "h:54329/maktaba"}, ""},
		{homeDBState{Code: "other", Fails: 2, Target: "h:54329/maktaba"}, ""},
		// the database moved, whatever the old address does now (closes, fails TLS)
		{homeDBState{Code: "tls", Fails: 2, Target: "h:5432/maktaba"}, "tls_moved"},
		{homeDBState{Code: "other", Fails: 2, Target: "h:5432/maktaba"}, "other_moved"},
		// the same port and database on another machine than the one it reaches the home at
		{homeDBState{Code: "other", Fails: 2, Target: "old-home:54329/maktaba", HomeHost: "new-home"}, "other_moved"},
		{homeDBState{Code: "other", Fails: 2, Target: "NEW-home:54329/maktaba", HomeHost: "new-home"}, ""},
		{homeDBState{Code: "unreachable", Fails: 2}, ""},
	} {
		if got := remakeWhy(c.db, "", 54329, "maktaba"); got != c.want {
			t.Errorf("%+v: %q, want %q", c.db, got, c.want)
		}
	}
	if !moved("db.lan:54329/maktaba", "db2.lan", 54329, "maktaba", "home") || moved("DB.lan:54329/maktaba", "db.lan", 54329, "maktaba", "home") {
		t.Error("the host the home names")
	}
	for tries, want := range map[int]time.Duration{0: time.Minute, 1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute,
		7: time.Hour, 50: time.Hour} {
		if got := retryAfter(tries); got != want {
			t.Errorf("retryAfter(%d) = %s, want %s", tries, got, want)
		}
	}

	// over the channel: TLS and a refusal at the right address are left alone
	f := newFixture(t)
	id, priv := f.approvedNode("studio-gpu")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGranter{usable: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.hub.EnsureAccess(ctx, g, 20*time.Millisecond, nil)
	a.send(map[string]any{"t": "update.state", "version": "1.5.0",
		"home_db": map[string]any{"ok": false, "code": "tls", "fails": 3, "target": "h:54329/maktaba"}})
	waitUntil(t, func() bool { return len(f.hub.MachinesOffDatabase()) == 1 })
	time.Sleep(150 * time.Millisecond)
	if g.grants() != 0 {
		t.Fatalf("a TLS failure rotated the access: %d", g.grants())
	}
	// the database moved (its old port refuses): re-made
	a.send(map[string]any{"t": "update.state", "version": "1.5.0",
		"home_db": map[string]any{"ok": false, "code": "refused", "fails": 2, "target": "h:5432/maktaba"}})
	cmd := a.recvType("cmd")
	if cmd["name"] != "home.access" {
		t.Fatalf("cmd = %v", cmd)
	}
	a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true})
	waitUntil(t, func() bool { return g.grants() == 1 })
}

// A machine's sessions say its host (the drain finds its running jobs by it).
func TestNodeHosts(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio-gpu")
	if _, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return len(f.hub.NodeHosts()[id]) == 1 })
	if got := f.hub.NodeHosts()[id]; got[0] != "test-host" {
		t.Fatalf("hosts: %v", got)
	}
}

// A machine that got no re-made access asks for one on its own agent's session
// (access.request): the home makes it for that session's machine and sends it
// (home.access), then answers {ok}; what stops it is answered with its code and reason;
// a request again too soon is refused (rate_limited), and a worker's session is not a
// machine's agent. Every request is in the events file.
func TestAMachineAsksForItsAccess(t *testing.T) {
	f := newFixture(t)
	id, priv := f.approvedNode("studio-gpu")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGranter{usable: true} // the push finds nothing to mend: the machine asks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.hub.EnsureAccess(ctx, g, time.Hour, nil)
	waitUntil(t, func() bool {
		f.hub.acc.mu.Lock()
		defer f.hub.acc.mu.Unlock()
		return f.hub.acc.granter != nil
	})
	a.send(map[string]any{"t": "access.request", "id": "ask-1",
		"home_db": map[string]any{"ok": false, "code": "refused", "fails": 4, "target": "192.168.50.177:5432/maktaba"}})
	cmd := a.recvType("cmd")
	if args, _ := cmd["args"].(map[string]any); cmd["name"] != "home.access" || args["db_port"] != float64(54329) {
		t.Fatalf("cmd = %v", cmd)
	}
	a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true})
	if ans := a.recvType("access.answer"); ans["id"] != "ask-1" || ans["ok"] != true {
		t.Fatalf("answer = %v", ans)
	}
	if g.grants() != 1 || g.granted[0] != id {
		t.Fatalf("granted %v", g.granted)
	}
	// again at once: refused, nothing re-made
	a.send(map[string]any{"t": "access.request", "id": "ask-2"})
	if ans := a.recvType("access.answer"); ans["ok"] != false || ans["code"] != cluster.AccessRateLimited {
		t.Fatalf("answer = %v", ans)
	}
	// what stops the home, with its code (the window passed)
	f.hub.acc.mu.Lock()
	f.hub.acc.asked = nil
	f.hub.acc.mu.Unlock()
	g.mu.Lock()
	g.err = &cluster.AccessError{Code: cluster.AccessNoAdmin, Reason: "this home's database user may not change this machine's role"}
	g.mu.Unlock()
	a.send(map[string]any{"t": "access.request", "id": "ask-3"})
	if ans := a.recvType("access.answer"); ans["ok"] != false || ans["code"] != cluster.AccessNoAdmin ||
		!strings.Contains(ans["reason"].(string), "may not change") {
		t.Fatalf("answer = %v", ans)
	}
	// the machine refuses what it is given (a home of an earlier term): not_taken, its words
	f.hub.acc.mu.Lock()
	f.hub.acc.asked = nil
	f.hub.acc.mu.Unlock()
	g.mu.Lock()
	g.err = nil
	g.mu.Unlock()
	a.send(map[string]any{"t": "access.request", "id": "ask-4"})
	cmd = a.recvType("cmd")
	a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": false, "detail": "from a home of term 3"})
	if ans := a.recvType("access.answer"); ans["ok"] != false || ans["code"] != cluster.AccessNotTaken ||
		!strings.Contains(ans["reason"].(string), "term 3") {
		t.Fatalf("answer = %v", ans)
	}
	// a worker's session asks: not a machine's agent, nothing made
	w, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, workerID: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	before := g.grants()
	w.send(map[string]any{"t": "access.request", "id": "ask-5"})
	if ans := w.recvType("access.answer"); ans["ok"] != false || ans["code"] != cluster.AccessUnknown || g.grants() != before {
		t.Fatalf("a worker's request: %v (%d grants)", ans, g.grants()-before)
	}
	logged := ""
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		f.hub.files.Flush()
		if logged = strings.Join(readLines(t, eventsPath(f)), "\n"); strings.Count(logged, `"access_request"`) >= 5 {
			break
		}
	}
	if strings.Count(logged, `"access_request"`) < 5 || strings.Contains(logged, "fresh") {
		t.Fatalf("the events file: %s", logged)
	}
}
