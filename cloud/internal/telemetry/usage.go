package telemetry

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// Usage is the operator's aggregate view of a window (GET /v1/admin/usage).
// Server-report figures use each home's latest report in the window.
type Usage struct {
	Days  int       `json:"days"`
	Since time.Time `json:"since"`

	Installs struct {
		Homes       int           `json:"homes"`
		AppInstalls int           `json:"app_installs"`
		ByApp       []AppInstalls `json:"by_app"`
	} `json:"installs"`

	ServerVersions  []ServerVersion  `json:"server_versions"`
	AppVersions     []AppVersion     `json:"app_versions"`
	ServerPlatforms []ServerPlatform `json:"server_platforms"`
	InstallKinds    []InstallKind    `json:"install_kinds"`
	AppPlatforms    []AppPlatform    `json:"app_platforms"`
	AppsInUse       []AppInUse       `json:"apps_in_use"`
	Features        []FeatureShare   `json:"features"`
	FeatureUse      []FeatureUse     `json:"feature_use"`
	Errors          []ErrorGroup     `json:"errors"`
	Vitals          []VitalP75       `json:"vitals"`
}

type AppInstalls struct {
	App      string `json:"app"`
	Installs int    `json:"installs"`
}

type ServerVersion struct {
	Version string `json:"version"`
	Homes   int    `json:"homes"`
}

type AppVersion struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	Installs int    `json:"installs"`
}

type ServerPlatform struct {
	OS    string `json:"os"`
	Arch  string `json:"arch"`
	Homes int    `json:"homes"`
}

type InstallKind struct {
	InstallKind string `json:"install_kind"`
	Homes       int    `json:"homes"`
}

type AppPlatform struct {
	App        string `json:"app"`
	Platform   string `json:"platform"`
	FormFactor string `json:"form_factor"`
	Installs   int    `json:"installs"`
}

// AppInUse: Homes is how many homes' reports list the app on that
// platform; Installs is how many opted-in installs of it sent batches.
type AppInUse struct {
	App      string `json:"app"`
	Platform string `json:"platform"`
	Homes    int    `json:"homes"`
	Installs int    `json:"installs"`
}

type FeatureShare struct {
	Feature string  `json:"feature"`
	Homes   int     `json:"homes"`
	Share   float64 `json:"share"`
}

type FeatureUse struct {
	App      string `json:"app"`
	Feature  string `json:"feature"`
	Total    int64  `json:"total"`
	Installs int    `json:"installs"`
}

