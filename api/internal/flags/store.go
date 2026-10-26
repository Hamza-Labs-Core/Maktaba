package flags

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Override is one row of feature_flag_overrides (slot 0157).
type Override struct {
	ID         string     `json:"id"`
	FlagKey    string     `json:"flag_key"`
	Scope      string     `json:"scope"`
	ScopeValue string     `json:"scope_value,omitempty"`
	Value      any        `json:"value"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedBy  string     `json:"created_by,omitempty"`
}

// ErrNotFound is returned for an override or a cohort membership that
// does not exist.
var ErrNotFound = errors.New("flags: not found")

// Store reads and writes the override and cohort tables (Postgres).
type Store struct {
	DB *sql.DB
}

const overrideCols = `CAST(id AS TEXT), flag_key, scope, COALESCE(scope_value, ''), CAST(value AS TEXT),
	created_at, expires_at, COALESCE(CAST(created_by AS TEXT), '')`

type rowScanner interface{ Scan(dest ...any) error }

func scanOverride(row rowScanner) (Override, error) {
	var (
		o   Override
		raw string
		exp sql.NullTime
	)
	if err := row.Scan(&o.ID, &o.FlagKey, &o.Scope, &o.ScopeValue, &raw, &o.CreatedAt, &exp, &o.CreatedBy); err != nil {
		return o, err
	}
	if err := json.Unmarshal([]byte(raw), &o.Value); err != nil {
		return o, err
	}
	if exp.Valid {
		t := exp.Time
		o.ExpiresAt = &t
	}
	return o, nil
}

func scanOverrides(rows *sql.Rows) ([]Override, error) {
	defer rows.Close()
	out := []Override{}
	for rows.Next() {
		o, err := scanOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Applicable returns the unexpired overrides that can apply to a user
// of tier in cohorts (userID "" for a principal that is no user), oldest
// first so a later row wins its scope.
func (s *Store) Applicable(ctx context.Context, tier, userID string, cohorts []string) ([]Override, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+overrideCols+`
		FROM feature_flag_overrides
		WHERE (expires_at IS NULL OR expires_at > now())
		  AND (scope = 'global'
		       OR (scope = 'tier' AND scope_value = $1)
		       OR (scope = 'user' AND scope_value = $2)
		       OR (scope = 'cohort' AND scope_value = ANY($3)))
		ORDER BY created_at, id`, tier, userID, pq.Array(cohorts))
	if err != nil {
		return nil, err
	}
	return scanOverrides(rows)
}

// List returns every override, oldest first.
func (s *Store) List(ctx context.Context) ([]Override, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+overrideCols+` FROM feature_flag_overrides ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	return scanOverrides(rows)
}

// Get returns the override id.
func (s *Store) Get(ctx context.Context, id string) (Override, error) {
	o, err := scanOverride(s.DB.QueryRowContext(ctx, `SELECT `+overrideCols+` FROM feature_flag_overrides WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

func nullUUID(s string) any {
	if _, err := uuid.Parse(s); err != nil {
		return nil
	}
	return s
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Create inserts o (its ID and CreatedAt are set here) and returns it.
func (s *Store) Create(ctx context.Context, o Override) (Override, error) {
	raw, err := json.Marshal(o.Value)
	if err != nil {
		return o, err
	}
	o.ID = uuid.NewString()
	var exp any
	if o.ExpiresAt != nil {
		exp = *o.ExpiresAt
	}
	return scanOverride(s.DB.QueryRowContext(ctx, `INSERT INTO feature_flag_overrides
		(id, flag_key, scope, scope_value, value, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+overrideCols,
		o.ID, o.FlagKey, o.Scope, nullString(o.ScopeValue), string(raw), exp, nullUUID(o.CreatedBy)))
}

// Update writes o's value and expiry over the row o.ID.
func (s *Store) Update(ctx context.Context, o Override) (Override, error) {
	raw, err := json.Marshal(o.Value)
	if err != nil {
		return o, err
	}
	var exp any
	if o.ExpiresAt != nil {
		exp = *o.ExpiresAt
	}
	got, err := scanOverride(s.DB.QueryRowContext(ctx, `UPDATE feature_flag_overrides
		SET value = $2, expires_at = $3 WHERE id = $1 RETURNING `+overrideCols, o.ID, string(raw), exp))
	if errors.Is(err, sql.ErrNoRows) {
		return got, ErrNotFound
	}
	return got, err
}

// Delete removes the override id.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM feature_flag_overrides WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CohortsOf returns the cohorts userID belongs to (none for a non-UUID).
func (s *Store) CohortsOf(ctx context.Context, userID string) ([]string, error) {
	if nullUUID(userID) == nil {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT cohort FROM beta_cohorts WHERE user_id = $1 ORDER BY cohort`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CohortSizes returns the member count of every cohort with a member.
func (s *Store) CohortSizes(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT cohort, COUNT(*) FROM beta_cohorts GROUP BY cohort`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			c string
			n int
		)
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

// ExistingUsers returns which of ids (all valid UUIDs) name a user.
func (s *Store) ExistingUsers(ctx context.Context, ids []string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT CAST(id AS TEXT) FROM users WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// UserIsAdmin reports whether userID names a user, and whether an admin.
func (s *Store) UserIsAdmin(ctx context.Context, userID string) (found, admin bool, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id = $1`, userID).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return err == nil, admin, err
}

// AddToCohort puts every user of ids (existing users) in cohort and
// returns how many were not in it already.
func (s *Store) AddToCohort(ctx context.Context, cohort string, ids []string) (int, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO beta_cohorts (user_id, cohort)
		SELECT u, $2 FROM unnest($1::uuid[]) AS u
		ON CONFLICT (user_id, cohort) DO NOTHING`, pq.Array(ids), cohort)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// RemoveFromCohort takes userID out of cohort.
func (s *Store) RemoveFromCohort(ctx context.Context, cohort, userID string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM beta_cohorts WHERE user_id = $1 AND cohort = $2`, userID, cohort)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
