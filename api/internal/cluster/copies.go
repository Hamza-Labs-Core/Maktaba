package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Copies is the home's side of its machines' copies of its database
// (specs/epics/37-node-replica): while a machine follows it, it writes its clock into
// cluster_replica_beat every BeatEvery (a copy then knows how far it has applied the
// home's changes); it publishes its database's TLS CA there (a machine pins it for its
// replication); it gives up on a machine so far behind that what the home keeps for it
// passes the cap (rules.cluster.replica_wal_keep_gb): that slot is dropped, and the
// machine copies the database anew when it is back; and every beat it drops the slots
// that are not Maktaba's (guardSlots). A database that is not Maktaba's own Postgres (an
// operator's) may have output plugins other than pgoutput, which the sweep alone guards
// against: the machines keep no copy of it unless the admin allows it
// (rules.cluster.copies_on_operator_postgres): until then they may not ask for one
// (holdDoor), and the copies they have end (endCopies).
type Copies struct {
	DB *sql.DB
	// CAFile is the database's TLS CA (MAKTABA_CLUSTER_PG_CA; "": none).
	CAFile string
	// KeepGB returns the cap (0: none).
	KeepGB func() int
	// OperatorPG reads rules.cluster.copies_on_operator_postgres: on a database that is not
	// Maktaba's own Postgres the machines keep copies only while it is on (nil: off). An
	// error leaves things as they are for that beat: no copy is ended for a rule not read.
	OperatorPG func(ctx context.Context) (bool, error)
	// Bundled: the database is Maktaba's own Postgres (no physical slot there is anyone's).
	Bundled bool
	Logger  *slog.Logger

	// kept is the cap Postgres was last set to (-1: never)
	kept int
	set  bool
	// told are the slots already warned about as not dropped (once a slot, while it lasts)
	told map[string]bool
	// refusedTold are the machines already logged as refused their copy (once while
	// copies are refused); doorTold: that the grant's callers could not be changed was
	// said; onTold: that copies on an operator's Postgres are on was said
	refusedTold      map[string]bool
	doorTold, onTold bool
}

// BeatEvery is how often the home writes its beat while a machine follows it.
const BeatEvery = 5 * time.Second

// copySlots matches the machines' slots (LIKE).
const copySlots = `maktaba\_copy\_%`

// grantFunc is what a machine calls, as its own role, to ask for its copy (slot 0147).
const grantFunc = `cluster_node_replica_grant(uuid, text, boolean)`

// Why the machines keep no copy of a database that is not Maktaba's own Postgres, and
// what the admin does about it.
const (
	refusedOperatorPG = "this home's database is not Maktaba's own Postgres, and copies on such a database " +
		"are off (rules.cluster.copies_on_operator_postgres)"
	fixRefused = "remove every logical decoding output plugin but pgoutput from that Postgres (test_decoding, " +
		"wal2json and the like), then turn on rules.cluster.copies_on_operator_postgres (Settings, Rules, Machines); " +
		"or run the home on Maktaba's own Postgres"
)

// What to do about a slot that is not Maktaba's and could not be dropped.
const (
	fixEnd = "end its connection yourself (pg_terminate_backend; a superuser's only a superuser may end), or " +
		"give this home's database user pg_signal_backend (Maktaba's own Postgres does): the home drops the " +
		"slot at its next beat; and remove the output plugin it uses from that Postgres"
	fixBusy = "the home tries again at every beat; if the slot stays in use, end its connection yourself " +
		"(pg_terminate_backend) and drop it (pg_drop_replication_slot)"
	fixDrop = "drop it yourself (pg_drop_replication_slot), or give this home's database user REPLICATION " +
		"(Maktaba's own Postgres does)"
)

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
}

