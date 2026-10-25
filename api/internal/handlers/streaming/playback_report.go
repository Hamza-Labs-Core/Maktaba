package streaming

// POST /api/stream/sessions/{id}/errors — a player's report of a playback
// failure after the session opened (a decode error, a playlist or segment
// that answered 404, a direct file that stalled). The browser is the only
// place these show, so the API writes them to its log as `playback_failed`:
// server.log then holds the reason next to the session's open and cleanup.
//
// Nothing is stored and the session is not looked up (it may already be
// cleaned up, the very failure being reported): the report is logged with the
// signed-in user, bounded in size and rate, and URLs lose their query (the
// signature) before they are written.

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// PlaybackReport is the body a player posts.
type PlaybackReport struct {
	VideoID string `json:"video_id"`
	// Mode is the session's mode (direct, remux, transcode).
	Mode string `json:"mode,omitempty"`
	// Engine plays it: hls.js, native HLS, or the element on a direct file.
	Engine string `json:"engine,omitempty"`
	// Outcome is what the player did: fatal (the error screen), reopen (it
	// opened the video again), stalled.
	Outcome string `json:"outcome,omitempty"`
	// Code is the player's own name for the error (an hls.js details string,
	// a MediaError code).
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// HTTPStatus of the failing load, when there was one.
	HTTPStatus  int     `json:"http_status,omitempty"`
	URL         string  `json:"url,omitempty"`
	PositionSec float64 `json:"position_sec,omitempty"`
}

// reportFieldMax bounds every string written to the log.
const reportFieldMax = 300

// reportInterval: at most one report per session this often (a player in a
// retry loop writes one line, not hundreds).
const reportInterval = time.Second

// PostPlaybackError logs a player's playback failure as `playback_failed`.
func (h *Handler) PostPlaybackError(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	sessionID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(sessionID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var rep PlaybackReport
	if e := common.ReadJSON(r, &rep, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !h.reports().Allow(sessionID, h.now()) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	level := slog.LevelWarn
	if rep.Outcome == "fatal" {
		level = slog.LevelError
	}
	mlog.From(r.Context()).Log(r.Context(), level, "playback_failed",
		"event", "playback_failed",
		"session_id", sessionID,
		"video_id", clip(rep.VideoID),
		"user_id", p.UserID,
		"mode", clip(rep.Mode),
		"engine", clip(rep.Engine),
		"outcome", clip(rep.Outcome),
		"code", clip(rep.Code),
		"error", clip(rep.Message),
		"http_status", rep.HTTPStatus,
		"url", clip(stripQuery(rep.URL)),
		"position_sec", rep.PositionSec,
		"user_agent", clip(r.UserAgent()),
	)
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) reports() *sessionDebouncer {
	h.reportOnce.Do(func() { h.reportLimit = newSessionDebouncer(reportInterval) })
	return h.reportLimit
}

// stripQuery drops a URL's query and fragment: a stream URL's query is its
// signature.
func stripQuery(raw string) string {
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil {
		u.RawQuery, u.Fragment, u.User = "", "", nil
		return u.String()
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

// clip bounds s to reportFieldMax bytes, on a rune boundary.
func clip(s string) string {
	if len(s) <= reportFieldMax {
		return s
	}
	s = s[:reportFieldMax]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}
