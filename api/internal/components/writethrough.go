package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	comps "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// A Mirror is the older switch a component's desired state keeps in step (story
// 51.07): the extensions row of a built-in extension, the tuner row of the HDHomeRun.
// The component's desired state decides; the mirror is written in the same
// transaction, so a release that reads only the older switch stays right. A hold does
// not touch the mirror (the component is still enabled, only stopped now).
type Mirror struct {
	// Write sets the older switch to enabled.
	Write func(ctx context.Context, tx *sql.Tx, enabled bool) error
	// Read answers the older switch and when it last changed (zero: unknown);
	// ok false when it has no row yet.
	Read func(ctx context.Context, q *sql.DB) (enabled bool, at time.Time, ok bool, err error)
}

// Mirrors is the write-through table: component id -> its older switch. Every writer of
// one side (PUT /api/extensions/{name}, the TV settings, PUT /api/components/{id})
// goes through SetDesiredTx, so neither side is ever updated alone.
var Mirrors = map[string]Mirror{
	"ext.plex":              extensionMirror("plex"),
	"ext.dizquetv":          extensionMirror("dizquetv"),
	"ext.cloudflare_stream": extensionMirror("cloudflare_stream"),
	"livetv.hdhomerun":      hdhrMirror,
}

// ExtensionComponent is the component of a built-in extension ("plex" -> "ext.plex"),
// or "" for one loaded from elsewhere (it keeps its extensions row as its only switch).
func ExtensionComponent(name string) string {
	id := "ext." + name
	if _, ok := Mirrors[id]; !ok {
		return ""
	}
	return id
}

func extensionMirror(name string) Mirror {
	return Mirror{
		Write: func(ctx context.Context, tx *sql.Tx, enabled bool) error {
			// the row exists once the pipeline saw the extension; before that the
			// component's desired state alone decides, and the pipeline's ensure_rows
			// starts it disabled (the seed then agrees)
			_, err := tx.ExecContext(ctx,
				`UPDATE extensions SET enabled = $2, updated_at = $3 WHERE name = $1 AND enabled <> $2`,
				name, enabled, time.Now().UTC())
			return err
		},
		Read: func(ctx context.Context, q *sql.DB) (bool, time.Time, bool, error) {
			var enabled bool
			var at sql.NullString
			err := q.QueryRowContext(ctx, `SELECT enabled, CAST(updated_at AS TEXT) FROM extensions WHERE name = $1`, name).
				Scan(&enabled, &at)
			if errors.Is(err, sql.ErrNoRows) {
				return false, time.Time{}, false, nil
			}
			if err != nil {
				return false, time.Time{}, false, err
			}
			t, _ := parseTime(at.String) // unreadable: unknown, the desired state wins
			return enabled, t, true, nil
		},
	}
}

var hdhrMirror = Mirror{
	Write: func(ctx context.Context, tx *sql.Tx, enabled bool) error {
		_, err := tx.ExecContext(ctx, `UPDATE hdhr_device SET enabled = $1 WHERE id = 1 AND enabled <> $1`, enabled)
		return err
	},
	Read: func(ctx context.Context, q *sql.DB) (bool, time.Time, bool, error) {
		var enabled bool
		err := q.QueryRowContext(ctx, `SELECT enabled FROM hdhr_device WHERE id = 1`).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return false, time.Time{}, false, nil
		}
		if err != nil {
			return false, time.Time{}, false, err
		}
		return enabled, time.Time{}, true, nil // no time of change: the desired state wins
	},
}

// SetDesiredTx writes d (and its audit row) through s inside tx, and the component's
// mirror with it: both commit or roll back together. The caller commits.
func SetDesiredTx(ctx context.Context, s *store.Store, tx *sql.Tx, d store.Desired, a store.Audit) (store.Desired, error) {
	after, err := s.SetDesiredTx(ctx, tx, d, a)
	if err != nil {
		return store.Desired{}, err
	}
	if err := writeMirror(ctx, tx, after); err != nil {
		return store.Desired{}, err
	}
	return after, nil
}

// writeMirror writes the older switch of the row as written (after), inside tx. A
// component with no mirror is left alone. The batch writes of Service.Apply (PUT, apply,
// a bundle) call it for each row, as store.BatchPlan.AfterWrite.
func writeMirror(ctx context.Context, tx *sql.Tx, after store.Desired) error {
	m, ok := Mirrors[after.ID]
	if !ok {
		return nil
	}
	if err := m.Write(ctx, tx, after.Want == comps.WantEnabled); err != nil {
		return fmt.Errorf("components: the mirror of %s: %w", after.ID, err)
	}
	return nil
}

