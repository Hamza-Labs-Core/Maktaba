package analytics

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The reads share one shape: the plays and the viewings that pass the
// filters, as one set of rows (the ev CTE), aggregated per question. ev has
// a row per play (surface 'video') and per viewing of a channel (surface
// 'app', 'tuner' or 'remote'):
//
//	surface, user_id (text), video_id, channel_id, client_app, client_version,
//	platform, device_type, started_at, last_at, seconds, completed (0|1)
//
// Viewings come from tvv, which reads tv_watch_sessions (app and tuner) and
// tv_remote_sessions (the remote TV, read as it is: no copy) as one shape.
//
// SQLite keeps timestamps as text in more than one format (datetime('now'),
// strftime's ISO form, the Go driver's own), so on SQLite every timestamp is
// compared through datetime() (see ts) and bound as 'YYYY-MM-DD HH:MM:SS' UTC.

// sqlb builds one statement's text and its arguments; a value used in more
// than one place (the range, in both halves of ev) binds once.
type sqlb struct {
	r     *repo
	args  []any
	named map[string]string
}

func (r *repo) newQ() *sqlb { return &sqlb{r: r, named: map[string]string{}} }

// arg binds v and returns its placeholder.
func (b *sqlb) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

// once binds v under key the first time and reuses the placeholder after.
func (b *sqlb) once(key string, v any) string {
	if p, ok := b.named[key]; ok {
		return p
	}
	p := b.arg(v)
	b.named[key] = p
	return p
}

// ts is a timestamp column as the dialect compares it. On SQLite, text
// datetime() cannot read (the Go driver's default, time.Time's String:
// "2026-10-26 14:50:00 +0000 UTC") is read by its first 19 characters, as
// UTC, which is what the writers bind.
func (r *repo) ts(col string) string {
	if r.isSQLite() {
		return "COALESCE(datetime(" + col + "), datetime(substr(" + col + ", 1, 19)))"
	}
	return col
}

