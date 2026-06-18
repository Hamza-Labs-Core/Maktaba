package watch

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	defaultHistoryLimit = 50
	maxHistoryLimit     = 200
)

// History implements GET /api/me/history (Story 29.2): the caller's
// watched videos, newest first, paginated and date-filterable.
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}

	limit, e := common.QueryInt(r, "limit", defaultHistoryLimit)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	offset, e := common.QueryInt(r, "offset", 0)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	limit = clampLimit(limit, defaultHistoryLimit, maxHistoryLimit)
	if offset < 0 {
		offset = 0
	}

	f := historyFilter{limit: limit, offset: offset}
	if from, ok, e := parseDateParam(r, "from"); e != nil {
		httperror.Write(w, r, e)
		return
	} else if ok {
		f.from = &from
	}
	if to, ok, e := parseDateParam(r, "to"); e != nil {
		httperror.Write(w, r, e)
		return
	} else if ok {
		f.to = &to
	}

	items, err := h.repo().history(r.Context(), p.UserID, f)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load history"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"items": items, "limit": limit, "offset": offset,
	})
}

// VideoHistory implements GET /api/me/history/{video_id}.
func (h *Handler) VideoHistory(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	videoID := chi.URLParam(r, "video_id")
	if _, err := uuid.Parse(videoID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("video_id must be a uuid"))
		return
	}
	vh, err := h.repo().videoHistory(r.Context(), p.UserID, videoID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load video history"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, vh)
}

// DeleteHistory implements DELETE /api/me/history/{video_id}. Owner-scoped;
// also clears the resume point so the Continue-Watching rail drops it.
func (h *Handler) DeleteHistory(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	videoID := chi.URLParam(r, "video_id")
	if _, err := uuid.Parse(videoID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("video_id must be a uuid"))
		return
	}
	if err := h.repo().deleteHistory(r.Context(), p.UserID, videoID); err != nil {
		httperror.Write(w, r, httperror.Internal("delete history"))
		return
	}
	common.WriteNoContent(w)
}

// clampLimit bounds a requested page size.
func clampLimit(v, def, hi int) int {
	if v <= 0 {
		return def
	}
	if v > hi {
		return hi
	}
	return v
}

// parseDateParam parses an RFC3339 timestamp or a bare YYYY-MM-DD date
// from a query param. Returns ok=false when the param is absent.
func parseDateParam(r *http.Request, key string) (time.Time, bool, *httperror.Error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return time.Time{}, false, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), true, nil
	}
	return time.Time{}, false, httperror.InvalidQuery(key + " must be RFC3339 or YYYY-MM-DD")
}
