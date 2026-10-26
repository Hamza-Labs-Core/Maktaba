package telemetry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Store writes and reads the telemetry tables.
type Store struct {
	DB *sql.DB
}

// Ingest writes every batch of one envelope in one transaction: all of
// it is kept or none.
func (s *Store) Ingest(ctx context.Context, batches []Batch, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, b := range batches {
		switch b.Type {
		case "forget":
			err = forget(ctx, tx, b.InstallID)
		case "server":
			err = putServer(ctx, tx, b.InstallID, b.Server, now)
		case "app":
			err = putApp(ctx, tx, b.InstallID, b.App, now)
		}
		if err != nil {
			return fmt.Errorf("%s batch: %w", b.Type, err)
		}
	}
	return tx.Commit()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// installTables are the tables holding an install id's rows.
var installTables = []string{
	"telemetry_server_reports", "telemetry_app_installs", "telemetry_app_events",
	"telemetry_error_occurrences", "telemetry_web_vitals",
}

func forget(ctx context.Context, tx execer, installID string) error {
	for _, t := range installTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE install_id = $1`, installID); err != nil {
			return err
		}
	}
	return dropEmptyGroups(ctx, tx)
}

// dropEmptyGroups removes error groups no occurrence refers to any more.
func dropEmptyGroups(ctx context.Context, tx execer) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM telemetry_error_groups g
        WHERE NOT EXISTS (SELECT 1 FROM telemetry_error_occurrences o WHERE o.fingerprint = g.fingerprint)`)
	return err
}

func putServer(ctx context.Context, tx execer, installID string, r *ServerReport, now time.Time) error {
	apps, err := json.Marshal(r.Apps)
	if err != nil {
		return err
	}
	features, err := json.Marshal(r.Features)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO telemetry_server_reports (install_id, day, version, os, arch, install_kind,
            library_size, libraries, users, machines, apps, features, received_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
        ON CONFLICT (install_id, day) DO UPDATE SET
            version = EXCLUDED.version, os = EXCLUDED.os, arch = EXCLUDED.arch,
            install_kind = EXCLUDED.install_kind, library_size = EXCLUDED.library_size,
            libraries = EXCLUDED.libraries, users = EXCLUDED.users, machines = EXCLUDED.machines,
            apps = EXCLUDED.apps, features = EXCLUDED.features, received_at = EXCLUDED.received_at`,
		installID, r.Day, r.Version, r.OS, r.Arch, r.InstallKind, r.LibrarySize, r.Libraries,
		r.Users, r.Machines, apps, features, now); err != nil {
		return err
	}
	// The resent day replaces the day's server errors too.
	if _, err := tx.ExecContext(ctx, `
        DELETE FROM telemetry_error_occurrences o USING telemetry_error_groups g
        WHERE o.fingerprint = g.fingerprint AND g.source = 'server' AND o.install_id = $1 AND o.day = $2`,
		installID, r.Day); err != nil {
		return err
	}
	for _, e := range r.Errors {
		if e.Count == 0 {
			continue
		}
		if err := putError(ctx, tx, "server", "server", e.Message, "", installID, r.Day, r.Version, e.Count, now, now); err != nil {
			return err
		}
	}
	return nil
}

func putApp(ctx context.Context, tx execer, installID string, a *AppBatch, now time.Time) error {
	day := now.UTC().Truncate(24 * time.Hour)
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO telemetry_app_installs (install_id, day, app, platform, form_factor, app_version, os_version, locale)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        ON CONFLICT (install_id, day) DO UPDATE SET
            app = EXCLUDED.app, platform = EXCLUDED.platform, form_factor = EXCLUDED.form_factor,
            app_version = EXCLUDED.app_version, os_version = EXCLUDED.os_version, locale = EXCLUDED.locale`,
		installID, day, a.App, a.Platform, a.FormFactor, a.AppVersion, a.OSVersion, a.Locale); err != nil {
		return err
	}
	for _, e := range a.Events {
		p, err := json.Marshal(e.Payload)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO telemetry_app_events (install_id, app, app_version, kind, payload, ts)
            VALUES ($1,$2,$3,$4,$5,$6)`, installID, a.App, a.AppVersion, e.Kind, p, e.TS); err != nil {
			return err
		}
	}
	for _, e := range a.Errors {
		d := e.TS.UTC().Truncate(24 * time.Hour)
		if err := putError(ctx, tx, "app", a.App, e.Message, e.FirstFrame, installID, d, a.AppVersion, 1, e.TS, e.TS); err != nil {
			return err
		}
	}
	for _, v := range a.Vitals {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO telemetry_web_vitals (install_id, app, app_version, metric, value, route, received_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7)`, installID, a.App, a.AppVersion, v.Metric, v.Value, v.Route, now); err != nil {
			return err
		}
	}
	return nil
}

