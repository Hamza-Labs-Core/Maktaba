package switchover

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbfence"
)

// The database's name, its app role (pgbundle.AppRole), and the copy's subscription
// (nodecopy.Subscription).
const (
	database     = "maktaba"
	appRole      = "maktaba"
	subscription = "maktaba_copy"
)

// Freeze stops the old home's writes for switchover id: its database read-only for every
// session from now (default_transaction_read_only), the app's roles' rights to write its
// tables taken (dbfence: a session's own BEGIN READ WRITE overrides the default), the
// sessions open now ended once those are gone (a transaction that wrote before cannot
// commit after the marker), and as its last write the switchover's row set to "frozen":
// the marker a copy holds once it has everything the old home wrote. super is the old
// home's superuser.
func Freeze(ctx context.Context, super *sql.DB, id string) error {
	if err := stopWrites(ctx, super); err != nil {
		return fmt.Errorf("switchover: freeze: %w", err)
	}
	tx, err := super.BeginTx(ctx, &sql.TxOptions{ReadOnly: false})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET TRANSACTION READ WRITE`); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE cluster_switchovers SET state = 'frozen', frozen_at = now(), updated_at = now()
	    WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("switchover: freeze: the marker: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("switchover: freeze: no switchover %s", id)
	}
	return tx.Commit()
}

// Thaw lets the old home write again (a switchover that did not go through): the rights
// a freeze took given back, the database's default read-write.
func Thaw(ctx context.Context, super *sql.DB) error {
	if err := dbfence.Release(ctx, super); err != nil {
		return fmt.Errorf("switchover: thaw: the app's rights: %w", err)
	}
	_, err := super.ExecContext(ctx, `ALTER DATABASE `+database+` RESET default_transaction_read_only`)
	return err
}

// stopWrites stops the app's roles writing (Freeze, Fence): the database read-only by
// default, their rights taken (in a transaction of its own: a role may not reconnect
// and write between the two), then every session of the app's ended, waiting for each
// to go so nothing it wrote commits later.
func stopWrites(ctx context.Context, super *sql.DB) error {
	if _, err := super.ExecContext(ctx, `ALTER DATABASE `+database+` SET default_transaction_read_only = on`); err != nil {
		return err
	}
	tx, err := super.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET TRANSACTION READ WRITE`); err != nil {
		return err
	}
	if err := dbfence.Take(ctx, tx); err != nil {
		return fmt.Errorf("the app's rights to write: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := super.ExecContext(ctx, `SELECT pg_terminate_backend(pid, 5000) FROM pg_stat_activity
	    WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'`); err != nil {
		return fmt.Errorf("end the open sessions: %w", err)
	}
	return nil
}

// HasMarker reports whether a copy holds switchover id's marker: everything the old home
// wrote before it froze.
func HasMarker(ctx context.Context, db *sql.DB, id string) (bool, error) {
	var state string
	err := db.QueryRowContext(ctx, `SELECT state FROM cluster_switchovers WHERE id = $1`, id).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	return state != "checking" && state != "freezing", nil
}

// Machine is a machine's registration on the new home that the old home did not have:
// the old home itself, joining the new one as a machine.
type Machine struct {
	ID, Name, OS, Arch, PublicKey string
	// RolePassword and CopyPassword are its database role's and its copy's replication
	// role's (it keeps them; never in the kit)
	RolePassword, CopyPassword string
	WebURL                     string
}

// Role and CopyRole are a machine's database roles (cluster.RoleName,
// nodecopy.Copy.Role); Slot its copy's slot and publication.
func (m Machine) id() string       { return strings.ReplaceAll(strings.ToLower(m.ID), "-", "") }
func (m Machine) Role() string     { return "maktaba_node_" + m.id() }
func (m Machine) CopyRole() string { return "maktaba_rep_" + m.id() }
func (m Machine) Slot() string     { return "maktaba_copy_" + m.id() }

