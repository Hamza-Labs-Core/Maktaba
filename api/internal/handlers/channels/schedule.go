package channels

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// MountSchedule wires the schedule read + regenerate-trigger endpoints
// (Story 27.2's API surface). The generation itself runs in the Python
// pipeline scheduler; the API exposes the read path over its output
// (channel_programs) and a regenerate trigger that marks the channel
// stale for the next scheduler pass — never a synchronous transcode.
func (h *Handler) MountSchedule(r chi.Router) {
	r.Get("/api/channels/{id}/schedule", h.Schedule)
	r.Post("/api/channels/{id}/regenerate", h.Regenerate)
}

// ScheduleBlock is one channel_programs row over the wire.
type ScheduleBlock struct {
	Seq            int64     `json:"seq"`
	Kind           string    `json:"kind"`
	VideoID        *string   `json:"video_id,omitempty"`
	StartAt        time.Time `json:"start_at"`
	EndAt          time.Time `json:"end_at"`
	SourceOffset   int       `json:"source_offset_ms"`
	SourceDuration int       `json:"source_duration_ms"`
	Title          string    `json:"title,omitempty"`
}

// Schedule returns the generated program blocks for a channel in the
// requested window (default: now → now+24h).
func (h *Handler) Schedule(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	c, err := h.repo().get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("channel "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load channel"))
		return
	}
	if !h.canRead(p, c.LibraryID) {
		httperror.Write(w, r, httperror.Forbidden("", "no access to this channel"))
		return
	}
	start := h.now()
	if s := r.URL.Query().Get("start"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			httperror.Write(w, r, httperror.InvalidQuery("start must be RFC3339"))
			return
		}
		start = t
	}
	end := start.Add(24 * time.Hour)
	if s := r.URL.Query().Get("end"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			httperror.Write(w, r, httperror.InvalidQuery("end must be RFC3339"))
			return
		}
		end = t
	}
	if !end.After(start) {
		httperror.Write(w, r, httperror.InvalidQuery("end must be after start"))
		return
	}
	blocks, err := h.loadSchedule(r, id, start, end)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read schedule"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"channel_id": id,
		"start":      start.UTC().Format(time.RFC3339),
		"end":        end.UTC().Format(time.RFC3339),
		"blocks":     blocks,
	})
}

// Regenerate marks the channel's schedule stale so the next pipeline
// scheduler pass rebuilds the future tail (D5). Admin-only; returns 202
// Accepted because generation is asynchronous.
func (h *Handler) Regenerate(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.repo().get(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("channel "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load channel"))
		return
	}
	h.markStale(r.Context(), id)
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{
		"channel_id": id,
		"status":     "regeneration-queued",
	})
}

func (h *Handler) loadSchedule(r *http.Request, channelID string, start, end time.Time) ([]ScheduleBlock, error) {
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT seq, kind, video_id, start_at, end_at, source_offset, source_duration, title_snapshot
		FROM channel_programs
		WHERE channel_id = $1 AND start_at < $3 AND end_at > $2
		ORDER BY start_at ASC
	`, channelID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScheduleBlock{}
	for rows.Next() {
		var b ScheduleBlock
		var vid sql.NullString
		var snap []byte
		if err := rows.Scan(&b.Seq, &b.Kind, &vid, &b.StartAt, &b.EndAt,
			&b.SourceOffset, &b.SourceDuration, &snap); err != nil {
			continue
		}
		if vid.Valid {
			b.VideoID = &vid.String
		}
		b.Title = titleFromSnapshot(snap)
		out = append(out, b)
	}
	return out, rows.Err()
}
