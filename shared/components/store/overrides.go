package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Per-machine overrides (story 51.12): what the admin routes read and write for one
// machine. The machine itself reads only its merged answer (Desired with its id).

// Overrides is machine node's override rows, by component id.
func (s *Store) Overrides(ctx context.Context, node string) (map[string]Override, error) {
	return s.overrides(ctx, s.DB, node)
}

func (s *Store) overrides(ctx context.Context, q queryer, node string) (map[string]Override, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, want, hold_by, hold_at, generation
		FROM component_overrides WHERE node_id = $1 ORDER BY id`, node)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Override{}
	for rows.Next() {
		o := Override{Node: node}
		var want, holdBy sql.NullString
		var holdAt nullTime
		if err := rows.Scan(&o.ID, &want, &holdBy, &holdAt, &o.Generation); err != nil {
			return nil, err
		}
		o.Want = want.String
		if holdAt.Valid {
			o.Hold = &Hold{By: holdBy.String, At: holdAt.T}
		}
		out[o.ID] = o
	}
	return out, rows.Err()
}

// Merge is what component_desired_for(node) answers, computed from the home's rows and
// one machine's overrides: the override's want when it has one, else the home's; the
// override's hold when it has one, else the home's (so a hold on either stops it); the
// generation is the sum. A component with neither a home row nor an override want is
// left out (the catalog default).
func Merge(home map[string]Desired, ov map[string]Override) map[string]Desired {
	out := make(map[string]Desired, len(home)+len(ov))
	for id, d := range home {
		out[id] = d
	}
	for id, o := range ov {
		d, has := home[id]
		if !has && o.Want == "" {
			continue
		}
		m := Desired{ID: id, Want: d.Want, Hold: d.Hold, Generation: d.Generation + o.Generation}
		if o.Want != "" {
			m.Want = o.Want
		}
		if o.Hold != nil {
			m.Hold = o.Hold
		}
		out[id] = m
	}
	return out
}

// MachineWrite is one transaction of per-machine writes: it holds the write lock every
// admin write of desired state holds (Postgres), and has read the home's rows, the
// machine's overrides and the revision through the transaction.
type MachineWrite struct {
	Tx        *sql.Tx
	Home      map[string]Desired
	Overrides map[string]Override
	Revision  int64
}

// BeginMachineWrite opens node's write transaction. The caller commits or rolls back.
func (s *Store) BeginMachineWrite(ctx context.Context, node string) (*MachineWrite, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*MachineWrite, error) {
		_ = tx.Rollback()
		return nil, err
	}
	if !s.sqlite() {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, WriteLock); err != nil {
			return fail(fmt.Errorf("components: write lock: %w", err))
		}
	}
	w := &MachineWrite{Tx: tx}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM component_meta`).Scan(&w.Revision); err != nil {
		return fail(err)
	}
	if w.Home, err = s.homeRows(ctx, tx); err != nil {
		return fail(err)
	}
	if w.Overrides, err = s.overrides(ctx, tx, node); err != nil {
		return fail(err)
	}
	return w, nil
}
