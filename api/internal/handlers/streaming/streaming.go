// Package streaming implements Stories 7.10, 7.11:
//
//	POST   /api/stream/sessions
//	GET    /api/stream/sessions/{id}
//	DELETE /api/stream/sessions/{id}
//	POST   /api/stream/sessions/{id}/progress
//	GET    /api/stream/capabilities
//
// The session lifecycle is a thin REST surface over an injected
// SessionService — production wires the gRPC Streaming client (Story
// 7.18); tests inject an in-memory fake.
//
// Watch-progress (Story 7.11) keeps a per-(user,video) row in
// playback_state with a debounced upsert: at most one persisted write
// per second per session, regardless of how often the player POSTs.
package streaming

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// SessionService is the gRPC-backed surface for OpenSession / CloseSession
// + capabilities. Story 7.18 wires the concrete implementation.
type SessionService interface {
	Open(ctx context.Context, req OpenSessionRequest) (OpenSessionResponse, error)
	Close(ctx context.Context, sessionID string) error
	Capabilities(ctx context.Context) (Capabilities, error)
}

// URLSigner mints the signed manifest URL for the session. Production
// implementation lives in the auth package (Story 10.8); we accept an
// interface so tests can stub it.
type URLSigner interface {
	SignManifestURL(sessionID, userID, libraryID string, ttl time.Duration) (string, time.Time, error)
}

// OpenSessionRequest is the AC-1 body shape; Streaming's gRPC stub
// consumes the same fields. Optional fields default in Streaming.
type OpenSessionRequest struct {
	VideoID         string `json:"video_id"`
	ClientProfile   string `json:"client_profile,omitempty"`
	AudioTrack      *int   `json:"audio_track,omitempty"`
	SubtitleTrack   string `json:"subtitle_track,omitempty"`
	StartSec        float64 `json:"start_sec,omitempty"`
	MaxBitrateKbps  int    `json:"max_bitrate_kbps,omitempty"`
	Format          string `json:"format,omitempty"`
	ForceSoftware   bool   `json:"force_software,omitempty"`
	ForceTranscode  bool   `json:"force_transcode,omitempty"`
	BurnSubs        bool   `json:"burn_subs,omitempty"`
	AcceptQueue     bool   `json:"accept_queue,omitempty"`
}

// OpenSessionResponse is the AC-1 envelope.
type OpenSessionResponse struct {
	SessionID        string         `json:"session_id"`
	Mode             string         `json:"mode"` // direct, remux, transcode
	ManifestURL      string         `json:"manifest_url,omitempty"`
	DirectURL        string         `json:"direct_url,omitempty"`
	ExpiresAt        time.Time      `json:"expires_at"`
	Ladder           []Rendition    `json:"ladder,omitempty"`
	CurrentRendition *Rendition     `json:"current_rendition,omitempty"`
}

// Rendition is a single ABR ladder rung.
type Rendition struct {
	Height      int    `json:"height"`
	Width       int    `json:"width"`
	BitrateKbps int    `json:"bitrate_kbps"`
	Codec       string `json:"codec"`
}

// Capabilities is the AC-4 shape.
type Capabilities struct {
	Codecs              []string `json:"codecs"`
	HWAccel             string   `json:"hwaccel"`
	MaxBitrateKbps      int      `json:"max_bitrate_kbps"`
	SupportedContainers []string `json:"supported_containers"`
	TranscodeSlots      Slots    `json:"transcode_slots"`
}

// Slots is the transcoder capacity snapshot.
type Slots struct {
	Used     int `json:"used"`
	Capacity int `json:"capacity"`
}

// Handler bundles deps. Service is mandatory in production; tests can
// inject a fake. NowFunc / TTL are testable.
type Handler struct {
	DB            *sql.DB
	Service       SessionService
	Signer        URLSigner
	SessionTTL    time.Duration
	NowFunc       func() time.Time
	CapCache      *capCache
	debounce      *sessionDebouncer
	debounceOnce  sync.Once
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	if h.CapCache == nil {
		h.CapCache = &capCache{ttl: 60 * time.Second}
	}
	h.debounceOnce.Do(func() { h.debounce = newSessionDebouncer(time.Second) })
	r.Get("/api/stream/capabilities", h.GetCapabilities)
	r.Post("/api/stream/sessions", h.OpenSession)
	r.Get("/api/stream/sessions/{id}", h.GetSession)
	r.Delete("/api/stream/sessions/{id}", h.CloseSession)
	r.Post("/api/stream/sessions/{id}/progress", h.PostProgress)
}

