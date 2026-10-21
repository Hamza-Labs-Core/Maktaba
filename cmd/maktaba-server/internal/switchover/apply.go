package switchover

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

// What an old home wrote after an emergency's cut (KeptAside) is reported to the new home,
// which may apply it. Each of the old home's transactions applies whole or not at all,
// and never on its own when it may undo or collide with what happened here since the
// fork (ForkSlot keeps that). A transaction is not applied when it:
//
//   - touches the secrets or the cluster's own rows (cluster_copy_excluded(), cluster_*,
//     instance_identity, workers): ReasonClusterRows;
//   - empties a table: ReasonTruncate;
//   - touches a table without a primary key here (its rows cannot be matched), or one
//     this home does not have: ReasonNoKey;
//   - touches a row this home inserted, changed or deleted since the fork:
//     ReasonChangedHere;
//   - updates a row this home does not have: ReasonMissingHere;
//   - fails when applied (a constraint, a type, an earlier transaction not applied):
//     ReasonFails, with the error.
//
// The plan is what applying would do now; Apply applies the transactions it applies, in
// the old home's commit order, in one transaction, only while the plan is still the one
// the admin saw (its hash).

// Reasons a transaction is not applied.
const (
	ReasonClusterRows = "cluster_rows"
	ReasonTruncate    = "truncate"
	ReasonNoKey       = "no_key"
	ReasonChangedHere = "changed_here"
	ReasonMissingHere = "missing_here"
	ReasonFails       = "fails"
	// ReasonUnknown: what this home changed since the fork is not known any more (its
	// fork slot is gone: nothing applies), or not kept for one of the tables (one made
	// since the fork, its detail).
	ReasonUnknown = "unknown"
)

// PlannedTx is one of the old home's transactions in a plan.
type PlannedTx struct {
	CommitLSN   string    `json:"commit_lsn"`
	CommittedAt time.Time `json:"committed_at"`
	Tables      []string  `json:"tables"`
	Changes     int       `json:"changes"`
	Apply       bool      `json:"apply"`
	Reason      string    `json:"reason,omitempty"`
	Detail      string    `json:"detail,omitempty"`
}

// Plan is what applying a report would do now.
type Plan struct {
	Report       string      `json:"report"`
	Hash         string      `json:"hash"`
	Transactions []PlannedTx `json:"transactions"`
	Apply        int         `json:"apply"`
	Skip         int         `json:"skip"`
	// Unknown: this home's changes since the fork are not known: nothing applies.
	Unknown bool `json:"unknown,omitempty"`
}

// ErrPlanChanged: the plan is not the one the admin saw any more (this home changed
// since): nothing was applied; the new plan is returned.
var ErrPlanChanged = errors.New("switchover: what would be applied changed since it was shown")

// ErrNotReported: the report was applied or dismissed already.
var ErrNotReported = errors.New("switchover: that report was applied or dismissed already")

type keptRow struct {
	term  int64
	lost  bool
	state string
	aside Aside
}

func readReport(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, lock bool) (keptRow, error) {
	var r keptRow
	var raw []byte
	query := `SELECT term, lost, state, report FROM cluster_kept_aside WHERE id = $1`
	if lock {
		query += ` FOR UPDATE`
	}
	if err := q.QueryRowContext(ctx, query, id).Scan(&r.term, &r.lost, &r.state, &raw); err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r.aside); err != nil {
		return r, fmt.Errorf("switchover: the report: %w", err)
	}
	return r, nil
}

// PlanReport is what applying report id would do now (nothing is applied).
func PlanReport(ctx context.Context, super *sql.DB, id string) (Plan, error) {
	r, err := readReport(ctx, super, id, false)
	if err != nil {
		return Plan{}, err
	}
	if r.state != "reported" {
		return Plan{}, ErrNotReported
	}
	tx, err := super.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return run(ctx, super, tx, id, r)
}