// Promotion is what makes a machine's copy the home.
type Promotion struct {
	// Self is the machine taking over (its row leaves the machines: it is the home now).
	Self string
	// Name is its name (the term's home).
	Name string
	// Term starts with it; Reason is "planned" or "emergency".
	Term   int64
	Reason string
	// Kit is what the old home handed over (nil: none, an emergency without a standby
	// kit: the machines' roles and the home's secrets are not here).
	Kit *Kit
	// OldHome joins as a machine (nil: it does not, an emergency).
	OldHome *Machine
	// Gap is how far past the copy's highest values the sequences start when the kit
	// does not say where the old home's were (an emergency: what it wrote after the copy's
	// last change is kept aside on it, and keeps its ids).
	Gap int64
	// Fork: the new home keeps every change it makes from the promotion on (ForkSlot), so
	// what the old home wrote after the cut is applied only where this home changed
	// nothing (an emergency).
	Fork bool
	// Record is the switch's row, written with the term (an emergency's: a planned switch's
	// row is the old home's, copied here); a switch the copy shows under way is cancelled.
	Record *Record
}

// Record is an emergency's switch row: its id, who asked, the old home's name.
type Record struct {
	ID, FromName, RequestedBy string
}

// EmergencyGap is how far an emergency promotion moves the sequences past the copy's
// highest values: room for what the old home wrote after the cut, which keeps its ids.
const EmergencyGap = 1_000_000

// ForkSlot is the slot (and its publication, of the tables a machine's copy holds) that
// keeps the new home's changes from an emergency's fork on: what changed here since the
// old home was cut off.
func ForkSlot(term int64) string { return fmt.Sprintf("maktaba_fork_%d", term) }

// Promoted is what a promotion found and made.
type Promoted struct {
	// CutLSN is the old home's position up to which the copy had its changes ("" when
	// unknown).
	CutLSN string
	// Slots are the slots made for the machines before any write (they follow the new
	// home from there without copying it again): by machine id.
	Slots map[string]string
	// SystemID is the new home's database's system identifier.
	SystemID string
}