// foreign is why a slot is not Maktaba's ("": it is): copying lists the machines that may
// have a copy (their ids, without dashes); refused is why none may now ("": they may), and
// then a machine's slot, or one its subscription made to copy a table, is no one's either.
func (s slotRow) foreign(copying map[string]bool, bundled bool, refused string) string {
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
		switch {
		case refused != "":
			return refused
		case copying[m[1]]:
			return ""
		}
		return "its machine may not have a copy"
	}
	if tableSyncName.MatchString(s.name) {
		return refused
	}
	if forkSlotName.MatchString(s.name) {
		return ""
	}
	return "not Maktaba's"
}

// refusal is why the machines may keep no copy of this database now ("": they may): a
// database that is not Maktaba's own Postgres keeps copies only with the admin's rule.
// known is false when the rule could not be read.
func (c *Copies) refusal(ctx context.Context) (why string, known bool) {
	if c.Bundled {
		return "", true
	}
	if c.OperatorPG == nil {
		return refusedOperatorPG, true
	}
	on, err := c.OperatorPG(ctx)
	switch {
	case err != nil:
		return "", false
	case on:
		return "", true
	}
	return refusedOperatorPG, true
}

// guardSlots drops the slots of the home's database that are not Maktaba's: one made
// with an output plugin other than pgoutput (it would decode every table, past what is
// published to the machine that made it), one Maktaba did not make or whose machine may
// no longer have a copy (it would hold the home's changes for ever), and on Maktaba's own
// Postgres an idle physical one. A slot in use has its connection ended first
// (dropSlot); one that cannot be ended or dropped is said once, with what to do. While the
// machines may keep no copy (refusal) they may not ask for one (holdDoor), and the copies
// they have are ended (endCopies). A beat that could not read the rule changes neither.
func (c *Copies) guardSlots(ctx context.Context) {
	gctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	refused, known := c.refusal(gctx)
	switch {
	case !known:
		// the rule could not be read: the door stays as it is, and the machines' copies are
		// judged as when they may have one
	case refused != "":
		c.holdDoor(gctx, refused)
		c.onTold = false
		c.endCopies(gctx, refused)
	default:
		c.holdDoor(gctx, refused)
		c.refusedTold = nil
		if !c.Bundled && !c.onTold {
			c.onTold = true
			c.log().Warn("cluster: the machines keep copies of this home's database on a Postgres that is not Maktaba's own",
				"rule", "rules.cluster.copies_on_operator_postgres",
				"why", "a machine's replication role may decode every table with any output plugin that Postgres has "+
					"besides pgoutput, past what is published to it; only this home's sweep drops such a slot",
				"fix", "remove every logical decoding output plugin but pgoutput from that Postgres",
				"event", "cluster_copies_operator_postgres")
		}
	}
	rows, err := c.DB.QueryContext(gctx, `SELECT slot_name, slot_type, COALESCE(plugin, ''), active,
	    COALESCE(database = current_database(), false) FROM pg_replication_slots`)
	if err != nil {
		return
	}
	var slots []slotRow
	present := map[string]bool{}
	for rows.Next() {
		var s slotRow
		if rows.Scan(&s.name, &s.kind, &s.plugin, &s.active, &s.here) == nil {
			slots = append(slots, s)
			present[s.name] = true
		}
	}
	_ = rows.Close()
	// a slot gone (dropped by someone else) is warned about again if it is made again
	for name := range c.told {
		if !present[name] {
			delete(c.told, name)
		}
	}
	if len(slots) == 0 {
		return
	}
	copying := map[string]bool{}
	if refused == "" {
		rows, err := c.DB.QueryContext(gctx, `SELECT replace(id::text, '-', '') FROM cluster_nodes
		    WHERE state = 'approved' AND full_node`)
		if err != nil {
			return // which machines may have a copy is not known: nothing is dropped
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				copying[id] = true
			}
		}
		_ = rows.Close()
	}
	if c.told == nil {
		c.told = map[string]bool{}
	}
	for _, s := range slots {
		why := s.foreign(copying, c.Bundled, refused)
		if why == "" {
			continue
		}
		d := c.dropSlot(gctx, s)
		switch {
		case d.dropped:
			delete(c.told, s.name)
			c.log().Warn("cluster: a replication slot that is not Maktaba's was dropped", "slot", s.name,
				"plugin", s.plugin, "why", why, "stream_ended", d.ended, "event", "cluster_slot_foreign_dropped")
		case d.gone:
			// a temporary slot, or one still being made, goes with its connection; else
			// another dropped it meanwhile
			delete(c.told, s.name)
			if d.ended {
				c.log().Warn("cluster: a replication slot that is not Maktaba's was removed with its connection",
					"slot", s.name, "plugin", s.plugin, "why", why, "event", "cluster_slot_foreign_ended")
			}
		case !c.told[s.name]:
			c.told[s.name] = true
			c.log().Warn("cluster: a replication slot that is not Maktaba's could not be dropped", "slot", s.name,
				"plugin", s.plugin, "why", why, "problem", d.problem, "fix", d.fix, "event", "cluster_slot_foreign_failed")
		}
	}
}