// ApplyReport applies report id's transactions that apply, in one transaction, while its
// plan is still hash (else ErrPlanChanged and the plan now); it records what was applied
// and by whom (a user id, or "").
func ApplyReport(ctx context.Context, super *sql.DB, id, hash, by string) (Plan, error) {
	tx, err := super.BeginTx(ctx, nil)
	if err != nil {
		return Plan{}, err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := readReport(ctx, tx, id, true)
	if err != nil {
		return Plan{}, err
	}
	if r.state != "reported" {
		return Plan{}, ErrNotReported
	}
	p, err := run(ctx, super, tx, id, r)
	if err != nil {
		return Plan{}, err
	}
	if p.Hash != hash {
		return p, ErrPlanChanged
	}
	b, err := json.Marshal(p)
	if err != nil {
		return Plan{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cluster_kept_aside SET state = 'applied', applied = $2::jsonb,
	    applied_at = now(), applied_by = NULLIF($3, '')::uuid WHERE id = $1`, id, string(b), by); err != nil {
		return Plan{}, err
	}
	return p, tx.Commit()
}

// DismissReport sets a report aside without applying anything of it (it stays, to
// download).
func DismissReport(ctx context.Context, super *sql.DB, id, by string) error {
	res, err := super.ExecContext(ctx, `UPDATE cluster_kept_aside SET state = 'dismissed', applied_at = now(),
	    applied_by = NULLIF($2, '')::uuid WHERE id = $1 AND state = 'reported'`, id, by)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotReported
	}
	return nil
}

// table is a table of this home as an applied change needs it.
type table struct {
	ident string            // schema.name, quoted
	keys  []string          // its primary key's columns, in order (none: no key)
	cols  map[string]string // its columns a row may set, with their types
}

type schema struct {
	tables map[string]*table // by "schema.name"
	never  map[string]bool   // the tables never applied on their own
}

func loadSchema(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (*schema, error) {
	s := &schema{tables: map[string]*table{}, never: map[string]bool{}}
	rows, err := q.QueryContext(ctx, `
SELECT n.nspname || '.' || c.relname, format('%I.%I', n.nspname, c.relname), a.attname, format_type(a.atttypid, NULL),
       COALESCE(array_position(i.indkey::int2[], a.attnum), -1)
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
  LEFT JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary
 WHERE c.relkind IN ('r', 'p') AND n.nspname = 'public'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]map[int]string{}
	for rows.Next() {
		var name, ident, col, typ string
		var pos int
		if err := rows.Scan(&name, &ident, &col, &typ, &pos); err != nil {
			return nil, err
		}
		t := s.tables[name]
		if t == nil {
			t = &table{ident: ident, cols: map[string]string{}}
			s.tables[name] = t
		}
		t.cols[col] = typ
		// indkey casts to an array from 0: the key's first column is at 0
		if pos >= 0 {
			if keys[name] == nil {
				keys[name] = map[int]string{}
			}
			keys[name][pos] = col
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for name, ks := range keys {
		pos := make([]int, 0, len(ks))
		for p := range ks {
			pos = append(pos, p)
		}
		sort.Ints(pos)
		for _, p := range pos {
			s.tables[name].keys = append(s.tables[name].keys, ks[p])
		}
	}
	var excluded pq.StringArray
	if err := s.queryExcluded(ctx, q, &excluded); err != nil {
		return nil, err
	}
	for _, t := range excluded {
		s.never["public."+t] = true
	}
	for _, t := range []string{"instance_identity", "workers", "goose_db_version", "maktaba_schema_version"} {
		s.never["public."+t] = true
	}
	return s, nil
}

func (s *schema) queryExcluded(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, out *pq.StringArray) error {
	rows, err := q.QueryContext(ctx, `SELECT cluster_copy_excluded()`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(out); err != nil {
			return err
		}
	}
	return rows.Err()
}

// neverApplied: a change of name is never applied on its own (the secrets, the cluster's
// rows).
func (s *schema) neverApplied(name string) bool {
	return s.never[name] || strings.HasPrefix(name, "public.cluster_")
}

// key is a change's row key in t ("", false: the row does not carry it).
func (t *table) key(row map[string]*string) (string, bool) {
	if len(t.keys) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, k := range t.keys {
		v, ok := row[k]
		if !ok {
			return "", false
		}
		if v == nil {
			b.WriteString("\x01")
		} else {
			b.WriteString(*v)
		}
		b.WriteString("\x00")
	}
	return b.String(), true
}

// touched are the rows this home changed since the fork of term, by table and key
// (false: not known, its fork slot is gone), and the tables its fork keeps them of (the
// ones a machine's copy holds, as they were at the fork: one made since is not).
func touched(ctx context.Context, super *sql.DB, s *schema, term int64) (map[string]map[string]bool, bool,
	map[string]bool, error) {
	fork := ForkSlot(term)
	var exists bool
	if err := super.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1
	    AND NOT COALESCE(wal_status = 'lost', false))`, fork).Scan(&exists); err != nil {
		return nil, false, nil, err
	}
	if !exists {
		return nil, false, nil, nil
	}
	tracked := map[string]bool{}
	rows, err := super.QueryContext(ctx, `SELECT schemaname || '.' || tablename FROM pg_publication_tables
	    WHERE pubname = $1`, fork)
	if err != nil {
		return nil, false, nil, err
	}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			_ = rows.Close()
			return nil, false, nil, err
		}
		tracked[t] = true
	}
	if err := rows.Close(); err != nil {
		return nil, false, nil, err
	}
	changed, err := KeptAside(ctx, super, fork, fork, "")
	if err != nil {
		return nil, false, nil, err
	}
	out := map[string]map[string]bool{}
	for _, tx := range changed.Transactions {
		for _, c := range tx.Changes {
			t := s.tables[c.Table]
			if t == nil {
				continue
			}
			if out[c.Table] == nil {
				out[c.Table] = map[string]bool{}
			}
			if c.Kind == "truncate" {
				out[c.Table]["\x02all"] = true
				continue
			}
			if k, ok := t.key(c.Row); ok {
				out[c.Table][k] = true
			}
		}
	}
	return out, true, tracked, nil
}

// run plans (and, in tx, does) every transaction of the report: each one in a savepoint,
// kept only when it applies. The caller commits tx (Apply) or not (a plan).
func run(ctx context.Context, super *sql.DB, tx *sql.Tx, id string, r keptRow) (Plan, error) {
	p := Plan{Report: id}
	s, err := loadSchema(ctx, tx)
	if err != nil {
		return p, err
	}
	seen, known, tracked, err := touched(ctx, super, s, r.term)
	if err != nil {
		return p, err
	}
	p.Unknown = !known
	for i, kt := range r.aside.Transactions {
		pt := PlannedTx{CommitLSN: kt.CommitLSN, CommittedAt: kt.CommittedAt, Changes: len(kt.Changes)}
		tables := map[string]bool{}
		for _, c := range kt.Changes {
			tables[c.Table] = true
		}
		for t := range tables {
			pt.Tables = append(pt.Tables, t)
		}
		sort.Strings(pt.Tables)
		pt.Reason, pt.Detail = check(s, seen, known, tracked, kt)
		if pt.Reason == "" {
			if reason, detail := apply(ctx, tx, s, kt, i); reason != "" {
				pt.Reason, pt.Detail = reason, detail
			} else {
				pt.Apply = true
			}
		}
		if pt.Apply {
			p.Apply++
		} else {
			p.Skip++
		}
		p.Transactions = append(p.Transactions, pt)
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s %v\n", id, p.Unknown)
	for _, t := range p.Transactions {
		fmt.Fprintf(h, "%s %v %s\n", t.CommitLSN, t.Apply, t.Reason)
	}
	p.Hash = hex.EncodeToString(h.Sum(nil))
	return p, nil
}

// check is why a transaction is not applied before it is tried ("": it may be).
func check(s *schema, seen map[string]map[string]bool, known bool, tracked map[string]bool,
	kt Transaction) (reason, detail string) {
	if !known {
		return ReasonUnknown, ""
	}
	for _, c := range kt.Changes {
		if s.neverApplied(c.Table) {
			return ReasonClusterRows, c.Table
		}
	}
	for _, c := range kt.Changes {
		if c.Kind == "truncate" {
			return ReasonTruncate, c.Table
		}
		t := s.tables[c.Table]
		if t == nil || len(t.keys) == 0 {
			return ReasonNoKey, c.Table
		}
		k, ok := t.key(c.Row)
		if !ok {
			return ReasonNoKey, c.Table
		}
		if !tracked[c.Table] {
			// what this home changed in it since the fork is not kept
			return ReasonUnknown, c.Table
		}
		if seen[c.Table][k] || seen[c.Table]["\x02all"] {
			return ReasonChangedHere, c.Table
		}
	}
	return "", ""
}

// apply does one transaction's changes in a savepoint of tx, rolled back when one of them
// does not apply ("": all did).
func apply(ctx context.Context, tx *sql.Tx, s *schema, kt Transaction, i int) (reason, detail string) {
	sp := fmt.Sprintf("kept_%d", i)
	if _, err := tx.ExecContext(ctx, `SAVEPOINT `+sp); err != nil {
		return ReasonFails, err.Error()
	}
	undo := func() { _, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT `+sp) }
	for _, c := range kt.Changes {
		t := s.tables[c.Table]
		n, err := applyChange(ctx, tx, t, c)
		switch {
		case err != nil:
			undo()
			return ReasonFails, err.Error()
		case n == 0 && c.Kind == "update":
			undo()
			return ReasonMissingHere, c.Table
		}
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT `+sp); err != nil {
		undo()
		return ReasonFails, err.Error()
	}
	return "", ""
}

// applyChange does one change; how many rows it touched.
func applyChange(ctx context.Context, tx *sql.Tx, t *table, c Change) (int64, error) {
	rec := map[string]json.RawMessage{}
	var cols []string
	for col, v := range c.Row {
		typ, ok := t.cols[col]
		if !ok {
			continue // a column this home does not have
		}
		cols = append(cols, col)
		switch {
		case v == nil:
			rec[col] = json.RawMessage("null")
		case typ == "json" || typ == "jsonb":
			if !json.Valid([]byte(*v)) {
				return 0, fmt.Errorf("%s.%s is not JSON", t.ident, col)
			}
			rec[col] = json.RawMessage(*v)
		default:
			b, _ := json.Marshal(*v)
			rec[col] = b
		}
	}
	sort.Strings(cols)
	raw, err := json.Marshal(rec)
	if err != nil {
		return 0, err
	}
	quoted := func(list []string, prefix string) string {
		out := make([]string, len(list))
		for i, c := range list {
			out[i] = prefix + pq.QuoteIdentifier(c)
		}
		return strings.Join(out, ", ")
	}
	match := make([]string, len(t.keys))
	for i, k := range t.keys {
		match[i] = "t." + pq.QuoteIdentifier(k) + " = r." + pq.QuoteIdentifier(k)
	}
	var q string
	switch c.Kind {
	case "insert":
		q = `INSERT INTO ` + t.ident + ` (` + quoted(cols, "") + `) OVERRIDING SYSTEM VALUE SELECT ` + quoted(cols, "") +
			` FROM json_populate_record(NULL::` + t.ident + `, $1::json)`
	case "update":
		var set []string
		for _, col := range cols {
			isKey := false
			for _, k := range t.keys {
				isKey = isKey || k == col
			}
			if !isKey {
				set = append(set, pq.QuoteIdentifier(col)+" = r."+pq.QuoteIdentifier(col))
			}
		}
		if len(set) == 0 {
			return 1, nil
		}
		q = `UPDATE ` + t.ident + ` AS t SET ` + strings.Join(set, ", ") + ` FROM json_populate_record(NULL::` +
			t.ident + `, $1::json) r WHERE ` + strings.Join(match, " AND ")
	case "delete":
		q = `DELETE FROM ` + t.ident + ` AS t USING json_populate_record(NULL::` + t.ident + `, $1::json) r WHERE ` +
			strings.Join(match, " AND ")
	default:
		return 0, fmt.Errorf("a change of kind %q", c.Kind)
	}
	res, err := tx.ExecContext(ctx, q, string(raw))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DropFork drops the fork's slot and publication of term (its reports applied or set
// aside: what this home changed since the fork is not needed any more).
func DropFork(ctx context.Context, super *sql.DB, term int64) error {
	fork := ForkSlot(term)
	if _, err := super.ExecContext(ctx, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots
	    WHERE slot_name = $1 AND NOT active`, fork); err != nil {
		return err
	}
	_, err := super.ExecContext(ctx, `DROP PUBLICATION IF EXISTS `+pq.QuoteIdentifier(fork))
	return err
}