// Promote makes this machine's copy the home's database. super is its superuser; the copy
// must have been frozen-in (planned: it holds the marker) or not (emergency). In order:
// the kit's rows of the machines replace the copy's (a machine's copy has its own row
// already, published to it: their load must not reach it); every machine that follows the
// new home gets its publication and its slot (and an emergency its fork's), before
// anything else is written; then, in one transaction, the subscription is stopped (its
// position kept as the cut), the kit's other rows replace the copy's (the secrets...), the
// sequences go on from the old home's, the machines' roles are made with their old
// passwords, this machine leaves the machines, the old home's registration and the term
// are written; after it the subscription is dropped. A promotion that fails leaves the
// copy as it was (the transaction undone: it follows its old home still; of the machines'
// rows its own alone, as a copy has it), the slots and publications it made dropped; run
// again, it makes what is missing.
func Promote(ctx context.Context, super *sql.DB, p Promotion) (out Promoted, err error) {
	conn, err := super.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	// this session writes while the copy's readers stay read-only until the commit
	if _, err := conn.ExecContext(ctx, `SET default_transaction_read_only = off`); err != nil {
		return out, err
	}
	if err := conn.QueryRowContext(ctx, `SELECT system_identifier::text FROM pg_control_system()`).Scan(&out.SystemID); err != nil {
		return out, err
	}
	// the subscription's position: the old home's WAL up to which the copy has its changes
	_ = conn.QueryRowContext(ctx, `SELECT COALESCE(s.remote_lsn::text, '')
	    FROM pg_replication_origin_status s JOIN pg_replication_origin o ON o.roident = s.local_id
	    JOIN pg_subscription sub ON o.roname = 'pg_' || sub.oid WHERE sub.subname = $1`, subscription).Scan(&out.CutLSN)
	// the copy whole: a table whose first copy is not done holds part of the home's rows,
	// which would become the library (one promoted already has no subscription left)
	var subscribed bool
	var tables, ready int
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_subscription WHERE subname = $1),
	        (SELECT count(*) FROM pg_subscription_rel r JOIN pg_subscription s ON s.oid = r.srsubid WHERE s.subname = $1),
	        (SELECT count(*) FROM pg_subscription_rel r JOIN pg_subscription s ON s.oid = r.srsubid
	          WHERE s.subname = $1 AND r.srsubstate = 'r')`, subscription).Scan(&subscribed, &tables, &ready); err != nil {
		return out, err
	}
	if subscribed && (tables == 0 || ready < tables) {
		return out, fmt.Errorf("switchover: the copy is not whole (%d of %d tables copied): it cannot become the home", ready, tables)
	}

	// what this promotion made, dropped again when it fails
	var madeSlots, madePubs []string
	nodesLoaded := false
	defer func() {
		if err == nil {
			return
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for _, s := range madeSlots {
			_, _ = conn.ExecContext(cctx, `SELECT pg_drop_replication_slot($1)`, s)
		}
		for _, pub := range madePubs {
			_, _ = conn.ExecContext(cctx, `DROP PUBLICATION IF EXISTS `+pq.QuoteIdentifier(pub))
		}
		if nodesLoaded {
			_ = unloadNodes(cctx, conn, p.Self)
		}
	}()
	if p.Kit != nil {
		if err := loadNodes(ctx, conn, p); err != nil {
			return out, err
		}
		nodesLoaded = true
	}
	has := func(q, name string) (bool, error) {
		var ok bool
		err := conn.QueryRowContext(ctx, q, name).Scan(&ok)
		return ok, err
	}
	slot := func(name string) error {
		exists, err := has(`SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)`, name)
		if err != nil || exists {
			return err
		}
		if _, err := conn.ExecContext(ctx, `SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, name); err != nil {
			return err
		}
		madeSlots = append(madeSlots, name)
		return nil
	}
	pubExists := `SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $1)`

	// every other machine follows the new home from here: its publication and its slot,
	// before anything is written
	out.Slots = map[string]string{}
	ids := kitMachines(p)
	if p.OldHome != nil {
		ids = append(ids, p.OldHome.ID)
	}
	for _, id := range ids {
		m := Machine{ID: id}
		existed, err := has(pubExists, m.Slot())
		if err != nil {
			return out, err
		}
		if _, err := conn.ExecContext(ctx, `SELECT cluster_copy_publication($1)`, id); err != nil {
			return out, fmt.Errorf("switchover: the publication of %s: %w", id, err)
		}
		if !existed {
			madePubs = append(madePubs, m.Slot())
		}
		if err := slot(m.Slot()); err != nil {
			return out, fmt.Errorf("switchover: the slot of %s: %w", id, err)
		}
		out.Slots[id] = m.Slot()
	}
	if p.Fork {
		// the tables a machine's copy holds (what it is already published, so a replication
		// role naming this publication reads nothing more): what is applied of an old home's
		// writes is in them alone (the secrets and the cluster's own rows never are)
		fork := ForkSlot(p.Term)
		existed, err := has(pubExists, fork)
		if err != nil {
			return out, err
		}
		if !existed {
			if _, err := conn.ExecContext(ctx, `DO $$
			    DECLARE t regclass;
			    BEGIN
			    CREATE PUBLICATION `+fork+` WITH (publish = 'insert, update, delete, truncate');
			    FOR t IN SELECT * FROM cluster_copy_tables() LOOP
			        IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indrelid = t AND indisprimary)
			           AND (SELECT relreplident FROM pg_class WHERE oid = t) = 'd' THEN
			            EXECUTE format('ALTER TABLE %s REPLICA IDENTITY FULL', t);
			        END IF;
			        EXECUTE format('ALTER PUBLICATION `+fork+` ADD TABLE %s', t);
			    END LOOP;
			    END $$`); err != nil {
				return out, fmt.Errorf("switchover: the fork's publication: %w", err)
			}
			madePubs = append(madePubs, fork)
		}
		if err := slot(fork); err != nil {
			return out, fmt.Errorf("switchover: the fork's slot: %w", err)
		}
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET TRANSACTION READ WRITE`); err != nil {
		return out, err
	}
	var subs int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_subscription WHERE subname = $1`, subscription).Scan(&subs); err != nil {
		return out, err
	}
	if subs > 0 {
		// stopped with the promotion (dropped after it: a drop forgets the subscription's
		// position at once, undone or not); the old home drops its slot itself (it may be
		// gone)
		for _, q := range []string{
			`ALTER SUBSCRIPTION ` + subscription + ` DISABLE`,
			`ALTER SUBSCRIPTION ` + subscription + ` SET (slot_name = NONE)`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return out, fmt.Errorf("switchover: end the subscription: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `ALTER DATABASE `+database+` RESET default_transaction_read_only`); err != nil {
		return out, err
	}
	var selfPath json.RawMessage
	var selfSource sql.NullString
	_ = tx.QueryRowContext(ctx, `SELECT path_map, path_map_source FROM cluster_nodes WHERE id = $1`, p.Self).
		Scan(&selfPath, &selfSource)
	if err := load(ctx, tx, p, selfPath, selfSource.String); err != nil {
		return out, err
	}
	if err := roles(ctx, tx, p); err != nil {
		return out, err
	}
	// every machine's copy gets what is written from here: the old home's registration
	// (its own row reaches it), the term, the switchover's state
	if p.OldHome != nil {
		m := p.OldHome
		if _, err := tx.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role,
		        approved_at, approved_by, full_node, web_url)
		    VALUES ($1, $2, 'approved', NULLIF($3, ''), NULLIF($4, ''), $5, $6, now(), 'switchover', true, NULLIF($7, ''))
		    ON CONFLICT (id) DO UPDATE SET state = 'approved', node_pubkey = excluded.node_pubkey,
		        db_role = excluded.db_role, full_node = true, web_url = excluded.web_url, updated_at = now()`,
			m.ID, m.Name, m.OS, m.Arch, m.PublicKey, m.Role(), m.WebURL); err != nil {
			return out, fmt.Errorf("switchover: the old home's registration: %w", err)
		}
	}
	var cut any
	if out.CutLSN != "" && p.Reason == "emergency" {
		cut = out.CutLSN
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cluster_terms (term, home_name, home_node, reason, cut_lsn)
	    VALUES ($1, $2, $3, $4, $5::pg_lsn)`, p.Term, p.Name, p.Self, p.Reason, cut); err != nil {
		return out, fmt.Errorf("switchover: the term: %w", err)
	}
	if r := p.Record; r != nil {
		if err := record(ctx, tx, p, r); err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("switchover: the promotion: %w", err)
	}
	// stopped and without a slot it is inert: one left (the drop failed) never applies
	if subs > 0 {
		_, _ = conn.ExecContext(ctx, `DROP SUBSCRIPTION IF EXISTS `+subscription)
	}
	return out, nil
}

