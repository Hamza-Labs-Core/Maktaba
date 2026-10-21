package cluster

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Copies is the home's side of its machines' copies of its database
// (specs/epics/37-node-replica): while a machine follows it, it writes its clock into
// cluster_replica_beat every BeatEvery (a copy then knows how far it has applied the
// home's changes); it publishes its database's TLS CA there (a machine pins it for its
// replication); and it gives up on a machine so far behind that what the home keeps
// for it passes the cap (rules.cluster.replica_wal_keep_gb): that slot is dropped, and
// the machine copies the database anew when it is back.
type Copies struct {
	DB *sql.DB
	// CAFile is the database's TLS CA (MAKTABA_CLUSTER_PG_CA; "": none).
	CAFile string
	// KeepGB returns the cap (0: none).
	KeepGB func() int
	Logger *slog.Logger
}

// BeatEvery is how often the home writes its beat while a machine follows it.
const BeatEvery = 5 * time.Second

// copySlots matches the machines' slots (LIKE).
const copySlots = `maktaba\_copy\_%`

// Run writes the beat, publishes the CA and holds the cap until ctx ends.
func (c *Copies) Run(ctx context.Context) {
	t := time.NewTicker(BeatEvery)
	defer t.Stop()
	var minute time.Time
	for {
		c.beat(ctx)
		if time.Since(minute) >= time.Minute {
			minute = time.Now()
			c.publishCA(ctx)
			c.holdCap(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Copies) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Copies) beat(ctx context.Context) {
	bctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _ = c.DB.ExecContext(bctx, `UPDATE cluster_replica_beat SET at = now()
	 WHERE id = 1 AND EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name LIKE $1)`, copySlots)
}

func (c *Copies) publishCA(ctx context.Context) {
	var ca sql.NullString
	if c.CAFile != "" {
		if b, err := os.ReadFile(c.CAFile); err == nil && strings.TrimSpace(string(b)) != "" {
			ca = sql.NullString{String: string(b), Valid: true}
		}
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _ = c.DB.ExecContext(pctx, `UPDATE cluster_replica_beat SET ca_pem = $1 WHERE id = 1 AND ca_pem IS DISTINCT FROM $1`, ca)
}

// holdCap drops the slot of a machine that does not follow and that the home keeps more
// than the cap of changes for.
func (c *Copies) holdCap(ctx context.Context) {
	if c.KeepGB == nil {
		return
	}
	if gb := c.KeepGB(); gb > 0 {
		c.dropOver(ctx, int64(gb)<<30)
	}
}

// dropOver drops the slots of the machines that do not follow and that the home keeps
// more than limit bytes of changes for.
func (c *Copies) dropOver(ctx context.Context, limit int64) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := c.DB.QueryContext(cctx, `SELECT slot_name, pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)::bigint
	  FROM pg_replication_slots
	 WHERE slot_name LIKE $1 AND NOT active AND restart_lsn IS NOT NULL
	   AND pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn) > $2::bigint`, copySlots, limit)
	if err != nil {
		return
	}
	type over struct {
		slot string
		kept int64
	}
	var drop []over
	for rows.Next() {
		var o over
		if rows.Scan(&o.slot, &o.kept) == nil {
			drop = append(drop, o)
		}
	}
	_ = rows.Close()
	for _, o := range drop {
		if _, err := c.DB.ExecContext(cctx, `SELECT pg_drop_replication_slot($1)`, o.slot); err != nil {
			c.log().Warn("cluster: a machine's copy is past the cap, and its slot could not be dropped",
				"slot", o.slot, "kept_bytes", o.kept, "err", err, "event", "cluster_copy_cap_failed")
			continue
		}
		c.log().Warn("cluster: a machine's copy fell too far behind; it copies the database anew when it is back",
			"slot", o.slot, "kept_bytes", o.kept, "cap_bytes", limit, "event", "cluster_copy_dropped")
	}
}