type ErrorGroup struct {
	Fingerprint string    `json:"fingerprint"`
	Source      string    `json:"source"`
	App         string    `json:"app"`
	Message     string    `json:"message"`
	FirstFrame  string    `json:"first_frame,omitempty"`
	Count       int64     `json:"count"`
	Installs    int       `json:"installs"`
	Versions    []string  `json:"versions"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

type VitalP75 struct {
	Metric  string  `json:"metric"`
	P75     float64 `json:"p75"`
	Samples int     `json:"samples"`
}

// MaxUsageDays bounds the window.
const MaxUsageDays = 400

// QueryUsage builds the aggregate view of the last `days` days.
func QueryUsage(ctx context.Context, db *sql.DB, now time.Time, days int) (*Usage, error) {
	if days < 1 {
		days = 30
	}
	if days > MaxUsageDays {
		days = MaxUsageDays
	}
	since := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	u := &Usage{Days: days, Since: since}
	q := &querier{ctx: ctx, db: db}

	// each home's latest report in the window
	const latest = `WITH latest AS (
        SELECT DISTINCT ON (install_id) * FROM telemetry_server_reports
        WHERE day >= $1 ORDER BY install_id, day DESC)`

	q.row(`SELECT count(DISTINCT install_id) FROM telemetry_server_reports WHERE day >= $1`,
		[]any{since}, &u.Installs.Homes)
	q.row(`SELECT count(DISTINCT install_id) FROM telemetry_app_installs WHERE day >= $1`,
		[]any{since}, &u.Installs.AppInstalls)

	u.Installs.ByApp = []AppInstalls{}
	q.rows(`SELECT app, count(DISTINCT install_id) FROM telemetry_app_installs WHERE day >= $1
        GROUP BY app ORDER BY 2 DESC, 1`, []any{since}, func(s scanner) error {
		var v AppInstalls
		err := s.Scan(&v.App, &v.Installs)
		u.Installs.ByApp = append(u.Installs.ByApp, v)
		return err
	})

	u.ServerVersions = []ServerVersion{}
	q.rows(latest+` SELECT version, count(*) FROM latest GROUP BY version ORDER BY 2 DESC, 1 DESC LIMIT 100`,
		[]any{since}, func(s scanner) error {
			var v ServerVersion
			err := s.Scan(&v.Version, &v.Homes)
			u.ServerVersions = append(u.ServerVersions, v)
			return err
		})

	// each app install's latest day in the window
	const latestApp = `WITH latest AS (
        SELECT DISTINCT ON (install_id) * FROM telemetry_app_installs
        WHERE day >= $1 ORDER BY install_id, day DESC)`
	u.AppVersions = []AppVersion{}
	q.rows(latestApp+` SELECT app, app_version, count(*) FROM latest GROUP BY app, app_version
        ORDER BY 1, 3 DESC, 2 DESC LIMIT 200`, []any{since}, func(s scanner) error {
		var v AppVersion
		err := s.Scan(&v.App, &v.Version, &v.Installs)
		u.AppVersions = append(u.AppVersions, v)
		return err
	})

	u.ServerPlatforms = []ServerPlatform{}
	q.rows(latest+` SELECT os, arch, count(*) FROM latest GROUP BY os, arch ORDER BY 3 DESC, 1, 2`,
		[]any{since}, func(s scanner) error {
			var v ServerPlatform
			err := s.Scan(&v.OS, &v.Arch, &v.Homes)
			u.ServerPlatforms = append(u.ServerPlatforms, v)
			return err
		})

	u.InstallKinds = []InstallKind{}
	q.rows(latest+` SELECT install_kind, count(*) FROM latest GROUP BY install_kind ORDER BY 2 DESC, 1`,
		[]any{since}, func(s scanner) error {
			var v InstallKind
			err := s.Scan(&v.InstallKind, &v.Homes)
			u.InstallKinds = append(u.InstallKinds, v)
			return err
		})

	u.AppPlatforms = []AppPlatform{}
	q.rows(latestApp+` SELECT app, platform, form_factor, count(*) FROM latest
        GROUP BY app, platform, form_factor ORDER BY 4 DESC, 1, 2, 3 LIMIT 200`, []any{since}, func(s scanner) error {
		var v AppPlatform
		err := s.Scan(&v.App, &v.Platform, &v.FormFactor, &v.Installs)
		u.AppPlatforms = append(u.AppPlatforms, v)
		return err
	})

	u.AppsInUse = []AppInUse{}
	q.rows(latest+`, reported AS (
            SELECT a->>'app' AS app, a->>'platform' AS platform, count(DISTINCT l.install_id) AS homes
            FROM latest l, jsonb_array_elements(l.apps) a
            WHERE a->>'people' <> '0' OR a->>'requests' <> '0'
            GROUP BY 1, 2),
        sent AS (
            SELECT app, platform, count(DISTINCT install_id) AS installs
            FROM telemetry_app_installs WHERE day >= $1 GROUP BY 1, 2)
        SELECT COALESCE(r.app, s.app), COALESCE(r.platform, s.platform),
               COALESCE(r.homes, 0), COALESCE(s.installs, 0)
        FROM reported r FULL JOIN sent s ON r.app = s.app AND r.platform = s.platform
        ORDER BY 3 DESC, 4 DESC, 1, 2 LIMIT 200`, []any{since}, func(s scanner) error {
		var v AppInUse
		err := s.Scan(&v.App, &v.Platform, &v.Homes, &v.Installs)
		u.AppsInUse = append(u.AppsInUse, v)
		return err
	})

	u.Features = []FeatureShare{}
	q.rows(latest+` SELECT f.key, count(*) FROM latest l, jsonb_each(l.features) f
        WHERE f.value = 'true'::jsonb OR (jsonb_typeof(f.value) = 'string' AND f.value <> '"0"')
        GROUP BY f.key ORDER BY 2 DESC, 1`, []any{since}, func(s scanner) error {
		var v FeatureShare
		err := s.Scan(&v.Feature, &v.Homes)
		if u.Installs.Homes > 0 {
			v.Share = float64(v.Homes) / float64(u.Installs.Homes)
		}
		u.Features = append(u.Features, v)
		return err
	})

	u.FeatureUse = []FeatureUse{}
	q.rows(`SELECT app, payload->>'feature', COALESCE(sum((payload->>'count')::bigint), 0), count(DISTINCT install_id)
        FROM telemetry_app_events
        WHERE kind = 'feature.used' AND ts >= $1 AND payload ? 'feature'
        GROUP BY 1, 2 ORDER BY 3 DESC, 1, 2 LIMIT 200`, []any{since}, func(s scanner) error {
		var v FeatureUse
		err := s.Scan(&v.App, &v.Feature, &v.Total, &v.Installs)
		u.FeatureUse = append(u.FeatureUse, v)
		return err
	})

	u.Errors = []ErrorGroup{}
	q.rows(`SELECT g.fingerprint, g.source, g.app, g.message, g.first_frame,
            sum(o.count), count(DISTINCT o.install_id),
            array_agg(DISTINCT o.version ORDER BY o.version DESC), min(o.first_seen), max(o.last_seen)
        FROM telemetry_error_occurrences o JOIN telemetry_error_groups g ON g.fingerprint = o.fingerprint
        WHERE o.day >= $1
        GROUP BY g.fingerprint ORDER BY 6 DESC, 10 DESC LIMIT 50`, []any{since}, func(s scanner) error {
		var v ErrorGroup
		var versions pq.StringArray
		err := s.Scan(&v.Fingerprint, &v.Source, &v.App, &v.Message, &v.FirstFrame,
			&v.Count, &v.Installs, &versions, &v.FirstSeen, &v.LastSeen)
		v.Versions = []string(versions)
		if v.Versions == nil {
			v.Versions = []string{}
		}
		u.Errors = append(u.Errors, v)
		return err
	})

	u.Vitals = []VitalP75{}
	q.rows(`SELECT metric, percentile_cont(0.75) WITHIN GROUP (ORDER BY value), count(*)
        FROM telemetry_web_vitals WHERE received_at >= $1 GROUP BY metric ORDER BY metric`,
		[]any{since}, func(s scanner) error {
			var v VitalP75
			err := s.Scan(&v.Metric, &v.P75, &v.Samples)
			u.Vitals = append(u.Vitals, v)
			return err
		})

	if q.err != nil {
		return nil, q.err
	}
	return u, nil
}

type scanner interface{ Scan(dest ...any) error }

// querier runs queries until the first error and keeps it.
type querier struct {
	ctx context.Context
	db  *sql.DB
	err error
}

func (q *querier) row(query string, args []any, dest ...any) {
	if q.err != nil {
		return
	}
	q.err = q.db.QueryRowContext(q.ctx, query, args...).Scan(dest...)
}

func (q *querier) rows(query string, args []any, each func(scanner) error) {
	if q.err != nil {
		return
	}
	rows, err := q.db.QueryContext(q.ctx, query, args...)
	if err != nil {
		q.err = err
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := each(rows); err != nil {
			q.err = err
			return
		}
	}
	q.err = rows.Err()
}