// loadNodes replaces the copy's machines (its own row) with the kit's, triggers off (the
// rows are the old home's, consistent as they were).
func loadNodes(ctx context.Context, conn *sql.Conn, p Promotion) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET TRANSACTION READ WRITE`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return err
	}
	if err := loadTable(ctx, tx, "cluster_nodes", p.Kit.Tables["cluster_nodes"]); err != nil {
		return err
	}
	return tx.Commit()
}

// unloadNodes leaves of the machines this machine's own row, as its copy had it (a
// promotion that failed after loadNodes).
func unloadNodes(ctx context.Context, conn *sql.Conn, self string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET TRANSACTION READ WRITE`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cluster_nodes WHERE id <> $1::uuid`, self); err != nil {
		return err
	}
	return tx.Commit()
}

// loadTable replaces table t's rows with the kit's (t one of KitTables; one this database
// lacks is left).
func loadTable(ctx context.Context, tx *sql.Tx, t string, rows []json.RawMessage) error {
	cols, err := insertable(ctx, tx, t)
	if err != nil || cols == "" {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.`+pq.QuoteIdentifier(t)); err != nil {
		return fmt.Errorf("switchover: empty %s: %w", t, err)
	}
	for i := 0; i < len(rows); i += 500 {
		batch, err := json.Marshal(rows[i:min(i+500, len(rows))])
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.`+pq.QuoteIdentifier(t)+` (`+cols+`) OVERRIDING SYSTEM VALUE
		    SELECT `+cols+` FROM json_populate_recordset(NULL::public.`+pq.QuoteIdentifier(t)+`, $1::json)`, string(batch)); err != nil {
			return fmt.Errorf("switchover: load %s: %w", t, err)
		}
	}
	return nil
}

// record writes an emergency's switch row in the promotion's tx: every machine waited for
// (as the new home finishes it, with the drain each had), after any switch the copy showed
// under way (the old home's, cut off with it) is cancelled.
func record(ctx context.Context, tx *sql.Tx, p Promotion, r *Record) error {
	if _, err := tx.ExecContext(ctx, `UPDATE cluster_switchovers SET state = 'cancelled', updated_at = now(),
	    detail = 'the home was gone: another machine took over' WHERE state NOT IN ('done', 'failed', 'cancelled')`); err != nil {
		return fmt.Errorf("switchover: the switches under way: %w", err)
	}
	machines := map[string]any{}
	rows, err := tx.QueryContext(ctx, `SELECT id::text, name, drain FROM cluster_nodes WHERE state = 'approved'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, name string
		var drain bool
		if err := rows.Scan(&id, &name, &drain); err != nil {
			_ = rows.Close()
			return err
		}
		machines[id] = map[string]any{"name": name, "state": "waiting", "prior_drain": drain}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	mj, err := json.Marshal(machines)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cluster_switchovers (id, term, kind, to_node, to_name, from_name,
	        state, machines, requested_by, frozen_at)
	    VALUES ($1, $2, 'emergency', $3, $4, $5, 'repointing', $6::jsonb, NULLIF($7, '')::uuid, now())`,
		r.ID, p.Term, p.Self, p.Name, r.FromName, string(mj), r.RequestedBy); err != nil {
		return fmt.Errorf("switchover: the switch's record: %w", err)
	}
	return nil
}

// PromotedAt reports whether this database is the home's of term already, as self's: a
// promotion that went through (asked again: what its answer said).
func PromotedAt(ctx context.Context, db *sql.DB, self string, term int64) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cluster_terms WHERE term = $1 AND home_node = $2::uuid)`,
		term, self).Scan(&ok)
	return ok, err
}