// slotDrop is what became of a slot dropSlot drops: dropped by it, or gone otherwise (a
// temporary slot, or one still being made, goes with the connection ended; or another
// dropped it meanwhile); ended: the connection that used it was ended first. Neither: why
// not, and what to do.
type slotDrop struct {
	dropped, gone, ended bool
	problem, fix         string
}

// dropSlot drops a slot that is not Maktaba's. A slot in use is dropped only once nothing
// uses it: the connection that uses it is ended first (pg_terminate_backend, which needs
// the home's database user to be a superuser or have pg_signal_backend), and the slot is
// dropped once that connection let go of it (within moments; a second for the most).
func (c *Copies) dropSlot(ctx context.Context, s slotRow) slotDrop {
	var d slotDrop
	for i := 0; ; i++ {
		var active bool
		var pid int
		err := c.DB.QueryRowContext(ctx, `SELECT active, COALESCE(active_pid, 0) FROM pg_replication_slots
		    WHERE slot_name = $1`, s.name).Scan(&active, &pid)
		if errors.Is(err, sql.ErrNoRows) {
			d.gone = true
			return d
		}
		if err != nil {
			d.problem, d.fix = "it could not be read: "+err.Error(), fixBusy
			return d
		}
		if !active {
			break
		}
		if !d.ended && pid > 0 {
			// false: that process is gone already
			var ok bool
			if err := c.DB.QueryRowContext(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&ok); err != nil {
				d.problem, d.fix = "its connection could not be ended: "+err.Error(), fixEnd
				return d
			}
			d.ended = true
		}
		if i >= 20 {
			d.problem, d.fix = "it is still in use: its connection did not end", fixBusy
			return d
		}
		select {
		case <-ctx.Done():
			d.problem, d.fix = "it is still in use: its connection did not end in time", fixBusy
			return d
		case <-time.After(50 * time.Millisecond):
		}
	}
	_, err := c.DB.ExecContext(ctx, `SELECT pg_drop_replication_slot($1)`, s.name)
	var pe *pq.Error
	switch {
	case err == nil:
		d.dropped = true
	case errors.As(err, &pe) && pe.Code == "42704":
		d.gone = true
	case errors.As(err, &pe) && pe.Code == "55006":
		d.problem, d.fix = "it is in use again (its client connected again): "+err.Error(), fixBusy
	case errors.As(err, &pe) && pe.Code == "42501":
		d.problem, d.fix = "this home's database user may not drop it: "+err.Error(), fixDrop
	default:
		d.problem, d.fix = "it could not be dropped: "+err.Error(), fixDrop
	}
	return d
}

