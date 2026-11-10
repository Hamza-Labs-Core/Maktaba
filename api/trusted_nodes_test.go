package main

import (
	"context"
	"database/sql"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Epic 59 story 59.08: the home trusts what its approved nodes forward, and only theirs:
// a node still waiting, one removed, an empty or unreadable address are not trusted.
func TestTrustedNodes(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	n := &trustedNodes{}
	if n.Has(netip.MustParseAddr("192.168.1.30")) {
		t.Fatal("nothing read yet, yet trusted")
	}
	// no table yet (a database not migrated): kept empty, no panic
	if err := n.load(context.Background(), db); err == nil {
		t.Fatal("no table, no error")
	}
	for _, q := range []string{
		`CREATE TABLE cluster_nodes (state TEXT, remote_addr TEXT)`,
		`INSERT INTO cluster_nodes VALUES ('approved','192.168.1.30'),('pending','192.168.1.31'),
		   ('removed','192.168.1.32'),('approved',''),('approved','bogus'),('approved','fd7a::5'),('approved',NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.run(ctx, db, time.Hour, nil); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for !n.Has(netip.MustParseAddr("192.168.1.30")) {
		if time.Now().After(deadline) {
			t.Fatal("the approved node is not trusted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	for addr, want := range map[string]bool{"192.168.1.30": true, "::ffff:192.168.1.30": true, "fd7a::5": true,
		"192.168.1.31": false, "192.168.1.32": false, "192.168.1.99": false} {
		if got := n.Has(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v, want %v", addr, got, want)
		}
	}
	// a database that stops answering: the last read counts until it is stale, then nothing
	now := time.Now()
	n.now = func() time.Time { return now }
	if err := n.load(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := n.load(context.Background(), db); err == nil {
		t.Fatal("closed database, no error")
	}
	now = now.Add(trustedNodesStale - time.Second)
	if !n.Has(netip.MustParseAddr("192.168.1.30")) {
		t.Fatal("a fresh read stopped counting")
	}
	now = now.Add(2 * time.Second)
	if n.Has(netip.MustParseAddr("192.168.1.30")) {
		t.Fatal("a stale read still trusted")
	}
}
