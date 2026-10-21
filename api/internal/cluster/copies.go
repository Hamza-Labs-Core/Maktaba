package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
)

// Copies is the home's side of its machines' copies of its database
// (specs/epics/37-node-replica): while a machine follows it, it writes its clock into
// cluster_replica_beat every BeatEvery (a copy then knows how far it has applied the
// home's changes); it publishes its database's TLS CA there (a machine pins it for its
// replication); it gives up on a machine so far behind that what the home keeps for it
// passes the cap (rules.cluster.replica_wal_keep_gb): that slot is dropped, and the
// machine copies the database anew when it is back; and every beat it drops the slots
// that are not Maktaba's (guardSlots).
type Copies struct {
	DB *sql.DB
	// CAFile is the database's TLS CA (MAKTABA_CLUSTER_PG_CA; "": none).
	CAFile string
	// KeepGB returns the cap (0: none).
	KeepGB func() int
	// Bundled: the database is Maktaba's own Postgres (no physical slot there is anyone's).
	Bundled bool
	Logger  *slog.Logger

	// kept is the cap Postgres was last set to (-1: never)
	kept int
	set  bool
	// told are the slots already logged as dropped or failing to drop
	told map[string]bool
}

// BeatEvery is how often the home writes its beat while a machine follows it.
const BeatEvery = 5 * time.Second

// copySlots matches the machines' slots (LIKE).
const copySlots = `maktaba\_copy\_%`

// Slots Maktaba keeps on the home's database: a machine's (maktaba_copy_<id>), the one an
// emergency keeps of what the new home changed since (maktaba_fork_<term>), and the ones a
// machine's subscription makes for a while to copy each table at first
// (pg_<subscription>_sync_<table>_<system>).
var (
	copySlotName  = regexp.MustCompile(`^maktaba_copy_([0-9a-f]{32})$`)
	forkSlotName  = regexp.MustCompile(`^maktaba_fork_[0-9]+$`)
	tableSyncName = regexp.MustCompile(`^pg_[0-9]+_sync_[0-9]+_[0-9]+$`)
)

// slotRow is a replication slot as guardSlots sees it.
type slotRow struct {
	name, kind, plugin string
	active, here       bool
	pid                int
}

// foreign is why a slot is not Maktaba's ("": it is): copying lists the machines that may
// have a copy (their ids, without dashes).
func (s slotRow) foreign(copying map[string]bool, bundled bool) string {
	if s.kind == "physical" {
		if bundled && !s.active {
			return "a physical slot: no machine has one"
		}
		return ""
	}
	if !s.here {
		return "" // another database's: it decodes nothing of this one
	}
	if s.plugin != "pgoutput" {
		// any other plugin (test_decoding) decodes every table, past the publications
		return "made with " + s.plugin + ", not pgoutput"
	}
	if m := copySlotName.FindStringSubmatch(s.name); m != nil {
		if copying[m[1]] {
			return ""
		}
		return "its machine may not have a copy"
	}
	if forkSlotName.MatchString(s.name) || tableSyncName.MatchString(s.name) {
		return ""
	}
	return "not Maktaba's"
}

// guardSlots drops the slots of the home's database that are not Maktaba's: one made
// with an output plugin other than pgoutput (it would decode every table, past what is
// published to the machine that made it), one Maktaba did not make or whose machine may
// no longer have a copy (it would hold the home's changes for ever), and on Maktaba's own
// Postgres an idle physical one. A slot in use has its connection ended first.
func (c *Copies) guardSlots(ctx context.Context) {
	gctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := c.DB.QueryContext(gctx, `SELECT slot_name, slot_type, COALESCE(plugin, ''), active,
	    COALESCE(active_pid, 0), COALESCE(database = current_database(), false) FROM pg_replication_slots`)
	if err != nil {
		return
	}
	var slots []slotRow
	for rows.Next() {
		var s slotRow
		if rows.Scan(&s.name, &s.kind, &s.plugin, &s.active, &s.pid, &s.here) == nil {
			slots = append(slots, s)
		}
	}
	_ = rows.Close()
	if len(slots) == 0 {
		return
	}
	copying := map[string]bool{}
	if rows, err := c.DB.QueryContext(gctx, `SELECT replace(id::text, '-', '') FROM cluster_nodes
	    WHERE state = 'approved' AND full_node`); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				copying[id] = true
			}
		}
		_ = rows.Close()
	} else {
		return // which machines may have a copy is not known: nothing is dropped
	}
	if c.told == nil {
		c.told = map[string]bool{}
	}
	for _, s := range slots {
		why := s.foreign(copying, c.Bundled)
		if why == "" {
			continue
		}
		if s.active && s.pid > 0 {
			_, _ = c.DB.ExecContext(gctx, `SELECT pg_terminate_backend($1)`, s.pid)
		}
		_, err := c.DB.ExecContext(gctx, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots
		    WHERE slot_name = $1 AND NOT active`, s.name)
		if c.told[s.name] && err != nil {
			continue
		}
		c.told[s.name] = err != nil
		if err != nil {
			c.log().Warn("cluster: a replication slot that is not Maktaba's could not be dropped", "slot", s.name,
				"plugin", s.plugin, "why", why, "err", err, "event", "cluster_slot_foreign_failed")
			continue
		}
		c.log().Warn("cluster: a replication slot that is not Maktaba's was dropped", "slot", s.name,
			"plugin", s.plugin, "why", why, "event", "cluster_slot_foreign_dropped")
	}
}

// Run writes the beat, publishes the CA and holds the cap until ctx ends.
func (c *Copies) Run(ctx context.Context) {
	t := time.NewTicker(BeatEvery)
	defer t.Stop()
	var minute time.Time
	for {
		c.beat(ctx)
		c.guardSlots(ctx)
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
	gb := c.KeepGB()
	if gb > 0 {
		c.dropOver(ctx, int64(gb)<<30)
	}
	c.keepAtMost(ctx, gb)
}

// keepAtMost sets Postgres's own cap on what a slot holds (max_slot_wal_keep_size) to
// the rule when it changed, so the cap holds while this API does not run (the bundled
// Postgres lets the app role set it; another database does not, which is said once).
func (c *Copies) keepAtMost(ctx context.Context, gb int) {
	if c.set && c.kept == gb {
		return
	}
	kctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	q := `ALTER SYSTEM RESET max_slot_wal_keep_size`
	if gb > 0 {
		q = fmt.Sprintf(`ALTER SYSTEM SET max_slot_wal_keep_size = '%dGB'`, gb)
	}
	_, err := c.DB.ExecContext(kctx, q)
	if err == nil {
		_, err = c.DB.ExecContext(kctx, `SELECT pg_reload_conf()`)
	}
	if err != nil {
		if !c.set && c.Logger != nil {
			c.Logger.Info("cluster: the database's own cap on a copy's changes cannot be set: the home holds it alone",
				"err", err, "event", "cluster_wal_keep_unset")
		}
		c.set, c.kept = true, -1
		return
	}
	c.set, c.kept = true, gb
}

// dropOver drops the slots of the machines that do not follow (theirs, and the ones their
// subscriptions made to copy a table at first) and that the home keeps more than limit
// bytes of changes for.
func (c *Copies) dropOver(ctx context.Context, limit int64) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := c.DB.QueryContext(cctx, `SELECT slot_name, pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)::bigint
	  FROM pg_replication_slots
	 WHERE (slot_name LIKE $1 OR slot_name ~ $3) AND NOT active AND restart_lsn IS NOT NULL
	   AND pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn) > $2::bigint`, copySlots, limit, tableSyncName.String())
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
