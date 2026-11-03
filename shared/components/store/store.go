// Package store reads and writes the components' desired and actual state (Epic 51,
// story 51.02; slot 0174): the one place that knows the SQL, so no runner writes any.
//
//   - Desired state (components, component_overrides) is written by the API (and the
//     supervisor's seed, 51.03) through SetDesired / SetOverride, each with its audit_log
//     row in the same transaction.
//   - Actual state (component_status) is written by each part of each machine through
//     ReportStatus, for its own (machine, part) only. On Postgres a joined machine goes
//     through the guarded functions component_status_report / component_desired_for;
//     the home (machine "home") and SQLite write the tables.
//   - Every change of desired state moves component_meta.revision and sends NOTIFY
//     components_changed. A runner watches with a Watcher: one revision query per 30 s
//     when nothing changes, a reload when the revision moves or a notification comes.
//
// It runs on database/sql with either driver the processes use (lib/pq, pgx stdlib) and
// on SQLite (modernc).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Home is the machine name of the home's own status rows; a joined machine's is its
// cluster_nodes.id.
const Home = "home"

// Channel is the NOTIFY channel a change of desired state is sent on.
const Channel = "components_changed"

// MaxDetailBytes caps a status row's detail; a bigger one is stored as {"truncated":true}.
const MaxDetailBytes = 4096

// AuditCategory is the audit_log category of every desired-state change.
const AuditCategory = "components"

// Hold is a stop that lasts until an admin starts the component again: there is no end
// time (the owner, 2026-11-03).
type Hold struct {
	By string    `json:"by"`
	At time.Time `json:"at"`
}

// Desired is what should be true of one component: on the home, or merged with a
// machine's override. Generation moves with every change (for a machine, it is the home
// row's plus the override's).
type Desired struct {
	ID         string `json:"id"`
	Want       string `json:"want"` // absent | installed | enabled
	Hold       *Hold  `json:"hold,omitempty"`
	Generation int64  `json:"generation"`
	// Licence is what the home's licence allows, as the API published it (slot 0181;
	// story 51.19). nil: nothing published, the component runs as it does today. Only
	// Desired fills it (the runners' read).
	Licence *Licence `json:"licence,omitempty"`
}

// Licence is a desired row's licensed and licensed_until: whether the home's licence
// grants the component's flag, and until when (the licence's expires_at + SkewGrace; nil:
// it does not expire, or it is not granted).
type Licence struct {
	Licensed bool       `json:"licensed"`
	Until    *time.Time `json:"until,omitempty"`
}

// LicensedAt is the Licensed condition at now: licensed AND (no until OR now < until).
// A row with nothing published is licensed.
func (d Desired) LicensedAt(now time.Time) bool {
	if d.Licence == nil {
		return true
	}
	return d.Licence.Licensed && (d.Licence.Until == nil || now.Before(*d.Licence.Until))
}

// Override is one machine's exception to the home's row (51.12). An empty Want follows
// the home; a nil Hold has no hold of its own.
type Override struct {
	Node       string `json:"node"`
	ID         string `json:"id"`
	Want       string `json:"want,omitempty"`
	Hold       *Hold  `json:"hold,omitempty"`
	Generation int64  `json:"generation"`
}