// kitMachines are the machines of the kit that follow the new home (approved, full; this
// machine aside).
func kitMachines(p Promotion) []string {
	if p.Kit == nil {
		return nil
	}
	var out []string
	for _, raw := range p.Kit.Tables["cluster_nodes"] {
		var row struct {
			ID    string `json:"id"`
			State string `json:"state"`
			Full  bool   `json:"full_node"`
		}
		if json.Unmarshal(raw, &row) != nil || row.State != "approved" || !row.Full ||
			strings.EqualFold(row.ID, p.Self) {
			continue
		}
		out = append(out, row.ID)
	}
	return out
}

// execer is a transaction or a session of the promotion.
type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// load replaces the copy's rows of the kit's tables with the kit's (triggers and foreign
// keys off for the load: the rows are the old home's, consistent as they were), moves
// the sequences on, and takes this machine out of the machines; in the promotion's tx.
func load(ctx context.Context, tx *sql.Tx, p Promotion, selfPath json.RawMessage, selfSource string) error {
	if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return err
	}
	if p.Kit != nil {
		for _, t := range KitTables {
			rows, ok := p.Kit.Tables[t]
			if !ok || t == "cluster_nodes" { // the machines: loadNodes, before their slots
				continue
			}
			if err := loadTable(ctx, tx, t, rows); err != nil {
				return err
			}
		}
	}
	// the sequences: on from the old home's, or past the copy's highest values
	seqs, err := sequenceOwners(ctx, tx)
	if err != nil {
		return err
	}
	known := map[string]Sequence{}
	if p.Kit != nil {
		for _, s := range p.Kit.Sequences {
			known[s.Name] = s
		}
	}
	for _, s := range seqs {
		var top sql.NullInt64
		if s.table != "" {
			if err := tx.QueryRowContext(ctx, `SELECT max(`+pq.QuoteIdentifier(s.column)+`)::bigint FROM `+s.table).Scan(&top); err != nil {
				return fmt.Errorf("switchover: %s: %w", s.table, err)
			}
		}
		want, called := top.Int64+p.Gap, top.Valid || p.Gap > 0
		if k, ok := known[s.name]; ok && (k.LastValue > want || (k.LastValue == want && k.IsCalled)) {
			want, called = k.LastValue, k.IsCalled
		}
		if want < 1 {
			want, called = 1, false
		}
		if _, err := tx.ExecContext(ctx, `SELECT setval($1::regclass, $2, $3)`, s.name, want, called); err != nil {
			return fmt.Errorf("switchover: %s: %w", s.name, err)
		}
	}
	// this machine is the home now, not one of its machines (its stage switches go with
	// its row: the triggers on again); its own mounts are the home's
	if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cluster_nodes WHERE id = $1`, p.Self); err != nil {
		return err
	}
	if selfSource == "admin" && len(selfPath) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE instance_identity SET path_map = $1::jsonb, updated_at = now() WHERE id = 1`,
			string(selfPath)); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE instance_identity SET path_map = NULL, updated_at = now() WHERE id = 1`); err != nil {
		return err
	}
	return nil
}

// insertable is a table's columns a row may set (not generated, not dropped), quoted
// and joined ("" for a table this database lacks).
func insertable(ctx context.Context, tx *sql.Tx, table string) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT quote_ident(a.attname) FROM pg_attribute a
	    WHERE a.attrelid = to_regclass('public.' || $1) AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
	    ORDER BY a.attnum`, table)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return "", err
		}
		cols = append(cols, c)
	}
	return strings.Join(cols, ", "), rows.Err()
}

