package analytics

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// exportRow is a single watch_sessions record in export form.
type exportRow struct {
	ID              string
	UserID          string
	VideoID         string
	StartedAt       string
	EndedAt         string
	DurationSec     int
	PercentComplete float64
	State           string
	DeviceType      string
	Platform        string
	Quality         string
}

// exportHeader is the CSV header / JSON field order.
var exportHeader = []string{
	"session_id", "user_id", "video_id", "started_at", "ended_at",
	"duration_sec", "percent_complete", "state", "device_type", "platform", "quality",
}

// record renders the row as an ordered string slice (CSV record / JSON
// stays aligned with exportHeader). Pure — unit-tested for RFC-4180
// round-tripping.
func (e exportRow) record() []string {
	return []string{
		e.ID, e.UserID, e.VideoID, e.StartedAt, e.EndedAt,
		strconv.Itoa(e.DurationSec),
		strconv.FormatFloat(e.PercentComplete, 'f', 2, 64),
		e.State, e.DeviceType, e.Platform, e.Quality,
	}
}

// Export implements GET /api/admin/analytics/export?format=csv|json&range=
// (Story 29.6). Admin only; streams rather than buffering the result set.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
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
	rng := ParseRange(r.URL.Query().Get("range"), now)

	rows, err := h.repo().exportRows(r.Context(), rng)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("open export"))
		return
	}
	defer rows.Close()

	filename := "maktaba-analytics-" + rng.Label + "-" + now.Format("2006-01-02")
	if format == "json" {
		h.streamJSON(w, rows, filename)
		return
	}
	h.streamCSV(w, rows, filename)
}

func (h *Handler) streamCSV(w http.ResponseWriter, rows *sql.Rows, filename string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write(exportHeader)
	for rows.Next() {
		rec, err := scanExportRow(rows)
		if err != nil {
			break // header already flushed; truncate rather than 500 mid-stream
		}
		_ = cw.Write(rec.record())
	}
	cw.Flush()
}

func (h *Handler) streamJSON(w http.ResponseWriter, rows *sql.Rows, filename string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.json"`)
	enc := json.NewEncoder(w)
	_, _ = w.Write([]byte("["))
	first := true
	for rows.Next() {
		rec, err := scanExportRow(rows)
		if err != nil {
			break
		}
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
	}
	_, _ = w.Write([]byte("]"))
}

// exportRows opens the streaming cursor for the range.
func (r *repo) exportRows(ctx context.Context, rng Range) (*sql.Rows, error) {
	where, args := r.startedPredicate(rng)
	q := `SELECT id, user_id, video_id, started_at, ended_at, duration_sec,
	             percent_complete, state, device_type, platform, quality
	      FROM watch_sessions ws` + where + ` ORDER BY started_at ASC`
	return r.db.QueryContext(ctx, q, args...)
}

func scanExportRow(rows *sql.Rows) (exportRow, error) {
	var e exportRow
	var started time.Time
	var ended sql.NullTime
	var device, platform, quality sql.NullString
	if err := rows.Scan(&e.ID, &e.UserID, &e.VideoID, &started, &ended,
		&e.DurationSec, &e.PercentComplete, &e.State, &device, &platform, &quality); err != nil {
		return exportRow{}, err
	}
	e.StartedAt = started.UTC().Format(time.RFC3339)
	if ended.Valid {
		e.EndedAt = ended.Time.UTC().Format(time.RFC3339)
	}
	e.DeviceType = device.String
	e.Platform = platform.String
	e.Quality = quality.String
	return e, nil
}