var reDigits = regexp.MustCompile(`[0-9]+`)

// Fingerprint groups errors: the source, the app, the scrubbed message
// with its numbers folded, and the first stack frame.
func Fingerprint(source, app, message, frame string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{source, app, reDigits.ReplaceAllString(message, "0"), frame}, "\x00")))
	return hex.EncodeToString(h[:16])
}

func putError(ctx context.Context, tx execer, source, app, message, frame, installID string, day time.Time,
	version string, count int64, first, last time.Time) error {
	fp := Fingerprint(source, app, message, frame)
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO telemetry_error_groups (fingerprint, source, app, message, first_frame, created_at)
        VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (fingerprint) DO NOTHING`,
		fp, source, app, message, frame, first); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
        INSERT INTO telemetry_error_occurrences (fingerprint, install_id, day, version, count, first_seen, last_seen)
        VALUES ($1,$2,$3,$4,$5,$6,$7)
        ON CONFLICT (fingerprint, install_id, day, version) DO UPDATE SET
            count = telemetry_error_occurrences.count + EXCLUDED.count,
            first_seen = LEAST(telemetry_error_occurrences.first_seen, EXCLUDED.first_seen),
            last_seen = GREATEST(telemetry_error_occurrences.last_seen, EXCLUDED.last_seen)`,
		fp, installID, day, version, count, first, last)
	return err
}

// Retention is how long each kind of row is kept.
type Retention struct {
	ServerReports time.Duration
	AppData       time.Duration // app installs, app events, error occurrences
	Vitals        time.Duration
}

// DefaultRetention is the documented retention.
var DefaultRetention = Retention{
	ServerReports: 400 * 24 * time.Hour,
	AppData:       90 * 24 * time.Hour,
	Vitals:        30 * 24 * time.Hour,
}

// SweepResult counts what one sweep removed.
type SweepResult struct {
	ServerReports, AppInstalls, AppEvents, ErrorOccurrences, ErrorGroups, Vitals int64
}

// Sweep deletes rows past their retention.
func (s *Store) Sweep(ctx context.Context, ret Retention, now time.Time) (SweepResult, error) {
	var res SweepResult
	day := func(d time.Duration) time.Time { return now.Add(-d).UTC().Truncate(24 * time.Hour) }
	steps := []struct {
		q   string
		arg time.Time
		n   *int64
	}{
		{`DELETE FROM telemetry_server_reports WHERE day < $1`, day(ret.ServerReports), &res.ServerReports},
		{`DELETE FROM telemetry_app_installs WHERE day < $1`, day(ret.AppData), &res.AppInstalls},
		{`DELETE FROM telemetry_app_events WHERE ts < $1`, now.Add(-ret.AppData), &res.AppEvents},
		{`DELETE FROM telemetry_error_occurrences WHERE day < $1`, day(ret.AppData), &res.ErrorOccurrences},
		{`DELETE FROM telemetry_web_vitals WHERE received_at < $1`, now.Add(-ret.Vitals), &res.Vitals},
		{`DELETE FROM telemetry_error_groups g WHERE created_at < $1 AND NOT EXISTS
            (SELECT 1 FROM telemetry_error_occurrences o WHERE o.fingerprint = g.fingerprint)`, now, &res.ErrorGroups},
	}
	for _, st := range steps {
		r, err := s.DB.ExecContext(ctx, st.q, st.arg)
		if err != nil {
			return res, err
		}
		*st.n, _ = r.RowsAffected()
	}
	return res, nil
}
