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

// trustedNodes is the approved nodes' addresses, as last read.
type trustedNodes struct {
	set atomic.Pointer[map[netip.Addr]bool]
}

// Has reports whether ip is an approved node's address (clientip.MiddlewareWith's extra).
func (n *trustedNodes) Has(ip netip.Addr) bool {
	m := n.set.Load()
	return m != nil && (*m)[ip.Unmap()]
}

// load reads the set once; an error keeps the last one.
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
	n.set.Store(&m)
	return nil
}

// run reads the set every interval until ctx ends (a database without the table yet, or
// one not answering, keeps the last set and is said once).
func (n *trustedNodes) run(ctx context.Context, db *sql.DB, every time.Duration, logger *slog.Logger) {
	said := false
	for {
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := n.load(qctx, db)
		cancel()
		if err != nil && !said && logger != nil {
			logger.Debug("approved nodes' addresses not read (their forwarded clients are not believed yet)", "error", err)
			said = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
