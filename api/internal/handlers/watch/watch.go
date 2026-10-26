package watch

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Handler serves the watch-session lifecycle (Story 29.1), the per-user
// history (29.2) and the activity feed + privacy switch (29.4). It is
// mounted by router/p29.go.
type Handler struct {
	DB *sql.DB

	// StaleTimeout caps credited time per heartbeat and defines when the
	// reaper interrupts a session. Zero ⇒ DefaultStaleTimeout.
	StaleTimeout time.Duration

	// IPSalt salts the client-IP hash. Empty ⇒ no hashing input beyond
	// the IP itself (still hashed, just unsalted).
	IPSalt string

	// NowFunc is the injectable clock (tests).
	NowFunc func() time.Time
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

func (h *Handler) repo() *repo { return &repo{db: h.DB} }

func (h *Handler) staleTimeout() time.Duration { return staleTimeoutOr(h.StaleTimeout) }

// Mount wires the watch routes. All require an authenticated principal
// (installed by the security chain) and are owner-scoped in-handler.
func (h *Handler) Mount(r chi.Router) {
	// Story 29.1 — session lifecycle.
	r.Post("/api/watch/start", h.Start)
	r.Post("/api/watch/heartbeat", h.Heartbeat)
	r.Post("/api/watch/stop", h.Stop)

	// Story 29.2 — history.
	r.Get("/api/me/history", h.History)
	r.Get("/api/me/history/{video_id}", h.VideoHistory)
	r.Delete("/api/me/history/{video_id}", h.DeleteHistory)

	// Story 29.4 — activity feed + privacy.
	r.Get("/api/me/activity", h.Activity)
	r.Get("/api/me/activity/settings", h.GetPrivacy)
	r.Put("/api/me/activity/settings", h.PutPrivacy)
}

// Start opens a watch session (Story 29.1). When the caller has paused
// tracking it is a no-op returning {tracking:false} (Story 29.4 / D5).
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req StartRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if _, err := uuid.Parse(req.VideoID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("video_id must be a uuid"))
		return
	}

	enabled, err := h.repo().trackingEnabled(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load tracking pref").Wrap(err))
		return
	}
	if !enabled {
		common.WriteJSON(w, r, http.StatusOK, StartResponse{Tracking: false})
		return
	}

	// A video in a library the caller cannot read is not found, like an
	// unknown one (no existence oracle, no stats for hidden videos).
	libraryID, exists, err := h.repo().videoLibrary(r.Context(), req.VideoID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("verify video").Wrap(err))
		return
	}
	if !exists || !videos.CanRead(p, libraryID) {
		httperror.Write(w, r, httperror.NotFound("video "+req.VideoID))
		return
	}

	// the app from X-Maktaba-Client (Epic 44); the device and platform the
	// body names, else the header's
	app := client.From(r.Context())
	row := startRow{
		id:            uuid.NewString(),
		userID:        p.UserID,
		videoID:       req.VideoID,
		deviceType:    orClient(truncate(req.DeviceType, 32), app.Device),
		platform:      orClient(truncate(req.Platform, 32), app.Platform),
		quality:       truncate(req.Quality, 32),
		ipHash:        HashIP(h.IPSalt, r),
		clientApp:     app.App,
		clientVersion: app.Version,
		now:           h.now(),
	}
	if err := h.repo().insertStart(r.Context(), row); err != nil {
		httperror.Write(w, r, httperror.Internal("open session").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, StartResponse{SessionID: row.id, Tracking: true})
}

// Heartbeat advances an active session (Story 29.1). A heartbeat for a
// closed session is a 409 — it never resurrects one.
func (h *Handler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req HeartbeatRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if _, err := uuid.Parse(req.SessionID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("session_id must be a uuid"))
		return
	}

	a, err := h.repo().loadActive(r.Context(), req.SessionID, p.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("session "+req.SessionID))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load session").Wrap(err))
		return
	}
	if a.state != StateActive {
		httperror.Write(w, r, httperror.Conflict("", "session is not active"))
		return
	}

	now := h.now()
	credited := CreditedSeconds(a.lastHeartbeat, now, h.staleTimeout())
	newDuration := a.durationSec + credited
	pct := PercentComplete(req.PositionSec, a.videoDuration)

	if _, err := h.repo().applyHeartbeat(r.Context(), req.SessionID, now, newDuration, pct); err != nil {
		httperror.Write(w, r, httperror.Internal("apply heartbeat").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, SessionView{
		SessionID: req.SessionID, VideoID: a.videoID, State: StateActive,
		DurationSec: newDuration, PercentComplete: pct,
	})
}

// Stop closes a session (Story 29.1). Idempotent: stopping an
// already-closed session returns its current view unchanged.
func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req StopRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if _, err := uuid.Parse(req.SessionID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("session_id must be a uuid"))
		return
	}

	a, err := h.repo().loadActive(r.Context(), req.SessionID, p.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("session "+req.SessionID))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load session").Wrap(err))
		return
	}

	// Already closed → return current view (idempotency).
	if a.state != StateActive {
		v, err := h.repo().sessionView(r.Context(), req.SessionID, p.UserID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("load session view").Wrap(err))
			return
		}
		common.WriteJSON(w, r, http.StatusOK, v)
		return
	}

	now := h.now()
	credited := CreditedSeconds(a.lastHeartbeat, now, h.staleTimeout())
	newDuration := a.durationSec + credited

	// Final percent: derive from the supplied position, else keep what
	// the last heartbeat recorded.
	var pct float64
	if req.PositionSec != nil {
		pct = PercentComplete(*req.PositionSec, a.videoDuration)
	} else if v, err := h.repo().sessionView(r.Context(), req.SessionID, p.UserID); err == nil {
		pct = v.PercentComplete
	}
	state := StopState(pct)

	if _, err := h.repo().stop(r.Context(), req.SessionID, now, state, pct, newDuration); err != nil {
		httperror.Write(w, r, httperror.Internal("stop session").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, SessionView{
		SessionID: req.SessionID, VideoID: a.videoID, State: state,
		DurationSec: newDuration, PercentComplete: pct,
	})
}

// orClient is the body's hint, or when it gives none the header's.
func orClient(hint, fromHeader string) string {
	if hint != "" {
		return hint
	}
	return fromHeader
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