// holdDoor lets the machines' group call the grant (slot 0147) while the machines may keep
// copies, and not while they may not (refused): a machine refused asks in vain (permission
// denied for the grant), so no replication role of its is made or let in again. Who calls
// the grant is its owner's to change (the user that ran the migrations: this home's own,
// or a superuser); a home whose user may not says so once (its machines' copies are still
// ended at every beat).
func (c *Copies) holdDoor(ctx context.Context, refused string) {
	open := refused == ""
	can, err := c.grantOpen(ctx)
	if err != nil {
		return // no machines' group, or no grant (an older schema): no door to hold
	}
	if can == open {
		c.doorTold = false
		return
	}
	q := `REVOKE EXECUTE ON FUNCTION ` + grantFunc + ` FROM PUBLIC, ` + WorkerGroup
	if open {
		q = `GRANT EXECUTE ON FUNCTION ` + grantFunc + ` TO ` + WorkerGroup
	}
	if _, err = c.DB.ExecContext(ctx, q); err == nil {
		// Postgres may only warn a user that cannot change it: the privilege is read again
		can, err = c.grantOpen(ctx)
	}
	switch {
	case err == nil && can == open && open:
		c.doorTold = false
		c.log().Info("cluster: the machines may ask for a copy of this home's database again",
			"event", "cluster_copies_open")
	case err == nil && can == open:
		c.doorTold = false
		c.log().Info("cluster: the machines may not ask for a copy of this home's database", "why", refused,
			"fix", fixRefused, "event", "cluster_copies_shut")
	case !c.doorTold:
		c.doorTold = true
		problem := "this home's database user does not own " + grantFunc
		if err != nil {
			problem = err.Error()
		}
		fix := "have the user that owns " + grantFunc + " (the one that ran the migrations) revoke EXECUTE on it from " +
			WorkerGroup + " and PUBLIC; until then this home ends the machines' copies at every beat"
		if open {
			fix = "have the user that owns " + grantFunc + " (the one that ran the migrations) grant EXECUTE on it to " +
				WorkerGroup + "; until then the machines cannot ask for a copy"
		}
		c.log().Warn("cluster: who may ask for a copy of this home's database could not be changed", "open", open,
			"why", refused, "problem", problem, "fix", fix, "event", "cluster_copies_door_failed")
	}
}

// grantOpen reports whether the machines' group may call the grant.
func (c *Copies) grantOpen(ctx context.Context) (bool, error) {
	var can bool
	err := c.DB.QueryRowContext(ctx, `SELECT has_function_privilege($1, $2, 'EXECUTE')`, WorkerGroup, grantFunc).Scan(&can)
	return can, err
}

// endCopies ends the copies this home's machines have while none may (refused says why),
// as a machine's removal does (PGRoles.endCopy): its replication role may no longer sign
// in and is dropped, its connections are ended, its slot and its publication are dropped.
// Each full machine refused is logged once while copies stay refused.
func (c *Copies) endCopies(ctx context.Context, refused string) {
	rows, err := c.DB.QueryContext(ctx, `SELECT n.id::text, n.name, n.state = 'approved' AND n.full_node,
	        EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_rep_' || replace(n.id::text, '-', ''))
	     OR EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'maktaba_copy_' || replace(n.id::text, '-', ''))
	     OR EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = 'maktaba_copy_' || replace(n.id::text, '-', '')
	                AND database = current_database())
	  FROM cluster_nodes n`)
	if err != nil {
		return
	}
	type machine struct {
		id, name   string
		full, kept bool
	}
	var ms []machine
	for rows.Next() {
		var m machine
		if rows.Scan(&m.id, &m.name, &m.full, &m.kept) == nil {
			ms = append(ms, m)
		}
	}
	_ = rows.Close()
	if c.refusedTold == nil {
		c.refusedTold = map[string]bool{}
	}
	roles := &PGRoles{DB: c.DB}
	for _, m := range ms {
		if m.kept {
			roles.endCopy(ctx, RoleName(m.id), true)
		}
		if (m.full || m.kept) && !c.refusedTold[m.id] {
			c.refusedTold[m.id] = true
			c.log().Warn("cluster: a machine may not keep a copy of this home's database", "node", m.id,
				"name", m.name, "why", refused, "had_copy", m.kept, "fix", fixRefused, "event", "cluster_copy_refused")
		}
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