// tArg is a time bound as the dialect binds it.
func (r *repo) tArg(t time.Time) any {
	if r.isSQLite() {
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	return t
}

// day is a DATE column as the dialect compares it (SQLite: 'YYYY-MM-DD').
func (r *repo) day(col string) string {
	if r.isSQLite() {
		return "date(" + col + ")"
	}
	return col
}

// dayText is a DATE column as 'YYYY-MM-DD' text.
func (r *repo) dayText(col string) string {
	if r.isSQLite() {
		return "date(" + col + ")"
	}
	return "to_char(" + col + ", 'YYYY-MM-DD')"
}

// appExpr is a client_app column with the empty and the missing as "unknown".
func appExpr(col string) string { return "COALESCE(NULLIF(" + col + ",''),'unknown')" }

// remoteSeconds is a remote TV session's watched time (its last heartbeat,
// or its end, less its start).
func (r *repo) remoteSeconds() string {
	if r.isSQLite() {
		return "CAST(ROUND(MAX((julianday(" + r.ts("COALESCE(rs.ended_at, rs.last_beat_at)") + ") - julianday(" +
			r.ts("rs.started_at") + ")) * 86400, 0)) AS INTEGER)"
	}
	return "CAST(GREATEST(EXTRACT(EPOCH FROM (COALESCE(rs.ended_at, rs.last_beat_at) - rs.started_at)), 0) AS BIGINT)"
}

// tvvCTE is every viewing of a channel, app and tuner rows and the remote
// TV's sessions in one shape. tv_watch_sessions rows marked 'remote' are left
// out: the remote TV is read from its own table.
func (b *sqlb) tvvCTE() string {
	return `tvv AS (
		SELECT tw.id AS id, tw.channel_id AS channel_id, CAST(tw.user_id AS TEXT) AS user_id,
		       tw.surface AS surface, tw.client_app AS client_app, tw.client_version AS client_version,
		       tw.platform AS platform, tw.device_type AS device_type, tw.started_at AS started_at,
		       tw.last_seen_at AS last_seen_at, tw.ended_at AS ended_at, tw.seconds AS seconds,
		       tw.end_reason AS end_reason
		FROM tv_watch_sessions tw WHERE tw.surface <> 'remote'
		UNION ALL
		SELECT rs.id, rs.channel_id, rs.user_id, 'remote', NULL, NULL, NULL, NULLIF(rs.device,''),
		       rs.started_at, rs.last_beat_at, rs.ended_at, ` + b.r.remoteSeconds() + `, rs.end_reason
		FROM tv_remote_sessions rs)`
}

// rangeWhere adds the range on a started_at column.
func (b *sqlb) rangeWhere(f Filters, col string, conds []string) []string {
	if f.Range.HasLowerBound() {
		conds = append(conds, b.r.ts(col)+" >= "+b.once("start", b.r.tArg(f.Range.Start)))
	}
	if f.Range.HasUpperBound() {
		conds = append(conds, b.r.ts(col)+" < "+b.once("end", b.r.tArg(f.Range.End)))
	}
	return conds
}

// playWhere is the WHERE of watch_sessions ws for the filters (withRange
// false: the range is left out, as for who is watching now).
func (b *sqlb) playWhere(f Filters, withRange bool) string {
	if !f.wantPlays() {
		return " WHERE 1=0"
	}
	conds := []string{}
	if withRange {
		conds = b.rangeWhere(f, "ws.started_at", conds)
	}
	if f.Library != "" {
		conds = append(conds, "ws.video_id IN (SELECT id FROM videos WHERE library_id = "+b.once("library", f.Library)+")")
	}
	if f.User != "" {
		conds = append(conds, "CAST(ws.user_id AS TEXT) = "+b.once("user", f.User))
	}
	if f.ClientApp != "" {
		conds = append(conds, appExpr("ws.client_app")+" = "+b.once("app", f.ClientApp))
	}
	return where(conds)
}

// viewWhere is the WHERE of tvv for the filters.
func (b *sqlb) viewWhere(f Filters, withRange bool) string {
	if !f.wantViewings() {
		return " WHERE 1=0"
	}
	conds := []string{}
	if withRange {
		conds = b.rangeWhere(f, "tvv.started_at", conds)
	}
	if f.Surface != "" {
		conds = append(conds, "tvv.surface = "+b.once("surface", f.Surface))
	}
	if f.User != "" {
		conds = append(conds, "tvv.user_id = "+b.once("user", f.User))
	}
	if f.ClientApp != "" {
		conds = append(conds, appExpr("tvv.client_app")+" = "+b.once("app", f.ClientApp))
	}
	return where(conds)
}

func where(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

// with is the WITH clause holding tvv and ev for the filters.
func (b *sqlb) with(f Filters) string {
	return `WITH ` + b.tvvCTE() + `,
	ev AS (
		SELECT 'video' AS surface, CAST(ws.user_id AS TEXT) AS user_id, ws.video_id AS video_id,
		       NULL AS channel_id, ws.client_app AS client_app, ws.client_version AS client_version,
		       ws.platform AS platform, ws.device_type AS device_type, ws.started_at AS started_at,
		       COALESCE(ws.ended_at, ws.last_heartbeat) AS last_at, ws.duration_sec AS seconds,
		       CASE WHEN ws.state = 'completed' THEN 1 ELSE 0 END AS completed
		FROM watch_sessions ws` + b.playWhere(f, true) + `
		UNION ALL
		SELECT tvv.surface, tvv.user_id, NULL, tvv.channel_id, tvv.client_app, tvv.client_version,
		       tvv.platform, tvv.device_type, tvv.started_at, COALESCE(tvv.ended_at, tvv.last_seen_at),
		       tvv.seconds, 0
		FROM tvv` + b.viewWhere(f, true) + `)
	`
}

// dayBounds binds the range as days for the daily tables (day >= from AND
// day <= to), on a DATE column.
func (b *sqlb) dayBounds(f Filters, col string, conds []string) []string {
	if f.Range.HasLowerBound() {
		conds = append(conds, b.r.day(col)+" >= "+b.once("dayFrom", f.Range.Start.UTC().Format("2006-01-02")))
	}
	if f.Range.HasUpperBound() {
		last := f.Range.End.Add(-time.Nanosecond).UTC().Format("2006-01-02")
		conds = append(conds, b.r.day(col)+" <= "+b.once("dayTo", last))
	}
	return conds
}

// dbTime scans a timestamp from either dialect: Postgres hands a time.Time,
// SQLite text in whichever format the writer used.
type dbTime struct {
	T     time.Time
	Valid bool
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
}

func (d *dbTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*d = dbTime{}
		return nil
	case time.Time:
		*d = dbTime{T: x.UTC(), Valid: true}
		return nil
	case []byte:
		return d.parse(string(x))
	case string:
		return d.parse(x)
	}
	return fmt.Errorf("analytics: cannot scan %T as a time", v)
}

func (d *dbTime) parse(s string) error {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			*d = dbTime{T: t.UTC(), Valid: true}
			return nil
		}
	}
	return fmt.Errorf("analytics: unreadable time %q", s)
}

// String is the time as RFC 3339, or "" when NULL.
func (d dbTime) String() string {
	if !d.Valid {
		return ""
	}
	return d.T.Format(time.RFC3339)
}

// nullString scans a nullable text (or uuid) column to "".
type nullString struct{ S string }

func (n *nullString) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		n.S = ""
	case []byte:
		n.S = string(x)
	case string:
		n.S = x
	default:
		n.S = fmt.Sprint(x)
	}
	return nil
}