// Condition is one of a status row's conditions (README §4.3). Status is "true",
// "false" or "unknown"; Reason one of the catalog's reason codes (or empty).
type Condition struct {
	Type       string         `json:"type"`
	Status     string         `json:"status"`
	Reason     string         `json:"reason,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	ChangedAt  time.Time      `json:"changed_at"`
	Generation int64          `json:"generation"`
}

// Status is what one part of one machine reports of one component. Since (when the
// phase last changed) and UpdatedAt are set by the database.
type Status struct {
	Machine            string         `json:"machine"`
	ID                 string         `json:"id"`
	Part               string         `json:"part"`
	Phase              string         `json:"phase"`
	Conditions         []Condition    `json:"conditions"`
	ObservedGeneration int64          `json:"observed_generation"`
	Version            string         `json:"version,omitempty"`
	Detail             map[string]any `json:"detail,omitempty"`
	Since              time.Time      `json:"since"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// Reason is the first false condition's reason, or "".
func (s Status) Reason() string {
	for _, c := range s.Conditions {
		if c.Status == "false" && c.Reason != "" {
			return c.Reason
		}
	}
	return ""
}

// Audit is who made a desired-state change. Action is "component.<verb>"
// (component.enable, component.disable, component.hold, component.seed, ...).
type Audit struct {
	By          string // user name, or "seed": the row's updated_by
	ActorUserID string // users.id when a signed-in user did it
	ActorSource string // web | cli | remote | admin | seed | system
	Action      string
	// BatchID ties the rows of one batch (an apply, a change with its conflicts, a
	// bundle) together in their payloads; "" for a single change.
	BatchID string
	// Node is the machine of an override (51.12): its payload carries node_id.
	Node string
}

// ErrNotFound is a component with no desired row.
var ErrNotFound = errors.New("components: no desired row")

// Store reads and writes the component tables of one database.
type Store struct {
	DB *sql.DB
	// Catalog validates phases and reasons (nil: components.Default()).
	Catalog *components.Catalog
	// Version is the release that reports status (component_status.version) when a row
	// leaves it empty.
	Version string
}

// New is a store on db.
func New(db *sql.DB) *Store { return &Store{DB: db} }

func (s *Store) catalog() *components.Catalog {
	if s.Catalog != nil {
		return s.Catalog
	}
	return components.Default()
}

// IsSQLite reports whether db is the SQLite backend.
func IsSQLite(db *sql.DB) bool {
	return db != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
}

func (s *Store) sqlite() bool { return IsSQLite(s.DB) }

type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Revision is component_meta.revision: it moves on every change of desired state. One
// cheap query: what a runner asks every 30 s.
func (s *Store) Revision(ctx context.Context) (int64, error) {
	var rev int64
	err := s.DB.QueryRowContext(ctx, `SELECT revision FROM component_meta`).Scan(&rev)
	return rev, err
}

// Desired is the desired state machine sees, by id, and the revision it was read at
// (read first: a change landing meanwhile moves the revision again, so the next look
// reloads). For Home the home's rows; for a joined machine (its cluster_nodes.id) the
// home's rows with its overrides applied.
func (s *Store) Desired(ctx context.Context, machine string) (map[string]Desired, int64, error) {
	rev, err := s.Revision(ctx)
	if err != nil {
		return nil, 0, err
	}
	var q string
	var args []any
	switch {
	case machine == Home || machine == "":
		q = `SELECT id, want, hold_by, hold_at, generation FROM components ORDER BY id`
	case !s.sqlite():
		q = `SELECT id, want, hold_by, hold_at, generation FROM component_desired_for($1::uuid)`
		args = []any{machine}
	default:
		q = `SELECT COALESCE(c.id, o.id), COALESCE(o.want, c.want),
		        CASE WHEN o.hold_at IS NOT NULL THEN o.hold_by ELSE c.hold_by END,
		        CASE WHEN o.hold_at IS NOT NULL THEN o.hold_at ELSE c.hold_at END,
		        COALESCE(c.generation, 0) + COALESCE(o.generation, 0)
		   FROM components c
		   LEFT JOIN component_overrides o ON o.id = c.id AND o.node_id = $1
		  WHERE COALESCE(o.want, c.want) IS NOT NULL
		 UNION ALL
		 SELECT o.id, o.want, o.hold_by, o.hold_at, o.generation
		   FROM component_overrides o
		  WHERE o.node_id = $1 AND o.want IS NOT NULL
		    AND NOT EXISTS (SELECT 1 FROM components c WHERE c.id = o.id)
		  ORDER BY 1`
		args = []any{machine}
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Desired{}
	for rows.Next() {
		var d Desired
		var holdBy sql.NullString
		var holdAt nullTime
		if err := rows.Scan(&d.ID, &d.Want, &holdBy, &holdAt, &d.Generation); err != nil {
			return nil, 0, err
		}
		if holdAt.Valid {
			d.Hold = &Hold{By: holdBy.String, At: holdAt.T}
		}
		out[d.ID] = d
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if machine == Home || machine == "" {
		if err := s.readLicences(ctx, out); err != nil {
			return nil, 0, err
		}
	}
	return out, rev, nil
}

// readLicences sets each home row's Licence from licensed and licensed_until. A database
// before slot 0181 has neither column: nothing is published there. A joined machine reads
// none (component_desired_for returns no licence): gating is per home, and no gated
// component runs on a machine (cloud.relay is home-only).
func (s *Store) readLicences(ctx context.Context, out map[string]Desired) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, licensed, licensed_until FROM components WHERE licensed IS NOT NULL`)
	if err != nil {
		if noLicenceColumns(err) {
			return nil
		}
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var licensed bool
		var until nullTime
		if err := rows.Scan(&id, &licensed, &until); err != nil {
			return err
		}
		d, ok := out[id]
		if !ok {
			continue
		}
		l := &Licence{Licensed: licensed}
		if until.Valid {
			u := until.T
			l.Until = &u
		}
		d.Licence = l
		out[id] = d
	}
	return rows.Err()
}

func noLicenceColumns(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "licensed") && (strings.Contains(m, "does not exist") || strings.Contains(m, "no such column"))
}

// SetLicences publishes what the home's licence allows on the desired rows of the
// components in ls (story 51.19): only a row whose licensed or licensed_until differs is
// written, so a publish that changes nothing sends no notification. A component with no
// desired row is left alone (no row runs as today; the seed writes every row). It
// answers how many rows it changed. Only the API calls it.
func (s *Store) SetLicences(ctx context.Context, ls map[string]Licence) (int, error) {
	ids := make([]string, 0, len(ls))
	for id := range ls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	q := `UPDATE components SET licensed = $2, licensed_until = $3
		WHERE id = $1 AND (licensed IS DISTINCT FROM $2 OR licensed_until IS DISTINCT FROM $3)`
	if s.sqlite() {
		q = `UPDATE components SET licensed = $2, licensed_until = $3
		WHERE id = $1 AND (licensed IS NOT $2 OR licensed_until IS NOT $3)`
	}
	n := 0
	for _, id := range ids {
		l := ls[id]
		var until any
		if l.Until != nil {
			until = ts(s.DB, *l.Until)
		}
		var licensed any = l.Licensed
		if s.sqlite() {
			licensed = 0
			if l.Licensed {
				licensed = 1
			}
		}
		res, err := s.DB.ExecContext(ctx, q, id, licensed, until)
		if err != nil {
			return n, err
		}
		if k, err := res.RowsAffected(); err == nil {
			n += int(k)
		}
	}
	return n, nil
}

// DesiredOf is the home's row for id (ErrNotFound when there is none).
func (s *Store) DesiredOf(ctx context.Context, id string) (Desired, error) {
	return s.desiredOf(ctx, s.DB, id)
}

func (s *Store) desiredOf(ctx context.Context, q queryer, id string) (Desired, error) {
	var d Desired
	var holdBy sql.NullString
	var holdAt nullTime
	err := q.QueryRowContext(ctx, `SELECT id, want, hold_by, hold_at, generation FROM components WHERE id = $1`, id).
		Scan(&d.ID, &d.Want, &holdBy, &holdAt, &d.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return Desired{}, ErrNotFound
	}
	if err != nil {
		return Desired{}, err
	}
	if holdAt.Valid {
		d.Hold = &Hold{By: holdBy.String, At: holdAt.T}
	}
	return d, nil
}

func validWant(w string) bool {
	return w == components.WantAbsent || w == components.WantInstalled || w == components.WantEnabled
}

func validAudit(a Audit) error {
	if strings.TrimSpace(a.By) == "" {
		return errors.New("components: an audit needs By (who made the change)")
	}
	if !strings.HasPrefix(a.Action, "component.") {
		return fmt.Errorf("components: audit action %q must be component.<verb>", a.Action)
	}
	return nil
}

// SetDesired writes the home's desired row for d.ID (want and hold; the generation is
// the database's) and its audit_log row, in one transaction, and answers the row as it
// now is. The id need not be in this release's catalog (a newer release may have written
// it); the caller decides what may be set.
func (s *Store) SetDesired(ctx context.Context, d Desired, audit Audit) (Desired, error) {
	if !validWant(d.Want) {
		return Desired{}, fmt.Errorf("components: bad want %q", d.Want)
	}
	if d.ID == "" {
		return Desired{}, errors.New("components: a desired row needs an id")
	}
	if err := validAudit(audit); err != nil {
		return Desired{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Desired{}, err
	}
	defer func() { _ = tx.Rollback() }()
	after, err := s.setDesired(ctx, tx, d, audit)
	if err != nil {
		return Desired{}, err
	}
	return after, tx.Commit()
}

// SetDesiredTx is SetDesired inside the caller's transaction tx (on s.DB): a change
// that also writes another table (the TV settings' hdhr_device.enabled) commits or
// rolls back with it. The caller commits.
func (s *Store) SetDesiredTx(ctx context.Context, tx *sql.Tx, d Desired, audit Audit) (Desired, error) {
	if !validWant(d.Want) {
		return Desired{}, fmt.Errorf("components: bad want %q", d.Want)
	}
	if d.ID == "" {
		return Desired{}, errors.New("components: a desired row needs an id")
	}
	if err := validAudit(audit); err != nil {
		return Desired{}, err
	}
	return s.setDesired(ctx, tx, d, audit)
}

func (s *Store) setDesired(ctx context.Context, tx *sql.Tx, d Desired, audit Audit) (Desired, error) {
	var before any
	if b, err := s.desiredOf(ctx, tx, d.ID); err == nil {
		before = b
	} else if !errors.Is(err, ErrNotFound) {
		return Desired{}, err
	}
	holdBy, holdAt := holdArgs(s.DB, d.Hold)
	if _, err := tx.ExecContext(ctx, `INSERT INTO components (id, want, hold_by, hold_at, updated_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET want = excluded.want, hold_by = excluded.hold_by,
			hold_at = excluded.hold_at, updated_by = excluded.updated_by`,
		d.ID, d.Want, holdBy, holdAt, audit.By); err != nil {
		return Desired{}, err
	}
	after, err := s.desiredOf(ctx, tx, d.ID)
	if err != nil {
		return Desired{}, err
	}
	if err := s.audit(ctx, tx, audit, d.ID, before, after); err != nil {
		return Desired{}, err
	}
	return after, nil
}

// SetOverride writes machine node's exception for o.ID (an empty Want and a nil Hold
// follow the home: the row stays, so the merged generation only goes up) and its audit
// row, in one transaction.
func (s *Store) SetOverride(ctx context.Context, o Override, audit Audit) error {
	if err := validOverride(o, audit); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.setOverride(ctx, tx, o, audit); err != nil {
		return err
	}
	return tx.Commit()
}

// SetOverrideTx is SetOverride inside the caller's transaction tx (on s.DB): an override
// that also writes the machine's stage switches (51.12) commits or rolls back with them.
// It answers the row as it now is. The caller commits.
func (s *Store) SetOverrideTx(ctx context.Context, tx *sql.Tx, o Override, audit Audit) (Override, error) {
	if err := validOverride(o, audit); err != nil {
		return Override{}, err
	}
	return s.setOverride(ctx, tx, o, audit)
}

func validOverride(o Override, audit Audit) error {
	if o.Want != "" && !validWant(o.Want) {
		return fmt.Errorf("components: bad want %q", o.Want)
	}
	if o.Node == "" || o.ID == "" {
		return errors.New("components: an override needs a node and an id")
	}
	return validAudit(audit)
}

func (s *Store) setOverride(ctx context.Context, tx *sql.Tx, o Override, audit Audit) (Override, error) {
	before, err := s.overrideOf(ctx, tx, o.Node, o.ID)
	if err != nil {
		return Override{}, err
	}
	holdBy, holdAt := holdArgs(s.DB, o.Hold)
	var want any
	if o.Want != "" {
		want = o.Want
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO component_overrides (node_id, id, want, hold_by, hold_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (node_id, id) DO UPDATE SET want = excluded.want, hold_by = excluded.hold_by,
			hold_at = excluded.hold_at, updated_by = excluded.updated_by`,
		o.Node, o.ID, want, holdBy, holdAt, audit.By); err != nil {
		return Override{}, err
	}
	after, err := s.overrideOf(ctx, tx, o.Node, o.ID)
	if err != nil {
		return Override{}, err
	}
	if err := s.audit(ctx, tx, audit, o.ID, before, after); err != nil {
		return Override{}, err
	}
	out, _ := after.(Override)
	return out, nil
}

func (s *Store) overrideOf(ctx context.Context, q queryer, node, id string) (any, error) {
	var o Override
	var want, holdBy sql.NullString
	var holdAt nullTime
	err := q.QueryRowContext(ctx, `SELECT node_id, id, want, hold_by, hold_at, generation
		FROM component_overrides WHERE node_id = $1 AND id = $2`, node, id).
		Scan(&o.Node, &o.ID, &want, &holdBy, &holdAt, &o.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.Want = want.String
	if holdAt.Valid {
		o.Hold = &Hold{By: holdBy.String, At: holdAt.T}
	}
	return o, nil
}

func holdArgs(db *sql.DB, h *Hold) (any, any) {
	if h == nil {
		return nil, nil
	}
	at := h.At
	if at.IsZero() {
		at = time.Now()
	}
	return h.By, ts(db, at)
}

func (s *Store) audit(ctx context.Context, q queryer, a Audit, id string, before, after any) error {
	p := map[string]any{"before": before, "after": after, "by": a.By}
	if a.BatchID != "" {
		p["batch_id"] = a.BatchID
	}
	if a.Node != "" {
		p["node_id"] = a.Node
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var actor any
	if looksLikeUUID(a.ActorUserID) {
		actor = a.ActorUserID
	}
	source := a.ActorSource
	if source == "" {
		source = "admin"
	}
	_, err = q.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id,
		target_kind, actor_source, payload) VALUES ($1, $2, $3, $4, 'component', $5, $6)`,
		AuditCategory, a.Action, actor, id, source, string(payload))
	return err
}

// ReportStatus writes what part of machine reports: one row per component it runs.
// Rows of that machine and part not in rows are removed (the part no longer runs them).
// Every phase and reason must be the catalog's; a detail over MaxDetailBytes is stored as
// {"truncated":true}. Machine and Part of rows are ignored (the arguments decide them).
func (s *Store) ReportStatus(ctx context.Context, machine, part string, rows []Status) error {
	if machine == "" || part == "" {
		return errors.New("components: a status report needs a machine and a part")
	}
	cat := s.catalog()
	type wire struct {
		ID                 string          `json:"id"`
		Phase              string          `json:"phase"`
		Conditions         []Condition     `json:"conditions"`
		ObservedGeneration int64           `json:"observed_generation"`
		Version            string          `json:"version,omitempty"`
		Detail             json.RawMessage `json:"detail"`
	}
	out := make([]wire, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		if r.ID == "" {
			return errors.New("components: a status row needs an id")
		}
		if seen[r.ID] {
			return fmt.Errorf("components: %s reported twice by %s", r.ID, part)
		}
		seen[r.ID] = true
		if !cat.IsPhase(r.Phase) {
			return fmt.Errorf("components: %s: unknown phase %q", r.ID, r.Phase)
		}
		for _, c := range r.Conditions {
			if c.Reason != "" && !cat.IsReason(c.Reason) {
				return fmt.Errorf("components: %s: unknown reason %q (add it to the catalog's reasons, with its words)", r.ID, c.Reason)
			}
			if c.Status != "true" && c.Status != "false" && c.Status != "unknown" {
				return fmt.Errorf("components: %s: condition %s has status %q (true, false or unknown)", r.ID, c.Type, c.Status)
			}
			if c.Type == "" {
				return fmt.Errorf("components: %s: a condition needs a type", r.ID)
			}
		}
		conds := r.Conditions
		if conds == nil {
			conds = []Condition{}
		}
		detail := []byte("{}")
		if len(r.Detail) > 0 {
			b, err := json.Marshal(r.Detail)
			if err != nil {
				return fmt.Errorf("components: %s: detail: %w", r.ID, err)
			}
			detail = b
		}
		if len(detail) > MaxDetailBytes {
			detail = []byte(`{"truncated":true}`)
		}
		version := r.Version
		if version == "" {
			version = s.Version
		}
		out = append(out, wire{ID: r.ID, Phase: r.Phase, Conditions: conds,
			ObservedGeneration: r.ObservedGeneration, Version: version, Detail: detail})
	}
	if machine != Home && !s.sqlite() {
		b, err := json.Marshal(out)
		if err != nil {
			return err
		}
		_, err = s.DB.ExecContext(ctx, `SELECT component_status_report($1::uuid, $2, $3::jsonb)`, machine, part, string(b))
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	upsert := `INSERT INTO component_status AS s
		(machine, id, part, phase, conditions, observed_generation, version, detail)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8::jsonb)
		ON CONFLICT (machine, id, part) DO UPDATE SET phase = excluded.phase,
			conditions = excluded.conditions, observed_generation = excluded.observed_generation,
			version = excluded.version, detail = excluded.detail,
			since = CASE WHEN s.phase IS DISTINCT FROM excluded.phase THEN now() ELSE s.since END,
			updated_at = now()`
	prune := `DELETE FROM component_status WHERE machine = $1 AND part = $2
		AND id NOT IN (SELECT jsonb_array_elements_text($3::jsonb))`
	if s.sqlite() {
		upsert = `INSERT INTO component_status AS s
		(machine, id, part, phase, conditions, observed_generation, version, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (machine, id, part) DO UPDATE SET phase = excluded.phase,
			conditions = excluded.conditions, observed_generation = excluded.observed_generation,
			version = excluded.version, detail = excluded.detail,
			since = CASE WHEN s.phase IS NOT excluded.phase THEN strftime('%Y-%m-%dT%H:%M:%fZ', 'now') ELSE s.since END,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`
		prune = `DELETE FROM component_status WHERE machine = $1 AND part = $2
		AND id NOT IN (SELECT value FROM json_each($3))`
	}
	ids := make([]string, 0, len(out))
	for _, w := range out {
		conds, err := json.Marshal(w.Conditions)
		if err != nil {
			return err
		}
		var version any
		if w.Version != "" {
			version = w.Version
		}
		if _, err := tx.ExecContext(ctx, upsert, machine, w.ID, part, w.Phase, string(conds),
			w.ObservedGeneration, version, string(w.Detail)); err != nil {
			return err
		}
		ids = append(ids, w.ID)
	}
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, prune, machine, part, string(idsJSON)); err != nil {
		return err
	}
	return tx.Commit()
}

const statusCols = `machine, id, part, phase, conditions, observed_generation, version, detail, since, updated_at`

// StatusOf is every status row of component id, every machine and part, by machine
// then part.
func (s *Store) StatusOf(ctx context.Context, id string) ([]Status, error) {
	return s.statuses(ctx, `SELECT `+statusCols+` FROM component_status WHERE id = $1 ORDER BY machine, part`, id)
}

// Statuses is every status row, by component, machine and part.
func (s *Store) Statuses(ctx context.Context) ([]Status, error) {
	return s.statuses(ctx, `SELECT `+statusCols+` FROM component_status ORDER BY id, machine, part`)
}

// StatusesOfMachine is one machine's rows ("home" or a node id), by id and part: what
// that machine's /metrics exports (story 51.14).
func (s *Store) StatusesOfMachine(ctx context.Context, machine string) ([]Status, error) {
	return s.statuses(ctx, `SELECT `+statusCols+` FROM component_status WHERE machine = $1 ORDER BY id, part`, machine)
}

func (s *Store) statuses(ctx context.Context, q string, args ...any) ([]Status, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Status
	for rows.Next() {
		var st Status
		var conds, detail []byte
		var version sql.NullString
		var since, updated nullTime
		if err := rows.Scan(&st.Machine, &st.ID, &st.Part, &st.Phase, &conds, &st.ObservedGeneration,
			&version, &detail, &since, &updated); err != nil {
			return nil, err
		}
		st.Version, st.Since, st.UpdatedAt = version.String, since.T, updated.T
		if err := json.Unmarshal(conds, &st.Conditions); err != nil {
			return nil, fmt.Errorf("components: %s/%s conditions: %w", st.Machine, st.ID, err)
		}
		if err := json.Unmarshal(detail, &st.Detail); err != nil {
			return nil, fmt.Errorf("components: %s/%s detail: %w", st.Machine, st.ID, err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ts is a timestamp argument: native on Postgres, the columns' text form on SQLite.
func ts(db *sql.DB, t time.Time) any {
	if IsSQLite(db) {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC()
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

// nullTime scans a timestamp stored natively (Postgres) or as text (SQLite).
type nullTime struct {
	T     time.Time
	Valid bool
}

func (n *nullTime) Scan(v any) error {
	n.Valid = false
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		n.T, n.Valid = x.UTC(), true
		return nil
	case string:
		return n.parse(x)
	case []byte:
		return n.parse(string(x))
	}
	return fmt.Errorf("components: cannot scan %T as a time", v)
}

func (n *nullTime) parse(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			n.T, n.Valid = t.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("components: bad timestamp %q", s)
}
