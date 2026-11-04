package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// The seed (story 51.03): desired state written once, from what a home runs today, by the
// home's supervisor at boot. It is the one write of desired state the API does not make.

// SeedBy is updated_by (and the audit actor) of every row the seed writes.
const SeedBy = "seed"

// SeedLock is the advisory lock key the seed holds in its transaction (Postgres).
const SeedLock = "components_seed"

// Queryer is what a seed plan reads the database through: the seed's transaction.
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SeedPlan is what one seed writes: a want per component, the bundle, the release that
// seeded and the server.toml values it imported.
type SeedPlan struct {
	Wants        map[string]string // id -> absent | installed | enabled
	Bundle       string            // full | core | worker | custom
	Release      string
	TomlImported map[string]string // "tv.slots" -> "on", ...
}

// Meta is component_meta: the revision, the bundle and the seed marker.
type Meta struct {
	Revision      int64
	Bundle        string
	SeededAt      time.Time // zero: not seeded
	SeededRelease string
	TomlImported  map[string]string
}

// Seeded reports whether the seed has run.
func (m Meta) Seeded() bool { return !m.SeededAt.IsZero() }

// Meta reads component_meta.
func (s *Store) Meta(ctx context.Context) (Meta, error) {
	var m Meta
	var bundle, release sql.NullString
	var at nullTime
	var imported []byte
	err := s.DB.QueryRowContext(ctx, `SELECT revision, bundle, seeded_at, seeded_release, toml_imported
		FROM component_meta`).Scan(&m.Revision, &bundle, &at, &release, &imported)
	if err != nil {
		return Meta{}, err
	}
	m.Bundle, m.SeededRelease = bundle.String, release.String
	if at.Valid {
		m.SeededAt = at.T
	}
	if len(imported) > 0 {
		if err := json.Unmarshal(imported, &m.TomlImported); err != nil {
			return Meta{}, fmt.Errorf("components: component_meta.toml_imported: %w", err)
		}
	}
	return m, nil
}

// Seed writes the plan once. In one transaction it takes the seed's lock (Postgres:
// pg_advisory_xact_lock), and only when component_meta.seeded_at is NULL asks plan for
// the rows (plan reads through the transaction), inserts them (a row already there is
// kept), sets the marker and writes one audit row. It answers false when the seed had run
// already: a second boot, a second machine, or a racing boot that came second. A crash
// halfway rolls the whole transaction back, so the next boot seeds again.
func (s *Store) Seed(ctx context.Context, plan func(ctx context.Context, q Queryer) (SeedPlan, error)) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if !s.sqlite() {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, SeedLock); err != nil {
			return false, fmt.Errorf("components: seed lock: %w", err)
		}
	}
	// the conditional update also takes the row's (SQLite: the database's) write lock, so
	// a second writer waits here and then finds the marker set
	res, err := tx.ExecContext(ctx, `UPDATE component_meta SET seeded_at = $1 WHERE seeded_at IS NULL`, ts(s.DB, time.Now()))
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, err
	} else if n == 0 {
		return false, nil
	}
	p, err := plan(ctx, tx)
	if err != nil {
		return false, err
	}
	if p.Bundle == "" {
		return false, errors.New("components: a seed needs a bundle")
	}
	ids := make([]string, 0, len(p.Wants))
	for id, want := range p.Wants {
		if !validWant(want) {
			return false, fmt.Errorf("components: seed: bad want %q for %s", want, id)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO components (id, want, updated_by) VALUES ($1, $2, $3)
			ON CONFLICT (id) DO NOTHING`, id, p.Wants[id], SeedBy); err != nil {
			return false, err
		}
	}
	imported := p.TomlImported
	if imported == nil {
		imported = map[string]string{}
	}
	raw, err := json.Marshal(imported)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE component_meta SET bundle = $1, seeded_release = $2, toml_imported = $3`,
		p.Bundle, p.Release, string(raw)); err != nil {
		return false, err
	}
	payload, err := json.Marshal(map[string]any{"bundle": p.Bundle, "release": p.Release, "wants": p.Wants,
		"toml_imported": imported})
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (category, action, target_kind, actor_source, payload)
		VALUES ($1, 'component.seed', 'component', $2, $3)`, AuditCategory, SeedBy, string(payload)); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// SeedOverrides writes a joined machine's first overrides (where its own server.toml
// differs from the home), each only where the machine has no override row for that
// component yet, and answers how many it wrote. On Postgres it goes through the guarded
// component_override_seed (a machine's login writes only its own rows).
func (s *Store) SeedOverrides(ctx context.Context, node string, wants map[string]string) (int, error) {
	if node == "" {
		return 0, errors.New("components: an override seed needs a node")
	}
	type row struct {
		ID   string `json:"id"`
		Want string `json:"want"`
	}
	rows := make([]row, 0, len(wants))
	for id, want := range wants {
		if !validWant(want) {
			return 0, fmt.Errorf("components: override seed: bad want %q for %s", want, id)
		}
		rows = append(rows, row{id, want})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if !s.sqlite() {
		raw, err := json.Marshal(rows)
		if err != nil {
			return 0, err
		}
		var n int
		err = s.DB.QueryRowContext(ctx, `SELECT component_override_seed($1::uuid, $2::jsonb)`, node, string(raw)).Scan(&n)
		return n, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, r := range rows {
		res, err := tx.ExecContext(ctx, `INSERT INTO component_overrides (node_id, id, want, updated_by)
			VALUES ($1, $2, $3, $4) ON CONFLICT (node_id, id) DO NOTHING`, node, r.ID, r.Want, SeedBy)
		if err != nil {
			return 0, err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			n++
		}
	}
	return n, tx.Commit()
}