type seqOwner struct{ name, table, column string }

// sequenceOwners are public's sequences with the column each one feeds (none: a
// sequence of its own).
func sequenceOwners(ctx context.Context, tx *sql.Tx) ([]seqOwner, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT format('%I.%I', sn.nspname, s.relname),
       CASE WHEN t.oid IS NULL THEN '' ELSE format('%I.%I', tn.nspname, t.relname) END, COALESCE(a.attname, '')
  FROM pg_class s JOIN pg_namespace sn ON sn.oid = s.relnamespace
  LEFT JOIN pg_depend d ON d.objid = s.oid AND d.classid = 'pg_class'::regclass AND d.refclassid = 'pg_class'::regclass
       AND d.deptype IN ('a', 'i')
  LEFT JOIN pg_class t ON t.oid = d.refobjid
  LEFT JOIN pg_namespace tn ON tn.oid = t.relnamespace
  LEFT JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
 WHERE s.relkind = 'S' AND sn.nspname = 'public'
 ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []seqOwner
	for rows.Next() {
		var s seqOwner
		if err := rows.Scan(&s.name, &s.table, &s.column); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// roles makes the machines' roles as the old home had them (their passwords' SCRAM
// verifiers: the machines keep their passwords) and the old home's own as a machine.
func roles(ctx context.Context, super execer, p Promotion) error {
	self := Machine{ID: p.Self}
	var list []Role
	if p.Kit != nil {
		list = p.Kit.Roles
	}
	for _, r := range list {
		if !machineRole(r.Name) || r.Name == self.Role() || r.Name == self.CopyRole() {
			continue
		}
		if err := ensureRole(ctx, super, r.Name, pq.QuoteLiteral(r.Verifier), r.Login, r.Replication, r.Groups); err != nil {
			return err
		}
	}
	if m := p.OldHome; m != nil {
		if err := ensureRole(ctx, super, m.Role(), pq.QuoteLiteral(m.RolePassword), true, false,
			[]string{"maktaba_worker"}); err != nil {
			return err
		}
		if err := ensureRole(ctx, super, m.CopyRole(), pq.QuoteLiteral(m.CopyPassword), true, true,
			[]string{"maktaba_replicas"}); err != nil {
			return err
		}
	}
	return nil
}

func ensureRole(ctx context.Context, super execer, name, password string, login, replication bool, groups []string) error {
	id := pq.QuoteIdentifier(name)
	var exists bool
	if err := super.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := super.ExecContext(ctx, `CREATE ROLE `+id); err != nil {
			return fmt.Errorf("switchover: the role %s: %w", name, err)
		}
	}
	opts := " NOLOGIN"
	if login {
		opts = " LOGIN"
	}
	if replication {
		opts += " REPLICATION"
	} else {
		opts += " NOREPLICATION"
	}
	if _, err := super.ExecContext(ctx, `ALTER ROLE `+id+opts+` INHERIT PASSWORD `+password); err != nil {
		return fmt.Errorf("switchover: the role %s: %w", name, err)
	}
	for _, g := range groups {
		var has bool
		if err := super.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, g).Scan(&has); err != nil {
			return err
		}
		if !has {
			continue
		}
		if _, err := super.ExecContext(ctx, `GRANT `+pq.QuoteIdentifier(g)+` TO `+id); err != nil {
			return fmt.Errorf("switchover: %s's group %s: %w", name, g, err)
		}
	}
	// the home's app role administers the machines' roles as it does those it made
	// itself (Postgres 16 gives a role's creator ADMIN OPTION on it): its grants, revokes
	// and removals alter them
	if name != appRole {
		if _, err := super.ExecContext(ctx, `GRANT `+id+` TO `+pq.QuoteIdentifier(appRole)+` WITH ADMIN OPTION`); err != nil {
			return fmt.Errorf("switchover: the app role administering %s: %w", name, err)
		}
	}
	return nil
}

