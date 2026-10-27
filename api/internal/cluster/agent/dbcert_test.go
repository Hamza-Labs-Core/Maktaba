package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A machine agent's word on its processes' certificates on the home's database
// (update.state's db_client_cert) is kept for the home's Postgres (Config.DBClientCert):
// when its session first says it and each time it changes, the latest word winning; an
// unchanged word is not written again, and a machine back on an older release (no word)
// is "no". Only a machine's own agent is heard.
func TestAMachinesWordOnItsDatabaseCertificateIsKept(t *testing.T) {
	var mu sync.Mutex
	var kept []bool
	var nodes []string
	f := newFixture(t, func(c *Config) {
		c.DBClientCert = func(_ context.Context, nodeID string, presents bool) error {
			mu.Lock()
			defer mu.Unlock()
			kept, nodes = append(kept, presents), append(nodes, nodeID)
			return nil
		}
	})
	words := func() []bool {
		mu.Lock()
		defer mu.Unlock()
		return append([]bool(nil), kept...)
	}
	waitFor := func(n int) []bool {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if w := words(); len(w) >= n {
				return w
			}
		}
		t.Fatalf("kept %v, want %d words", words(), n)
		return nil
	}
	id, priv := f.approvedNode("studio-gpu")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, process: "supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	a.send(map[string]any{"t": "update.state", "version": "1.5.0", "db_client_cert": true})
	if w := waitFor(1); !w[0] {
		t.Fatalf("kept %v", w)
	}
	a.send(map[string]any{"t": "update.state", "version": "1.5.0", "db_client_cert": true})
	a.send(map[string]any{"t": "update.state", "version": "1.4.0"}) // an older release: no word
	if w := waitFor(2); w[1] {
		t.Fatalf("an older release's state kept as presenting: %v", w)
	}
	time.Sleep(100 * time.Millisecond)
	if w := words(); len(w) != 2 {
		t.Fatalf("an unchanged word written again: %v", w)
	}
	mu.Lock()
	for _, n := range nodes {
		if n != id {
			t.Errorf("kept for %q, not the machine %q", n, id)
		}
	}
	mu.Unlock()

	// a process that is not the machine's agent is not heard
	p, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv})
	if err != nil {
		t.Fatal(err)
	}
	p.send(map[string]any{"t": "update.state", "version": "1.5.0", "db_client_cert": true})
	time.Sleep(150 * time.Millisecond)
	if w := words(); len(w) != 2 {
		t.Fatalf("another process's word kept: %v", w)
	}
}