// OpenSession implements AC-1.
func (h *Handler) OpenSession(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req OpenSessionRequest
	if e := common.ReadJSON(r, &req, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if _, err := uuid.Parse(req.VideoID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("video_id required"))
		return
	}

	// Resolve the library_id so we can authorise + later anchor the
	// signed URL's lib[] claim.
	var libraryID string
	err := h.DB.QueryRowContext(r.Context(), `SELECT library_id FROM videos WHERE id=$1`, req.VideoID).Scan(&libraryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+req.VideoID))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video"))
		return
	}
	if !p.AccessAllLibraries && !p.HasLibrary(libraryID) {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/access-denied",
			Title:  "access denied",
			Status: http.StatusForbidden,
			Detail: "no access to this library",
		})
		return
	}

	// Clamp start_sec to duration_sec - 5 if too large (EC).
	var duration sql.NullFloat64
	_ = h.DB.QueryRowContext(r.Context(), `SELECT duration_sec FROM videos WHERE id=$1`, req.VideoID).Scan(&duration)
	clampedHeader := ""
	if duration.Valid && req.StartSec > duration.Float64 {
		req.StartSec = duration.Float64 - 5
		if req.StartSec < 0 {
			req.StartSec = 0
		}
		clampedHeader = "start-sec-clamped"
	}

	// gRPC Open. If Service is nil (single-binary stub) we synthesise
	// a transcode-mode session anchored to a manufactured URL.
	var resp OpenSessionResponse
	if h.Service != nil {
		resp, err = h.Service.Open(r.Context(), req)
		if err != nil {
			httperror.Write(w, r, &httperror.Error{
				Type:   "https://maktaba.dev/problems/streaming-unavailable",
				Title:  "streaming unavailable",
				Status: http.StatusServiceUnavailable,
				Detail: err.Error(),
			})
			w.Header().Set("Retry-After", "5")
			return
		}
	} else {
		resp = OpenSessionResponse{
			SessionID:   uuid.NewString(),
			Mode:        "transcode",
			ExpiresAt:   h.now().Add(h.ttl()),
			ManifestURL: "/stream/" + uuid.NewString() + "/manifest.m3u8",
		}
	}

	// Sign manifest URL if a signer is configured.
	if h.Signer != nil && resp.ManifestURL != "" {
		signed, exp, signErr := h.Signer.SignManifestURL(resp.SessionID, p.UserID, libraryID, h.ttl())
		if signErr == nil {
			resp.ManifestURL = signed
			resp.ExpiresAt = exp
		}
	}

	// Persist row.
	_, err = h.DB.ExecContext(r.Context(), `
		INSERT INTO streaming_sessions
		(id, user_id, video_id, client_profile, mode, audio_track_id, subtitle_lang,
		 start_sec, max_bitrate_kbps, burn_subs, opened_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
	`, resp.SessionID, p.UserID, req.VideoID, req.ClientProfile, resp.Mode,
		req.AudioTrack, req.SubtitleTrack, req.StartSec, req.MaxBitrateKbps,
		req.BurnSubs, h.now())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("persist session: "+err.Error()))
		return
	}

	if clampedHeader != "" {
		w.Header().Set("Maktaba-Warning", clampedHeader)
	}
	common.WriteJSON(w, r, http.StatusCreated, resp)
}

// GetSession implements AC-2.
func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	row := h.DB.QueryRowContext(r.Context(), `
		SELECT id, user_id, video_id, mode, opened_at, last_seen_at, closed_at
		FROM streaming_sessions WHERE id=$1
	`, id)
	var sess struct {
		ID         string    `json:"session_id"`
		UserID     string    `json:"user_id"`
		VideoID    string    `json:"video_id"`
		Mode       string    `json:"mode"`
		OpenedAt   time.Time `json:"opened_at"`
		LastSeenAt time.Time `json:"last_segment_fetched_at"`
		ClosedAt   sql.NullTime
	}
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.VideoID, &sess.Mode, &sess.OpenedAt, &sess.LastSeenAt, &sess.ClosedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("session "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load session"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, sess)
}

// CloseSession implements AC-3.
func (h *Handler) CloseSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if h.Service != nil {
		_ = h.Service.Close(r.Context(), id) // best-effort
	}
	if _, err := h.DB.ExecContext(r.Context(), `
		UPDATE streaming_sessions SET closed_at = $1 WHERE id = $2
	`, h.now(), id); err != nil {
		httperror.Write(w, r, httperror.Internal("close session"))
		return
	}
	common.WriteNoContent(w)
}