// StopServing ends what the old home's database serves its machines (their publications
// and slots), first of its demotion: once it follows the new home it would pass the new
// home's changes on to a machine still following it, which then has them twice. Nothing of
// its own data goes (Demote, once it follows). super writes despite the freeze. Run again,
// it finds nothing more to do.
func StopServing(ctx context.Context, super *sql.DB) error {
	rows, err := super.QueryContext(ctx, `SELECT pubname FROM pg_publication WHERE pubname LIKE 'maktaba\_copy\_%'`)
	if err != nil {
		return err
	}
	var pubs []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return err
		}
		pubs = append(pubs, p)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, p := range pubs {
		if _, err := super.ExecContext(ctx, `DROP PUBLICATION IF EXISTS `+pq.QuoteIdentifier(p)); err != nil {
			return err
		}
	}
	// a slot in use is free a moment after its connection ends
	for tries := 0; ; tries++ {
		if _, err := super.ExecContext(ctx, `SELECT pg_terminate_backend(active_pid) FROM pg_replication_slots
		    WHERE slot_name LIKE 'maktaba\_copy\_%' AND active`); err != nil {
			return err
		}
		if _, err := super.ExecContext(ctx, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots
		    WHERE slot_name LIKE 'maktaba\_copy\_%' AND NOT active`); err != nil {
			return fmt.Errorf("switchover: the old home's slots: %w", err)
		}
		var left int
		if err := super.QueryRowContext(ctx, `SELECT count(*) FROM pg_replication_slots
		    WHERE slot_name LIKE 'maktaba\_copy\_%'`).Scan(&left); err != nil {
			return err
		}
		if left == 0 {
			return nil
		}
		if tries == 20 {
			return fmt.Errorf("switchover: %d of the old home's slots stay in use", left)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Demote gives up what was the old home's in its database, now a copy of the new one's
// (it holds the marker: it is the new home's as of the freeze; it serves no machine any
// more, StopServing, and its subscription is the copy's, nodecopy.Copy.Handover, which
// receives the new home's changes): the home-only rows (its secrets, its machines but its
// own row as the new home's machine, self, which the new home publishes to it, its caches)
// emptied. It stays frozen for the app's roles while its home processes still run (the
// subscription applies all the same): the copy ends the freeze when this machine starts
// as a machine (nodecopy.Copy.EnsureReader). super writes despite the freeze
// (pgbundle.Bundle.SuperURL). Run again, it finds nothing more to do.
func Demote(ctx context.Context, super *sql.DB, self string) error {
	tx, err := super.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY cluster_copy_excluded() LOOP
        IF t NOT IN ('goose_db_version', 'maktaba_schema_version', 'cluster_nodes')
           AND to_regclass('public.' || t) IS NOT NULL THEN
            EXECUTE format('DELETE FROM public.%I', t);
        END IF;
    END LOOP;
END
$$`); err != nil {
		return fmt.Errorf("switchover: empty the home's own tables: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cluster_nodes WHERE id <> $1::uuid`, self); err != nil {
		return fmt.Errorf("switchover: the home's machines: %w", err)
	}
	return tx.Commit()
}
