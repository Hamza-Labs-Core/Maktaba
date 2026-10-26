package analytics

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// exportRow is one play (watch_sessions) or one viewing of a channel (Live
// TV in an app, through the tuner or on the remote TV) in export form.
type exportRow struct {
	Kind            string // "play" | "viewing"
	ID              string
	UserID          string
	VideoID         string
	ChannelID       string
	Surface         string
	StartedAt       string
	EndedAt         string
	DurationSec     int
	PercentComplete float64
	State           string // a play's state; a viewing's end reason, or "open"
	ClientApp       string
	ClientVersion   string
	DeviceType      string
	Platform        string
	Quality         string
}

// exportHeader is the CSV header / JSON field order.
var exportHeader = []string{
	"kind", "session_id", "user_id", "video_id", "channel_id", "surface",
	"started_at", "ended_at", "duration_sec", "percent_complete", "state",
	"client_app", "client_version", "device_type", "platform", "quality",
}

// record renders the row as an ordered string slice (CSV record / JSON
// stays aligned with exportHeader). Pure — unit-tested for RFC-4180
// round-tripping.
func (e exportRow) record() []string {
	return []string{
		e.Kind, e.ID, e.UserID, e.VideoID, e.ChannelID, e.Surface,
		e.StartedAt, e.EndedAt,
		strconv.Itoa(e.DurationSec),
		strconv.FormatFloat(e.PercentComplete, 'f', 2, 64),
		e.State, e.ClientApp, e.ClientVersion, e.DeviceType, e.Platform, e.Quality,
	}
}

// Export implements GET /api/admin/analytics/export?format=csv|json plus the
// range and filters (Story 29.6; Epic 44 adds the viewings). Admin only;
// streams the plays, then the viewings, rather than buffering them.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil || !h.gateAdvanced(w, r) {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		httperror.Write(w, r, httperror.InvalidQuery("format must be csv or json"))
		return
	}
	now := h.now()
	f, e := ParseFilters(r, now)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}

	rp := h.repo()
	plays, err := rp.exportPlays(r.Context(), f)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("open export").Wrap(err))
		return
	}
	defer plays.Close()

	// The viewings open once the plays are read and closed: a SQLite home
	// may hold a single connection.
	next := func(yield func(exportRow) bool) {
		for plays.Next() {
			rec, err := scanExportPlay(plays)
			if err != nil || !yield(rec) {
				return // header already flushed; truncate rather than 500 mid-stream
			}
		}
		_ = plays.Close()
		views, err := rp.exportViewings(r.Context(), f)
		if err != nil {
			return
		}
		defer views.Close()
		for views.Next() {
			rec, err := scanExportViewing(views)
			if err != nil || !yield(rec) {
				return
			}
		}
	}

	filename := "maktaba-analytics-" + f.Range.Label + "-" + now.Format("2006-01-02")
	if format == "json" {
		streamJSON(w, next, filename)
		return
	}
	streamCSV(w, next, filename)
}

func streamCSV(w http.ResponseWriter, rows func(func(exportRow) bool), filename string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write(exportHeader)
	rows(func(rec exportRow) bool {
		_ = cw.Write(rec.record())
		return true
	})
	cw.Flush()
}

func streamJSON(w http.ResponseWriter, rows func(func(exportRow) bool), filename string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.json"`)
	enc := json.NewEncoder(w)
	_, _ = w.Write([]byte("["))
	first := true
	rows(func(rec exportRow) bool {
		if !first {
			_, _ = w.Write([]byte(","))
		}
		first = false
		obj := map[string]any{}
		vals := rec.record()
		for i, k := range exportHeader {
			obj[k] = vals[i]
		}
		_ = enc.Encode(obj)
		return true
	})
	_, _ = w.Write([]byte("]"))
}

// exportPlays opens the plays' cursor for the filters.
func (r *repo) exportPlays(ctx context.Context, f Filters) (*sql.Rows, error) {
	b := r.newQ()
	q := `SELECT ws.id, CAST(ws.user_id AS TEXT), ws.video_id, ws.started_at, ws.ended_at, ws.duration_sec,
	             ws.percent_complete, ws.state, ws.client_app, ws.client_version, ws.device_type,
	             ws.platform, ws.quality
	      FROM watch_sessions ws` + b.playWhere(f, true) + ` ORDER BY ` + r.ts("ws.started_at") + ` ASC`
	return r.db.QueryContext(ctx, q, b.args...)
}

// exportViewings opens the viewings' cursor for the filters.
func (r *repo) exportViewings(ctx context.Context, f Filters) (*sql.Rows, error) {
	b := r.newQ()
	q := `WITH ` + b.tvvCTE() + `
	      SELECT tvv.id, tvv.user_id, tvv.channel_id, tvv.surface, tvv.started_at, tvv.ended_at,
	             tvv.seconds, tvv.end_reason, tvv.client_app, tvv.client_version, tvv.device_type,
	             tvv.platform
	      FROM tvv` + b.viewWhere(f, true) + ` ORDER BY ` + r.ts("tvv.started_at") + ` ASC`
	return r.db.QueryContext(ctx, q, b.args...)
}

func scanExportPlay(rows *sql.Rows) (exportRow, error) {
	e := exportRow{Kind: "play", Surface: SurfaceVideo}
	var started, ended dbTime
	var app, ver, device, platform, quality nullString
	if err := rows.Scan(&e.ID, &e.UserID, &e.VideoID, &started, &ended, &e.DurationSec,
		&e.PercentComplete, &e.State, &app, &ver, &device, &platform, &quality); err != nil {
		return exportRow{}, err
	}
	e.StartedAt, e.EndedAt = started.String(), ended.String()
	e.ClientApp, e.ClientVersion, e.DeviceType, e.Platform, e.Quality = app.S, ver.S, device.S, platform.S, quality.S
	return e, nil
}

func scanExportViewing(rows *sql.Rows) (exportRow, error) {
	e := exportRow{Kind: "viewing"}
	var started, ended dbTime
	var user, reason, app, ver, device, platform nullString
	if err := rows.Scan(&e.ID, &user, &e.ChannelID, &e.Surface, &started, &ended, &e.DurationSec,
		&reason, &app, &ver, &device, &platform); err != nil {
		return exportRow{}, err
	}
	e.UserID = user.S
	e.StartedAt, e.EndedAt = started.String(), ended.String()
	e.State = reason.S
	if e.State == "" {
		if ended.Valid {
			e.State = "ended"
		} else {
			e.State = "open"
		}
	}
	e.ClientApp, e.ClientVersion, e.DeviceType, e.Platform = app.S, ver.S, device.S, platform.S
	return e, nil
}
