package main

// The home's approved nodes forward their clients' requests to it (homeproxy, Epic 35 story
// 35.10), with the client each node resolved in X-Forwarded-For. The home believes that from
// an approved node's own address (cluster_nodes.remote_addr), besides loopback and
// [server] trusted_proxies (Epic 59 story 59.08): without it everyone signing in through a
// node would share the node's address, and with it one rate-limit key. The set is read
// again every 30 s, so a node approved or removed counts within that.

import (
	"context"
	"database/sql"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"time"
)

// trustedNodesEvery is how often the approved nodes' addresses are read again.
const trustedNodesEvery = 30 * time.Second

const trustedNodesSQL = `SELECT DISTINCT remote_addr FROM cluster_nodes
  WHERE state = 'approved' AND remote_addr IS NOT NULL AND remote_addr <> ''`

// trustedNodesStale is how old a read may get before the set stops counting: a database that
// stopped answering takes the trust away (fail closed) rather than keeping a removed node's.
const trustedNodesStale = 3 * trustedNodesEvery

// nodeSet is one read of the approved nodes' addresses and when it was made.
type nodeSet struct {
	addrs map[netip.Addr]bool
	at    time.Time
}

// trustedNodes is the approved nodes' addresses, as last read.
type trustedNodes struct {
	set atomic.Pointer[nodeSet]
	now func() time.Time // time.Now when nil (tests set it)
}

func (n *trustedNodes) clock() time.Time {
	if n.now != nil {
		return n.now()
	}
	return time.Now()
}

// Has reports whether ip is an approved node's address (clientip.MiddlewareWith's extra). A
// read older than trustedNodesStale counts as none.
func (n *trustedNodes) Has(ip netip.Addr) bool {
	s := n.set.Load()
	if s == nil || n.clock().Sub(s.at) > trustedNodesStale {
		return false
	}
	return s.addrs[ip.Unmap()]
}

// load reads the set once; on an error the last read stays until it is stale.
func (n *trustedNodes) load(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, trustedNodesSQL)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	m := map[netip.Addr]bool{}
	for rows.Next() {
		var s string
		if rows.Scan(&s) != nil {
			continue
		}
		if ip, err := netip.ParseAddr(s); err == nil {
			m[ip.WithZone("").Unmap()] = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n.set.Store(&nodeSet{addrs: m, at: n.clock()})
	return nil
}

// run reads the set every interval until ctx ends. The first failure of a run of them is a
// warning (a database without the table yet, or one not answering), and the read that ends
// it is said too.
func (n *trustedNodes) run(ctx context.Context, db *sql.DB, every time.Duration, logger *slog.Logger) {
	failing := false
	for {
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := n.load(qctx, db)
		cancel()
		switch {
		case err != nil && !failing:
			failing = true
			if logger != nil {
				logger.Warn("approved nodes' addresses not read; their forwarded clients stop being believed once the last read is stale",
					"error", err, "stale_after", trustedNodesStale)
			}
		case err == nil && failing:
			failing = false
			if logger != nil {
				logger.Info("approved nodes' addresses read again")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