// GetCapabilities implements AC-4. Cached for 60 s inside h.CapCache.
func (h *Handler) GetCapabilities(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.WriteJSON(w, r, http.StatusOK, Capabilities{
			Codecs:              []string{"h264", "hevc"},
			HWAccel:             "none",
			MaxBitrateKbps:      10000,
			SupportedContainers: []string{"mp4", "ts"},
		})
		return
	}
	caps, fresh, err := h.CapCache.GetOrFetch(r.Context(), h.Service.Capabilities, h.now)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("capabilities: "+err.Error()))
		return
	}
	if !fresh {
		w.Header().Set("X-Cache", "HIT")
	}
	common.WriteJSON(w, r, http.StatusOK, caps)
}

// ProgressRequest is the AC-1 7.11 body.
type ProgressRequest struct {
	PositionSec float64 `json:"position_sec"`
	Completed   *bool   `json:"completed,omitempty"`
}

// PostProgress implements Story 7.11 AC-1/AC-3/AC-4: debounced upsert,
// stale POSTs accepted (no monotonicity), warning header on clamp.
func (h *Handler) PostProgress(w http.ResponseWriter, r *http.Request) {
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
	var req ProgressRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}

	// Resolve video_id + duration.
	var videoID string
	var duration sql.NullFloat64
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT s.video_id, v.duration_sec
		FROM streaming_sessions s
		LEFT JOIN videos v ON v.id = s.video_id
		WHERE s.id = $1
	`, sessionID).Scan(&videoID, &duration)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("session "+sessionID))
			return
		}
		httperror.Write(w, r, httperror.Internal("load session"))
		return
	}

	warned := ""
	if duration.Valid && req.PositionSec > duration.Float64 {
		req.PositionSec = duration.Float64
		warned = "position-clamped"
	}

	// Auto-completion at ≥95 % (AC-1).
	completed := false
	if req.Completed != nil {
		completed = *req.Completed
	}
	if duration.Valid && duration.Float64 > 0 && req.PositionSec/duration.Float64 > 0.95 {
		completed = true
	}

	// Debounce — 1/s per session (AC-3).
	if !h.debounce.Allow(sessionID, h.now()) {
		// Accepted but skipped. Returning 200 keeps the player happy.
		if warned != "" {
			w.Header().Set("Maktaba-Warning", warned)
		}
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"debounced": true})
		return
	}

	// Upsert. The (user, video) PK lets a closed-session POST still
	// land (EC: "POST after DELETE /sessions/{id}").
	_, err = h.DB.ExecContext(r.Context(), `
		INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, video_id) DO UPDATE SET
		  position_sec = EXCLUDED.position_sec,
		  completed    = EXCLUDED.completed,
		  updated_at   = EXCLUDED.updated_at
	`, p.UserID, videoID, req.PositionSec, completed, h.now())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("upsert progress: "+err.Error()))
		return
	}

	if warned != "" {
		w.Header().Set("Maktaba-Warning", warned)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{
		"position_sec": req.PositionSec,
		"completed":    completed,
	})
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

func (h *Handler) ttl() time.Duration {
	if h.SessionTTL > 0 {
		return h.SessionTTL
	}
	return 30 * time.Minute
}

// ---------------------------------------------------------------
// capCache: 60-s in-memory cache for the GetCapabilities path.
// ---------------------------------------------------------------

type capCache struct {
	mu    sync.Mutex
	val   Capabilities
	fresh bool
	exp   time.Time
	ttl   time.Duration
}

func (c *capCache) GetOrFetch(ctx context.Context, fetch func(context.Context) (Capabilities, error), now func() time.Time) (Capabilities, bool, error) {
	c.mu.Lock()
	if c.fresh && now().Before(c.exp) {
		v := c.val
		c.mu.Unlock()
		return v, false, nil
	}
	c.mu.Unlock()

	v, err := fetch(ctx)
	if err != nil {
		return Capabilities{}, true, err
	}
	c.mu.Lock()
	c.val = v
	c.fresh = true
	c.exp = now().Add(c.ttl)
	c.mu.Unlock()
	return v, true, nil
}

// ---------------------------------------------------------------
// sessionDebouncer: 1-write-per-second per session id.
// ---------------------------------------------------------------

type sessionDebouncer struct {
	mu       sync.Mutex
	last     map[string]time.Time
	interval time.Duration
}

func newSessionDebouncer(interval time.Duration) *sessionDebouncer {
	return &sessionDebouncer{
		last:     map[string]time.Time{},
		interval: interval,
	}
}

func (d *sessionDebouncer) Allow(id string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	prev, ok := d.last[id]
	if ok && now.Sub(prev) < d.interval {
		return false
	}
	d.last[id] = now
	return true
}
