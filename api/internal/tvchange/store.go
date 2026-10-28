// Package tvchange is the TV change controller (Epic 33, stories 33.10 and 33.12):
// the change store, the state machine of design §11.2, approval and the auto-promote
// rules, the open drafts the channel editor and the TV settings write into, and the
// controller loop that verifies staged changes, promotes approved ones and enqueues
// the pipeline's tv_stage / tv_swap / tv_gc jobs.
//
// The release and schedule-generation building (tv_stage) and the swap commit
// (tv_swap) are the pipeline's (pipeline/src/maktaba_pipeline/tvrelease): this package
// moves states and enqueues them.
package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// States of a TV change (design §11, migration 0127).
const (
	StateDraft      = "draft"
	StateStaged     = "staged"
	StateVerifying  = "verifying"
	StateReady      = "ready"
	StateApproved   = "approved"
	StateSwapping   = "swapping"
	StateLive       = "live"
	StateSuperseded = "superseded"
	StateFailed     = "failed"
	StateCancelled  = "cancelled"
	StateRolledBack = "rolled_back"
)

// InFlight are the states at most one change is in at a time (README R9.4).
var InFlight = []string{StateStaged, StateVerifying, StateReady, StateApproved, StateSwapping}

// Change is a tv_changes row over the wire.
type Change struct {
	ID            string          `json:"id"`
	Number        int64           `json:"number"`
	ReleaseID     *string         `json:"release_id,omitempty"`
	BaseReleaseID *string         `json:"base_release_id,omitempty"`
	State         string          `json:"state"`
	Kinds         []string        `json:"kinds"`
	Title         string          `json:"title"`
	Source        map[string]any  `json:"source"`
	ChangeSet     json.RawMessage `json:"change_set,omitempty"`
	Digest        *string         `json:"digest,omitempty"`
	Diff          map[string]any  `json:"diff"`
	VerifyReport  map[string]any  `json:"verify_report"`
	AutoRule      *string         `json:"auto_rule,omitempty"`
	ApprovedBy    []string        `json:"approved_by"`
	ApprovedAt    *time.Time      `json:"approved_at,omitempty"`
	AnchorAt      *time.Time      `json:"anchor_at,omitempty"`
	SwapAt        *time.Time      `json:"swap_at,omitempty"`
	SwapWait      *string         `json:"swap_wait,omitempty"`
	CreatedBy     *string         `json:"created_by,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	StagedAt      *time.Time      `json:"staged_at,omitempty"`
	ReadyAt       *time.Time      `json:"ready_at,omitempty"`
	SwappedAt     *time.Time      `json:"swapped_at,omitempty"`
	EndedAt       *time.Time      `json:"ended_at,omitempty"`
	EndReason     *string         `json:"end_reason,omitempty"`
	SupersededBy  *string         `json:"superseded_by,omitempty"`
	// Origin is who made it (migration 0169): OriginAuto, the system (it goes on the air
	// by its kind's rule, with no approval), or OriginManual, a person (it waits for an
	// admin's approval). A change a person's edit joined is manual.
	Origin string `json:"origin"`
	// AllowedActions are the admin actions the state allows (story 33.10 AC8).
	AllowedActions []string `json:"allowed_actions"`
	// Events is the history (detail only).
	Events []Event `json:"events,omitempty"`
}

// SourceKind is the producer kind (write_channels, editor, settings, schedule, manual).
func (c *Change) SourceKind() string {
	if s, ok := c.Source["kind"].(string); ok {
		return s
	}
	return ""
}

// Pending is the change's diff.pending (what the producer wants).
func (c *Change) Pending() *Pending {
	m, _ := c.Diff["pending"].(map[string]any)
	return &Pending{M: m}
}

// Event is a tv_change_events row.
type Event struct {
	ID          int64          `json:"id"`
	At          time.Time      `json:"at"`
	ActorUserID *string        `json:"actor_user_id,omitempty"`
	ActorSource string         `json:"actor_source"`
	FromState   *string        `json:"from_state,omitempty"`
	ToState     *string        `json:"to_state,omitempty"`
	Action      string         `json:"action"`
	Detail      map[string]any `json:"detail"`
}

// ErrNotFound is a missing change.
var ErrNotFound = errors.New("tvchange: not found")

// Store reads and writes tv_changes / tv_change_events / audit_log.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// IsSQLite reports whether db is the SQLite backend.
func IsSQLite(db *sql.DB) bool {
	return db != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
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
	case string:
		return n.parse(x)
	case []byte:
		return n.parse(string(x))
	default:
		return fmt.Errorf("nullTime: cannot scan %T", v)
	}
	return nil
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
	return fmt.Errorf("nullTime: bad timestamp %q", s)
}

func (n nullTime) ptr() *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.T
	return &t
}

func strPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

func decodeJSON(b []byte, into any) {
	if len(b) > 0 {
		_ = json.Unmarshal(b, into)
	}
}

const changeCols = `id, number, release_id, base_release_id, state, kinds, title, source, change_set,
	digest, diff, verify_report, auto_rule, approved_by, approved_at, anchor_at, swap_at, swap_wait,
	created_by, created_at, updated_at, staged_at, ready_at, swapped_at, ended_at, end_reason, superseded_by, origin`

// summaryCols is changeCols with an empty diff: a list never needs the diff,
// and a channel plan's is tens of MB (the changes page read them all, ~500 MB a
// load, and took 40 s). The literal keeps scanChange's column order.
var summaryCols = strings.Replace(changeCols, "digest, diff,", "digest, '{}' AS diff,", 1)

func scanChange(rs interface{ Scan(...any) error }) (*Change, error) {
	var c Change
	var num sql.NullInt64
	var rel, base, digest, rule, wait, by, reason, sup, origin sql.NullString
	var kinds, source, cs, diff, vr, appr []byte
	var approvedAt, anchorAt, swapAt, createdAt, updatedAt, stagedAt, readyAt, swappedAt, endedAt nullTime
	if err := rs.Scan(&c.ID, &num, &rel, &base, &c.State, &kinds, &c.Title, &source, &cs, &digest,
		&diff, &vr, &rule, &appr, &approvedAt, &anchorAt, &swapAt, &wait, &by, &createdAt,
		&updatedAt, &stagedAt, &readyAt, &swappedAt, &endedAt, &reason, &sup, &origin); err != nil {
		return nil, err
	}
	c.Number = num.Int64
	c.ReleaseID, c.BaseReleaseID, c.Digest = strPtr(rel), strPtr(base), strPtr(digest)
	c.AutoRule, c.SwapWait, c.CreatedBy = strPtr(rule), strPtr(wait), strPtr(by)
	c.EndReason, c.SupersededBy = strPtr(reason), strPtr(sup)
	c.Origin = OriginManual
	if origin.String == OriginAuto {
		c.Origin = OriginAuto
	}
	c.Kinds, c.ApprovedBy = []string{}, []string{}
	c.Source, c.Diff, c.VerifyReport = map[string]any{}, map[string]any{}, map[string]any{}
	decodeJSON(kinds, &c.Kinds)
	decodeJSON(source, &c.Source)
	decodeJSON(diff, &c.Diff)
	decodeJSON(vr, &c.VerifyReport)
	decodeJSON(appr, &c.ApprovedBy)
	if c.Kinds == nil {
		c.Kinds = []string{}
	}
	if c.ApprovedBy == nil {
		c.ApprovedBy = []string{}
	}
	if c.Source == nil {
		c.Source = map[string]any{}
	}
	if c.Diff == nil {
		c.Diff = map[string]any{}
	}
	if c.VerifyReport == nil {
		c.VerifyReport = map[string]any{}
	}
	if len(cs) > 0 && string(cs) != "{}" {
		c.ChangeSet = json.RawMessage(cs)
	}
	c.ApprovedAt, c.AnchorAt, c.SwapAt = approvedAt.ptr(), anchorAt.ptr(), swapAt.ptr()
	c.CreatedAt, c.UpdatedAt = createdAt.T, updatedAt.T
	c.StagedAt, c.ReadyAt, c.SwappedAt, c.EndedAt = stagedAt.ptr(), readyAt.ptr(), swappedAt.ptr(), endedAt.ptr()
	c.AllowedActions = AllowedActions(c.State, c.SourceKind())
	return &c, nil
}

// Get loads one change.
func (s *Store) Get(ctx context.Context, q queryer, id string) (*Change, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	c, err := scanChange(q.QueryRowContext(ctx, `SELECT `+changeCols+` FROM tv_changes WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// List returns changes, newest first, optionally of some states.
func (s *Store) List(ctx context.Context, states []string, limit int) ([]*Change, error) {
	return s.list(ctx, changeCols, states, limit)
}

// ListSummaries is List without each change's diff (an empty map): what the
// changes page shows. Open a change (Get) for its diff.
func (s *Store) ListSummaries(ctx context.Context, states []string, limit int) ([]*Change, error) {
	return s.list(ctx, summaryCols, states, limit)
}

func (s *Store) list(ctx context.Context, cols string, states []string, limit int) ([]*Change, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT ` + cols + ` FROM tv_changes`
	args := []any{}
	if len(states) > 0 {
		ph := make([]string, len(states))
		for i, st := range states {
			args = append(args, st)
			ph[i] = fmt.Sprintf("$%d", i+1)
		}
		q += ` WHERE state IN (` + strings.Join(ph, ",") + `)`
	}
	args = append(args, limit)
	q += fmt.Sprintf(` ORDER BY created_at DESC, number DESC LIMIT $%d`, len(args))
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Change{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// InFlightID is the id of the change in staged…swapping, or "" (no row read).
func (s *Store) InFlightID(ctx context.Context, q queryer) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM tv_changes
		WHERE state IN ('staged','verifying','ready','approved','swapping') ORDER BY created_at LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// InFlightChange is the change in staged…swapping, or nil.
func (s *Store) InFlightChange(ctx context.Context, q queryer) (*Change, error) {
	c, err := scanChange(q.QueryRowContext(ctx, `SELECT `+changeCols+` FROM tv_changes
		WHERE state IN ('staged','verifying','ready','approved','swapping') ORDER BY created_at LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// Events is a change's history, oldest first.
func (s *Store) Events(ctx context.Context, id string) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, at, actor_user_id, actor_source, from_state,
		to_state, action, detail FROM tv_change_events WHERE change_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at nullTime
		var actor, from, to sql.NullString
		var detail []byte
		if err := rows.Scan(&e.ID, &at, &actor, &e.ActorSource, &from, &to, &e.Action, &detail); err != nil {
			return nil, err
		}
		e.At, e.ActorUserID, e.FromState, e.ToState = at.T, strPtr(actor), strPtr(from), strPtr(to)
		e.Detail = map[string]any{}
		decodeJSON(detail, &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Insert creates a change (a draft, normally) and its creation event.
func (s *Store) Insert(ctx context.Context, q queryer, c *Change) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := s.now()
	c.CreatedAt, c.UpdatedAt = now, now
	src, _ := json.Marshal(nonNilMap(c.Source))
	diff, _ := json.Marshal(nonNilMap(c.Diff))
	var by any
	if c.CreatedBy != nil {
		by = *c.CreatedBy
	}
	if c.Origin != OriginAuto && c.Origin != OriginManual {
		c.Origin = OriginFor(c.SourceKind(), c.CreatedBy)
	}
	var err error
	if IsSQLite(s.DB) {
		_, err = q.ExecContext(ctx, `INSERT INTO tv_changes (id, number, state, title, source, diff,
			created_by, created_at, updated_at, origin) VALUES ($1, (SELECT COALESCE(MAX(number), 0) + 1 FROM tv_changes),
			$2, $3, $4, $5, $6, $7, $7, $8)`, c.ID, c.State, c.Title, string(src), string(diff), by, ts(s.DB, now), c.Origin)
	} else {
		_, err = q.ExecContext(ctx, `INSERT INTO tv_changes (id, state, title, source, diff, created_by,
			created_at, updated_at, origin) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8)`,
			c.ID, c.State, c.Title, string(src), string(diff), by, now, c.Origin)
	}
	if err != nil {
		return err
	}
	return s.Event(ctx, q, c.ID, Event{ActorUserID: c.CreatedBy, ActorSource: actorSource(c.CreatedBy, c.SourceKind()),
		ToState: &c.State, Action: "create", Detail: map[string]any{"source": c.Source}})
}

func actorSource(user *string, fallback string) string {
	if user != nil && *user != "" {
		return "admin"
	}
	if fallback == "" {
		return "system"
	}
	return fallback
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// ts is a timestamp parameter: native on Postgres, RFC 3339 text on SQLite.
func ts(db *sql.DB, t time.Time) any {
	if IsSQLite(db) {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC()
}

// Set writes columns of a change (JSON values are marshalled, times converted).
func (s *Store) Set(ctx context.Context, q queryer, id string, fields map[string]any) error {
	sets := []string{}
	args := []any{id}
	fields["updated_at"] = s.now()
	for k, v := range fields {
		switch x := v.(type) {
		case map[string]any, []string, []any:
			b, err := json.Marshal(x)
			if err != nil {
				return err
			}
			v = string(b)
		case time.Time:
			v = ts(s.DB, x)
		case *time.Time:
			if x == nil {
				v = nil
			} else {
				v = ts(s.DB, *x)
			}
		}
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", k, len(args)))
	}
	_, err := q.ExecContext(ctx, `UPDATE tv_changes SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...)
	return err
}

// Event appends a tv_change_events row.
func (s *Store) Event(ctx context.Context, q queryer, changeID string, e Event) error {
	detail, _ := json.Marshal(nonNilMap(e.Detail))
	var actor, from, to any
	if e.ActorUserID != nil {
		actor = *e.ActorUserID
	}
	if e.FromState != nil {
		from = *e.FromState
	}
	if e.ToState != nil {
		to = *e.ToState
	}
	if e.ActorSource == "" {
		e.ActorSource = "system"
	}
	_, err := q.ExecContext(ctx, `INSERT INTO tv_change_events (change_id, at, actor_user_id, actor_source,
		from_state, to_state, action, detail) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		changeID, ts(s.DB, s.now()), actor, e.ActorSource, from, to, e.Action, string(detail))
	return err
}

// Audit writes an admin action to audit_log (category tv_change, actor_source admin).
// Best-effort on a database whose audit_log lacks the 0054 columns.
func (s *Store) Audit(ctx context.Context, q queryer, userID, action, changeID string, payload map[string]any) {
	s.audit(ctx, q, "admin", userID, action, changeID, payload)
}

func (s *Store) audit(ctx context.Context, q queryer, source, userID, action, changeID string, payload map[string]any) {
	b, _ := json.Marshal(nonNilMap(payload))
	var actor any
	if _, err := uuid.Parse(userID); err == nil {
		actor = userID
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id,
		target_kind, actor_source, payload) VALUES ('tv_change', $1, $2, $3, 'tv_change', $4, $5)`,
		action, actor, changeID, source, string(b)); err != nil {
		_, _ = q.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
			VALUES ('tv_change', $1, $2, $3, $4)`, action, actor, changeID, string(b))
	}
}

// AuditRow is one audit_log row of a change.
type AuditRow struct {
	ID          int64          `json:"id"`
	At          time.Time      `json:"at"`
	Action      string         `json:"action"`
	ActorUserID *string        `json:"actor_user_id,omitempty"`
	ActorSource string         `json:"actor_source"`
	Payload     map[string]any `json:"payload"`
}

// AuditRows are a change's audit_log rows, oldest first (story 33.13 AC3).
func (s *Store) AuditRows(ctx context.Context, changeID string) ([]AuditRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, ts, action, actor_user_id, COALESCE(actor_source, ''), payload
		FROM audit_log WHERE category = 'tv_change' AND target_id = $1 ORDER BY id`, changeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var a AuditRow
		var at nullTime
		var actor sql.NullString
		var payload []byte
		if err := rows.Scan(&a.ID, &at, &a.Action, &actor, &a.ActorSource, &payload); err != nil {
			return nil, err
		}
		a.At, a.ActorUserID = at.T, strPtr(actor)
		a.Payload = map[string]any{}
		decodeJSON(payload, &a.Payload)
		out = append(out, a)
	}
	return out, rows.Err()
}

// Move transitions a change from its current state (which must be from) to `to` with
// extra column values, recording the event. ErrConflict when it is elsewhere.
func (s *Store) Move(ctx context.Context, q queryer, c *Change, to, action string, actor *string,
	detail map[string]any, fields map[string]any) error {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["state"] = to
	args := []any{c.ID, c.State}
	sets := []string{}
	fields["updated_at"] = s.now()
	for k, v := range fields {
		switch x := v.(type) {
		case map[string]any, []string, []any:
			b, _ := json.Marshal(x)
			v = string(b)
		case time.Time:
			v = ts(s.DB, x)
		case *time.Time:
			if x == nil {
				v = nil
			} else {
				v = ts(s.DB, *x)
			}
		}
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", k, len(args)))
	}
	res, err := q.ExecContext(ctx, `UPDATE tv_changes SET `+strings.Join(sets, ", ")+
		` WHERE id = $1 AND state = $2`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &ConflictError{State: c.State}
	}
	from := c.State
	src := "system"
	if actor != nil {
		src = "admin"
	}
	if err := s.Event(ctx, q, c.ID, Event{ActorUserID: actor, ActorSource: src, FromState: &from, ToState: &to,
		Action: action, Detail: detail}); err != nil {
		return err
	}
	c.State = to
	c.AllowedActions = AllowedActions(to, c.SourceKind())
	return nil
}

// HeldAfterSwapFailure reports whether a change's last swap failed and no admin has
// acted on it since (the controller then promotes nothing by itself).
func (s *Store) HeldAfterSwapFailure(ctx context.Context, q queryer, id string) (bool, error) {
	var failed, admin int64
	if err := q.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT MAX(id) FROM tv_change_events WHERE change_id = $1 AND action = 'swap_failed'), 0),
		COALESCE((SELECT MAX(id) FROM tv_change_events WHERE change_id = $1 AND actor_source = 'admin'), 0)`,
		id).Scan(&failed, &admin); err != nil {
		return false, err
	}
	return failed > admin, nil
}

// Contributors are the users who created a change or saved an edit into it (its create
// and append events, and created_by): with require_second_admin none of them approves.
func (s *Store) Contributors(ctx context.Context, q queryer, c *Change) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	if c.CreatedBy != nil && *c.CreatedBy != "" {
		seen[*c.CreatedBy] = true
		out = append(out, *c.CreatedBy)
	}
	// a draft that joined this change (failToDraft) brought its authors with it
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT actor_user_id FROM tv_change_events
		WHERE actor_user_id IS NOT NULL AND action IN ('create', 'append')
		  AND (change_id = $1 OR change_id IN (SELECT id FROM tv_changes WHERE superseded_by = $1
		       AND end_reason = 'joined_change'))`, c.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out, rows.Err()
}

// ConflictError is an invalid transition (409, with the current state).
type ConflictError struct {
	State  string
	Reason string
	// InFlight is the change already in staged…swapping (change_in_flight).
	InFlight string
}

func (e *ConflictError) Error() string {
	if e.Reason != "" {
		return e.Reason
	}
	return "change is " + e.State
}
