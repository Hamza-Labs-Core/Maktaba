package cluster

import (
	"context"
	"strings"
	"time"

	"github.com/lib/pq"
)

// A machine's copy of the home's database (specs/epics/37-node-replica, slot 0144's
// cluster_node_replica_grant): its replication role, its publication and its slot (the
// last two share a name), named after its id as its database role is.

// ReplicaGroup is the group role every machine's replication role joins (slot 0144).
const ReplicaGroup = "maktaba_replicas"

// CopyNames are a machine's replication role and slot, from its database role (false:
// not a machine role).
func CopyNames(role string) (copyRole, slot string, ok bool) {
	if !ValidRole(role) {
		return "", "", false
	}
	hex := strings.TrimPrefix(role, "maktaba_node_")
	return "maktaba_rep_" + hex, "maktaba_copy_" + hex, true
}

// endCopy stops a machine's copy from following this database: its replication role
// may no longer sign in (password rotated), its sessions (the replication's included)
// are ended, its slot is dropped (so the changes it kept are released) and so is its
// publication; with drop the role goes too. Best effort: a home whose user may not do
// one of these leaves it.
func (p *PGRoles) endCopy(ctx context.Context, role string, drop bool) {
	copyRole, slot, ok := CopyNames(role)
	if !ok {
		return
	}
	var exists bool
	_ = p.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, copyRole).Scan(&exists)
	if exists {
		if pw, err := NewPassword(); err == nil {
			_, _ = p.DB.ExecContext(ctx, `ALTER ROLE `+pq.QuoteIdentifier(copyRole)+` NOLOGIN PASSWORD `+pq.QuoteLiteral(pw))
		}
		_, _ = p.DB.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, copyRole)
	}
	for i := 0; i < 5; i++ {
		var active bool
		err := p.DB.QueryRowContext(ctx, `SELECT active FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&active)
		if err != nil {
			break // no such slot (or not readable)
		}
		if !active {
			_, _ = p.DB.ExecContext(ctx, `SELECT pg_drop_replication_slot($1)`, slot)
			break
		}
		_, _ = p.DB.ExecContext(ctx,
			`SELECT pg_terminate_backend(active_pid) FROM pg_replication_slots WHERE slot_name = $1 AND active`, slot)
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	_, _ = p.DB.ExecContext(ctx, `DROP PUBLICATION IF EXISTS `+pq.QuoteIdentifier(slot))
	if drop && exists {
		_, _ = p.DB.ExecContext(ctx, `DROP ROLE IF EXISTS `+pq.QuoteIdentifier(copyRole))
	}
}