// SetDesired is SetDesiredTx in a transaction of its own.
func SetDesired(ctx context.Context, s *store.Store, d store.Desired, a store.Audit) (store.Desired, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return store.Desired{}, err
	}
	defer func() { _ = tx.Rollback() }()
	after, err := SetDesiredTx(ctx, s, tx, d, a)
	if err != nil {
		return store.Desired{}, err
	}
	return after, tx.Commit()
}

// Switch turns component id on (want enabled) or off (want installed) inside tx, keeping
// its hold, and writes the mirror: what an older switch's route (PUT
// /api/extensions/{name}) calls when it changes enabled.
func Switch(ctx context.Context, s *store.Store, tx *sql.Tx, id string, enabled bool, a store.Audit) (store.Desired, error) {
	d := store.Desired{ID: id, Want: comps.WantInstalled}
	if enabled {
		d.Want = comps.WantEnabled
	}
	cur, err := desiredIn(ctx, tx, id)
	if err != nil {
		return store.Desired{}, err
	}
	if cur != nil {
		d.Hold = cur.Hold
	}
	if a.Action == "" {
		a.Action = "component.disable"
		if enabled {
			a.Action = "component.enable"
		}
	}
	return SetDesiredTx(ctx, s, tx, d, a)
}

// desiredIn reads id's hold inside tx (nil: no row).
func desiredIn(ctx context.Context, tx *sql.Tx, id string) (*store.Desired, error) {
	var by sql.NullString
	var at sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT hold_by, CAST(hold_at AS TEXT) FROM components WHERE id = $1`, id).Scan(&by, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d := &store.Desired{ID: id}
	if by.Valid && at.Valid {
		t, _ := parseTime(at.String)
		d.Hold = &store.Hold{By: by.String, At: t}
	}
	return d, nil
}

// timeLayouts are how Postgres (CAST AS TEXT) and SQLite (the drivers' text) write a time.
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999",
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("components: unreadable time %q", s)
}

// RepairMirrors runs at the home API's start: a mirror a release before 51.07 changed
// alone (a rollback and forward again) disagrees with the desired state. The newer side
// wins (a mirror with no time of change loses) and the other is written, with one audit
// row component.mirror_repaired each. Components with no desired row are the seed's.
// Answers the ids repaired.
func RepairMirrors(ctx context.Context, s *store.Store, logger *slog.Logger) ([]string, error) {
	ids := make([]string, 0, len(Mirrors))
	for id := range Mirrors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var repaired []string
	for _, id := range ids {
		m := Mirrors[id]
		d, err := s.DesiredOf(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return repaired, err
		}
		on, at, ok, err := m.Read(ctx, s.DB)
		if err != nil {
			return repaired, fmt.Errorf("components: the mirror of %s: %w", id, err)
		}
		if !ok || on == (d.Want == comps.WantEnabled) {
			continue
		}
		desiredAt, err := updatedAt(ctx, s.DB, id)
		if err != nil {
			return repaired, err
		}
		if err := repairOne(ctx, s, id, d, on, !at.IsZero() && at.After(desiredAt)); err != nil {
			return repaired, err
		}
		repaired = append(repaired, id)
		if logger != nil {
			logger.Warn("components: a mirror disagreed with the desired state and was repaired",
				"event", "component_mirror_repaired", "component", id, "mirror_enabled", on,
				"want", d.Want, "mirror_won", !at.IsZero() && at.After(desiredAt))
		}
	}
	return repaired, nil
}

func repairOne(ctx context.Context, s *store.Store, id string, d store.Desired, on, mirrorWins bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	audit := store.Audit{By: "system", ActorSource: "system", Action: "component.mirror_repaired"}
	if mirrorWins {
		want := comps.WantInstalled
		if on {
			want = comps.WantEnabled
		}
		if d.Want == comps.WantAbsent && !on {
			want = comps.WantAbsent // off either way: keep what the admin removed
		}
		d.Want = want
		if _, err := s.SetDesiredTx(ctx, tx, d, audit); err != nil {
			return err
		}
	} else {
		if err := Mirrors[id].Write(ctx, tx, d.Want == comps.WantEnabled); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"mirror_enabled": on, "want": d.Want})
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (category, action, target_id, target_kind,
			actor_source, payload) VALUES ($1, 'component.mirror_repaired', $2, 'component', 'system', $3)`,
			store.AuditCategory, id, string(payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func updatedAt(ctx context.Context, db *sql.DB, id string) (time.Time, error) {
	var at sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT CAST(updated_at AS TEXT) FROM components WHERE id = $1`, id).Scan(&at); err != nil {
		return time.Time{}, err
	}
	t, _ := parseTime(at.String)
	return t, nil
}
