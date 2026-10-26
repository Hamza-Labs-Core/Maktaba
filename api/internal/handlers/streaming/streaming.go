// Package streaming implements Stories 7.10, 7.11:
//
//	POST   /api/stream/sessions
//	GET    /api/stream/sessions/{id}
//	DELETE /api/stream/sessions/{id}
//	POST   /api/stream/sessions/{id}/progress
//	POST   /api/stream/sessions/{id}/errors   (playback_report.go)
//	GET    /api/stream/capabilities
//
// The session lifecycle is a thin REST surface over an injected
// SessionService — production wires the gRPC Streaming client (Story
// 7.18); tests inject an in-memory fake.
//
// Watch-progress (Story 7.11) keeps a per-(user,video) row in
// playback_state with a debounced upsert: at most one persisted write
// per second per session, regardless of how often the player POSTs.
//
// On a full node (Offline set, PR #145) playback goes on with the home's
// database down: the video's library and length come from the node's
// catalog, the session's owner is kept here (the streaming_sessions row is
// written once the database answers again: Reconcile), and every database
// call is bounded so a dead connection is not waited on. Only a failure of
// the database itself counts (it does not answer, or is the read-only copy
// standing in): a request that went away, or an error the database answered
// with, is not an outage.
package streaming

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/watch"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// SessionService is the gRPC-backed surface for OpenSession / CloseSession
// + capabilities. Story 7.18 wires the concrete implementation.
type SessionService interface {
	Open(ctx context.Context, req OpenSessionRequest) (OpenSessionResponse, error)
	Close(ctx context.Context, sessionID string) error
	Capabilities(ctx context.Context) (Capabilities, error)
}

// URLSigner signs the stream URLs the streaming service handed out for a
// session (Story 10.8): the manifest (aud=streaming, sub=session) or the
// direct-play URL (aud=streaming-direct, sub=video), for the user and the
// video's library, expiring after ttl. Production is signedurl.Minter
// (api/internal/auth/signedurl); tests can stub it.
type URLSigner interface {
	SignStreamURL(rawURL, sessionID, userID, libraryID string, ttl time.Duration) (string, time.Time, error)
}

// OpenSessionRequest is the AC-1 body shape; Streaming's gRPC stub
// consumes the same fields. Optional fields default in Streaming.
type OpenSessionRequest struct {
	VideoID        string  `json:"video_id"`
	ClientProfile  string  `json:"client_profile,omitempty"`
	AudioTrack     *int    `json:"audio_track,omitempty"`
	SubtitleTrack  string  `json:"subtitle_track,omitempty"`
	StartSec       float64 `json:"start_sec,omitempty"`
	MaxBitrateKbps int     `json:"max_bitrate_kbps,omitempty"`
	Format         string  `json:"format,omitempty"`
	ForceSoftware  bool    `json:"force_software,omitempty"`
	ForceTranscode bool    `json:"force_transcode,omitempty"`
	BurnSubs       bool    `json:"burn_subs,omitempty"`
	AcceptQueue    bool    `json:"accept_queue,omitempty"`
}

// OpenSessionResponse is the AC-1 envelope.
type OpenSessionResponse struct {
	SessionID        string      `json:"session_id"`
	Mode             string      `json:"mode"` // direct, remux, transcode
	ManifestURL      string      `json:"manifest_url,omitempty"`
	DirectURL        string      `json:"direct_url,omitempty"`
	ExpiresAt        time.Time   `json:"expires_at"`
	Ladder           []Rendition `json:"ladder,omitempty"`
	CurrentRendition *Rendition  `json:"current_rendition,omitempty"`
	// DurationSec is the video's length in the catalogue (0: unknown): a
	// player knows the real end of a stream that may stop short of it.
	DurationSec float64 `json:"duration_sec,omitempty"`
	// StartSec is where in the video the stream starts (seconds; 0: its
	// start): a transcode opened at a position (start_sec) starts there, its
	// times the video's, the part before it gaps a player cannot play.
	StartSec float64 `json:"start_sec,omitempty"`
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

// Offline is a full node's way to play with the home's database down (nil on
// the home).
type Offline interface {
	// DBDown: the home's database does not answer (it is skipped, not waited on).
	DBDown() bool
	// DBFailed records a call that failed on it.
	DBFailed(err error)
	// Video is a video's library and length from the node's catalog.
	Video(id string) (libraryID string, durationSec float64, ok bool)
	// WhenDBBack calls fn each time the home's database answers again.
	WhenDBBack(fn func())
	// LedgerPath is where the sessions it opened are kept until the database has them
	// ("": in memory only).
	LedgerPath() string
}

// ProblemUnavailableOffline is the problem of a video a node cannot play while
// the home's database does not answer (it is not in the node's catalog).
const ProblemUnavailableOffline = "https://maktaba.dev/problems/video-unavailable-offline"

// offlineDBTimeout bounds a node's database call: past it the catalog answers.
const offlineDBTimeout = 3 * time.Second

var errNotInCatalog = errors.New("streaming: video not in this machine's catalog")

// Handler bundles deps. Service is mandatory in production; tests can
// inject a fake. NowFunc / TTL are testable.
type Handler struct {
	DB *sql.DB
	// IPSalt salts the viewer's address hashed into a play's record (the home's
	// secret, router.AnalyticsSalt); empty keeps no hash.
	IPSalt     string
	Service    SessionService
	Signer     URLSigner
	SessionTTL time.Duration
	// Settings holds the runtime session URL TTL (session.url_ttl_sec; nil
	// or unset: SessionTTL, else 30 min).
	Settings *runtimecfg.Reader
	// Offline: a full node playing on with the home's database down (nil: the home).
	Offline      Offline
	NowFunc      func() time.Time
	CapCache     *capCache
	debounce     *sessionDebouncer
	debounceOnce sync.Once
	ledger       ledger
	ledgerOnce   sync.Once
	// reconcileOnce starts the retry of the sessions not written yet (Mount)
	reconcileOnce sync.Once
	// reports bounds the playback error reports (PostPlaybackError)
	reports reportLimiter
}

// dbCtx bounds a database call on a node (Offline set); the home waits as before.
func (h *Handler) dbCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if h.Offline == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, offlineDBTimeout)
}

// dbUp: the database is worth asking (the home's always is; a node's while it answers).
func (h *Handler) dbUp() bool { return h.Offline == nil || !h.Offline.DBDown() }

// failed reports whether err is a database failure the node's catalog stands in for
// (recording it): the database did not answer (nodemode.Unreachable), or the home's
// is down and the read-only copy answered for it. "No rows" is an answer, an error the
// database answered with is not an outage, a request that went away (ctx) says
// nothing, and the home has no catalog.
func (h *Handler) failed(ctx context.Context, err error) bool {
	if err == nil || errors.Is(err, sql.ErrNoRows) || h.Offline == nil || ctx.Err() != nil {
		return false
	}
	if !nodemode.Unreachable(err) && !h.Offline.DBDown() {
		return false
	}
	h.Offline.DBFailed(err)
	return true
}

// streamingNotFound reports whether the streaming service said it does not know the
// video (it answers "not-found" in band).
func streamingNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not-found")
}

// ProblemInsufficientStorage: the streaming service has too little free on its
// cache's disk to prepare (transcode) the video.
const ProblemInsufficientStorage = "https://maktaba.dev/problems/insufficient-storage"

// streamingNoDiskSpace reports whether the streaming service refused a transcode
// for want of disk space (it answers "insufficient-storage" in band).
func streamingNoDiskSpace(err error) bool {
	return err != nil && strings.Contains(err.Error(), "insufficient-storage")
}

// Reconcile writes what the database lacks of the sessions this node opened while the
// home's database did not answer: their rows, as they were (an id the database already
// has is left as it is), and the closing of those it had and this node closed then. It
// stops at the first failure: the next time the database answers again tries the rest.
func (h *Handler) Reconcile(ctx context.Context) {
	if h.Offline == nil || h.DB == nil {
		return
	}
	for id, e := range h.led().pending() {
		if ctx.Err() != nil {
			return
		}
		qctx, cancel := context.WithTimeout(ctx, offlineDBTimeout)
		var err error
		if e.Written {
			// the row is there: its closing is not
			_, err = h.DB.ExecContext(qctx, `UPDATE streaming_sessions SET closed_at = $1
			    WHERE id = $2 AND closed_at IS NULL`, e.Closed, id)
		} else {
			var closed any
			if !e.Closed.IsZero() {
				closed = e.Closed
			}
			_, err = h.DB.ExecContext(qctx, `
			INSERT INTO streaming_sessions
			(id, user_id, video_id, client_profile, mode, audio_track_id, subtitle_lang,
			 start_sec, max_bitrate_kbps, burn_subs, opened_at, last_seen_at, closed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11, $12)
			ON CONFLICT (id) DO NOTHING`, id, e.Owner, e.Video, e.Row.ClientProfile, e.Mode,
				e.Row.AudioTrack, e.Row.SubtitleTrack, e.Row.StartSec, e.Row.MaxBitrateKbps, e.Row.BurnSubs,
				e.Opened, closed)
		}
		cancel()
		if err != nil {
			if h.DB.PingContext(ctx) != nil {
				return // the database is away again: the next time it is back
			}
			// this one does not write (it stays to try again): the others do
			continue
		}
		if e.Written {
			h.led().closeWritten(id)
		} else {
			h.led().wrote(id)
		}
	}
}

// reconcileEvery is how often the sessions not written yet are tried again besides the
// database coming back (one that failed then, or a restart's while it answered).
const reconcileEvery = 5 * time.Minute

// led is the node's ledger, kept where the node says (Offline.LedgerPath).
func (h *Handler) led() *ledger {
	h.ledgerOnce.Do(func() {
		if h.Offline != nil {
			h.ledger.path = h.Offline.LedgerPath()
		}
	})
	return &h.ledger
}

// video resolves a video's library and length: the database, or on a node whose
// database does not answer, its catalog (errNotInCatalog when the video is not there).
func (h *Handler) video(ctx context.Context, id string) (string, sql.NullFloat64, error) {
	if h.dbUp() {
		qctx, cancel := h.dbCtx(ctx)
		var lib string
		var dur sql.NullFloat64
		err := h.DB.QueryRowContext(qctx, `SELECT library_id, duration_sec FROM videos WHERE id=$1`, id).Scan(&lib, &dur)
		cancel()
		if !h.failed(ctx, err) {
			return lib, dur, err
		}
	}
	lib, d, ok := h.Offline.Video(id)
	if !ok {
		return "", sql.NullFloat64{}, errNotInCatalog
	}
	return lib, sql.NullFloat64{Float64: d, Valid: d > 0}, nil
}

func writeUnavailableOffline(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "30")
	e := &httperror.Error{Type: ProblemUnavailableOffline, Status: http.StatusServiceUnavailable,
		Title:  "this video isn't available while the home is unreachable",
		Detail: "this machine has no record of the video, and the home's database does not answer"}
	httperror.Write(w, r, e.With("code", "video_unavailable_offline"))
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	if h.CapCache == nil {
		h.CapCache = &capCache{ttl: 60 * time.Second}
	}
	h.debounceOnce.Do(func() { h.debounce = newSessionDebouncer(time.Second) })
	if h.Offline != nil {
		// the sessions opened while the database was down are written once it answers,
		// and tried again now and then (one that did not write then)
		h.Offline.WhenDBBack(func() { h.Reconcile(context.Background()) })
		h.reconcileOnce.Do(func() {
			go func() {
				t := time.NewTicker(reconcileEvery)
				defer t.Stop()
				for range t.C {
					if len(h.led().pending()) > 0 {
						h.Reconcile(context.Background())
					}
				}
			}()
		})
	}
	r.Get("/api/stream/capabilities", h.GetCapabilities)
	r.Post("/api/stream/sessions", h.OpenSession)
	r.Get("/api/stream/sessions/{id}", h.GetSession)
	r.Delete("/api/stream/sessions/{id}", h.CloseSession)
	r.Post("/api/stream/sessions/{id}/progress", h.PostProgress)
	r.Post("/api/stream/sessions/{id}/errors", h.PostPlaybackError)
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
	libraryID, duration, err := h.video(r.Context(), req.VideoID)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			httperror.Write(w, r, httperror.NotFound("video "+req.VideoID))
		case errors.Is(err, errNotInCatalog):
			writeUnavailableOffline(w, r)
		default:
			httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		}
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
		if err != nil && h.Offline != nil && !h.dbUp() && streamingNotFound(err) {
			// this machine's streaming role does not know the video while the home's
			// database is down (it is not in its catalog)
			writeUnavailableOffline(w, r)
			return
		}
		if streamingNoDiskSpace(err) {
			e := &httperror.Error{Type: ProblemInsufficientStorage, Status: http.StatusInsufficientStorage,
				Title:  "not enough disk space to prepare this video",
				Detail: "the server that streams it has too little free on its disk to transcode it"}
			httperror.Write(w, r, e.With("code", "insufficient_storage"))
			return
		}
		if err != nil {
			mlog.From(r.Context()).Error("stream session not opened",
				"video_id", req.VideoID, "user_id", p.UserID, "client_profile", req.ClientProfile,
				"start_sec", req.StartSec, "err", err.Error(), "event", "stream_open_failed")
			w.Header().Set("Retry-After", "5")
			httperror.Write(w, r, &httperror.Error{
				Type:   "https://maktaba.dev/problems/streaming-unavailable",
				Title:  "streaming unavailable",
				Status: http.StatusServiceUnavailable,
				Detail: err.Error(),
			})
			return
		}
	} else {
		sid := uuid.NewString()
		resp = OpenSessionResponse{
			SessionID:   sid,
			Mode:        "transcode",
			ExpiresAt:   h.now().Add(h.ttl(r.Context())),
			ManifestURL: "/stream/" + sid + "/manifest.m3u8",
		}
	}

	// Sign the stream URLs (Story 10.8): the streaming service refuses an
	// unsigned manifest/segment/direct request, so without this a session opens
	// but no byte plays. The URL lives as long as the session can: the
	// session TTL, or the video's length plus an hour when that is longer.
	if h.Signer != nil {
		ttl := h.urlTTL(r.Context(), duration)
		for _, u := range []*string{&resp.ManifestURL, &resp.DirectURL} {
			if *u == "" {
				continue
			}
			signed, exp, signErr := h.Signer.SignStreamURL(*u, resp.SessionID, p.UserID, libraryID, ttl)
			if signErr != nil {
				w.Header().Set("Retry-After", "5")
				httperror.Write(w, r, &httperror.Error{
					Type:   "https://maktaba.dev/problems/signing-unavailable",
					Title:  "signing unavailable",
					Status: http.StatusServiceUnavailable,
					Detail: signErr.Error(),
				})
				return
			}
			*u = signed
			resp.ExpiresAt = exp
		}
	}

	// A node keeps every session's owner itself: with the home's database
	// down that is the only record (the row is not written meanwhile).
	if h.Offline != nil && !h.led().open(resp.SessionID, p.UserID, req, resp.Mode, h.now()) {
		httperror.Write(w, r, httperror.Internal("persist session: session id conflict"))
		return
	}

	// Persist row. The streaming service hands back the open session of the
	// same (user, video) when there is one (a reload, a second tab): that is
	// the row already stored, refreshed instead of a duplicate-key 500.
	if h.dbUp() {
		qctx, cancel := h.dbCtx(r.Context())
		res, err := h.DB.ExecContext(qctx, `
		INSERT INTO streaming_sessions
		(id, user_id, video_id, client_profile, mode, audio_track_id, subtitle_lang,
		 start_sec, max_bitrate_kbps, burn_subs, opened_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
		ON CONFLICT (id) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at, closed_at = NULL
		WHERE streaming_sessions.user_id = EXCLUDED.user_id
	`, resp.SessionID, p.UserID, req.VideoID, req.ClientProfile, resp.Mode,
			req.AudioTrack, req.SubtitleTrack, req.StartSec, req.MaxBitrateKbps,
			req.BurnSubs, h.now())
		cancel()
		switch {
		case h.failed(r.Context(), err):
			// the node's own record holds it, and writes it once the database answers
		case err != nil:
			httperror.Write(w, r, httperror.Internal("persist session: "+err.Error()))
			return
		default:
			if n, nerr := res.RowsAffected(); nerr == nil && n == 0 {
				// The id is another user's session: never hand it (or a URL
				// signed for it) to this caller.
				h.led().drop(resp.SessionID)
				httperror.Write(w, r, httperror.Internal("persist session: session id conflict"))
				return
			}
			h.led().wrote(resp.SessionID)
		}
	}

	if clampedHeader != "" {
		w.Header().Set("Maktaba-Warning", clampedHeader)
	}
	if duration.Valid {
		resp.DurationSec = duration.Float64
	}
	// the session's id next to its video and mode: a later playback_failed,
	// stream_request_failed or vod_transcode_exited line names the session only
	mlog.From(r.Context()).Info("stream session opened",
		"session_id", resp.SessionID, "video_id", req.VideoID, "user_id", p.UserID,
		"mode", resp.Mode, "start_sec", req.StartSec, "event", "stream_session_opened")
	common.WriteJSON(w, r, http.StatusCreated, resp)
}

// GetSession implements AC-2. Only the session's owner or an admin may
// read it; anyone else gets 404, so a session id does not leak.
func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var sess struct {
		ID         string     `json:"session_id"`
		UserID     string     `json:"user_id"`
		VideoID    string     `json:"video_id"`
		Mode       string     `json:"mode"`
		OpenedAt   time.Time  `json:"opened_at"`
		LastSeenAt time.Time  `json:"last_segment_fetched_at"`
		ClosedAt   *time.Time `json:"closed_at,omitempty"`
	}
	var closedAt sql.NullTime
	asked := h.dbUp()
	var err error
	if asked {
		qctx, cancel := h.dbCtx(r.Context())
		err = h.DB.QueryRowContext(qctx, `
		SELECT id, user_id, video_id, mode, opened_at, last_seen_at, closed_at
		FROM streaming_sessions WHERE id=$1
	`, id).Scan(&sess.ID, &sess.UserID, &sess.VideoID, &sess.Mode, &sess.OpenedAt, &sess.LastSeenAt, &closedAt)
		cancel()
	}
	if !asked || err != nil {
		if asked && !errors.Is(err, sql.ErrNoRows) && !h.failed(r.Context(), err) {
			httperror.Write(w, r, httperror.Internal("load session").Wrap(err))
			return
		}
		// not asked, no row (opened here while the database was down), or a
		// failure: the node's record answers
		e, ok := h.led().get(id)
		if !ok {
			httperror.Write(w, r, httperror.NotFound("session "+id))
			return
		}
		sess.ID, sess.UserID, sess.VideoID, sess.Mode = id, e.Owner, e.Video, e.Mode
		sess.OpenedAt, sess.LastSeenAt = e.Opened, e.Opened
		if !e.Closed.IsZero() {
			closedAt = sql.NullTime{Time: e.Closed, Valid: true}
		}
	}
	if !mayActOn(p, sess.UserID) {
		httperror.Write(w, r, httperror.NotFound("session "+id))
		return
	}
	if closedAt.Valid {
		sess.ClosedAt = &closedAt.Time
	}
	common.WriteJSON(w, r, http.StatusOK, sess)
}

// CloseSession implements AC-3. Only the session's owner or an admin may
// close it; anyone else (and an unknown id) gets 404.
func (h *Handler) CloseSession(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var owner string
	asked := h.dbUp()
	var err error
	if asked {
		qctx, cancel := h.dbCtx(r.Context())
		err = h.DB.QueryRowContext(qctx,
			`SELECT user_id FROM streaming_sessions WHERE id = $1`, id).Scan(&owner)
		cancel()
	}
	if !asked || err != nil {
		if asked && !errors.Is(err, sql.ErrNoRows) && !h.failed(r.Context(), err) {
			httperror.Write(w, r, httperror.Internal("load session").Wrap(err))
			return
		}
		// a session opened here while the database was down has no row: the
		// node's record names its owner (one already closed is no more)
		e, ok := h.led().get(id)
		if !ok || !e.Closed.IsZero() {
			httperror.Write(w, r, httperror.NotFound("session "+id))
			return
		}
		owner = e.Owner
	}
	if !mayActOn(p, owner) {
		httperror.Write(w, r, httperror.NotFound("session "+id))
		return
	}
	if h.Service != nil {
		// best-effort: the streaming service reaps an idle session anyway
		if err := h.Service.Close(r.Context(), id); err != nil {
			mlog.From(r.Context()).Warn("stream session not closed on the streaming service",
				"session_id", id, "err", err.Error(), "event", "stream_close_failed")
		}
	}
	h.led().close(id, h.now())
	if h.dbUp() {
		qctx, cancel := h.dbCtx(r.Context())
		_, err := h.DB.ExecContext(qctx, `
		UPDATE streaming_sessions SET closed_at = $1 WHERE id = $2
	`, h.now(), id)
		cancel()
		if err != nil && !h.failed(r.Context(), err) {
			httperror.Write(w, r, httperror.Internal("close session").Wrap(err))
			return
		}
		if err == nil {
			h.led().closeWritten(id)
			// its play ends with it (Epic 44 story 44.3); best effort, the
			// reaper interrupts one left open
			if err := watch.StopStreamPlay(r.Context(), h.DB, id, h.now()); err != nil {
				mlog.From(r.Context()).Warn("play not closed",
					"session_id", id, "err", err.Error(), "event", "stream_play_stop_failed")
			}
		}
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

	// Resolve the owner, video_id + duration.
	var ownerID, videoID string
	var duration sql.NullFloat64
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT s.user_id, s.video_id, v.duration_sec
		FROM streaming_sessions s
		LEFT JOIN videos v ON v.id = s.video_id
		WHERE s.id = $1
	`, sessionID).Scan(&ownerID, &videoID, &duration)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("session "+sessionID))
			return
		}
		httperror.Write(w, r, httperror.Internal("load session").Wrap(err))
		return
	}
	// Only the owner (or an admin) may report progress on a session; the
	// position lands on the owner's playback state either way.
	if !mayActOn(p, ownerID) {
		httperror.Write(w, r, httperror.NotFound("session "+sessionID))
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

	// The play (Epic 44 story 44.3): one watch_sessions row per stream session,
	// kept by its progress. Before the upsert below, which it measures against.
	// Best effort: the resume point matters more than the play's record.
	if err := watch.RecordStreamProgress(r.Context(), h.DB, watch.StreamProgress{
		StreamSessionID: sessionID, UserID: ownerID, VideoID: videoID,
		PositionSec: req.PositionSec, VideoDurationSec: duration.Float64, Completed: completed,
		Client: client.From(r.Context()), IPHash: watch.HashIP(h.IPSalt, r), Now: h.now(),
	}); err != nil {
		mlog.From(r.Context()).Warn("play not recorded",
			"session_id", sessionID, "err", err.Error(), "event", "stream_play_record_failed")
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
	`, ownerID, videoID, req.PositionSec, completed, h.now())
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

// mayActOn reports whether p may read or act on a session owned by
// ownerID: its owner, or an admin.
func mayActOn(p *principal.Principal, ownerID string) bool {
	if p == nil {
		return false
	}
	return p.IsAdmin || (p.UserID != "" && strings.EqualFold(p.UserID, ownerID))
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

func (h *Handler) ttl(ctx context.Context) time.Duration {
	if h.dbUp() {
		qctx, cancel := h.dbCtx(ctx)
		d, ok := h.Settings.Seconds(qctx, runtimecfg.SessionURLTTL)
		cancel()
		if ok {
			return d
		}
	}
	if h.SessionTTL > 0 {
		return h.SessionTTL
	}
	return 30 * time.Minute
}

// urlTTL is a signed stream URL's lifetime: the session TTL, or the video's
// length plus an hour when that is longer (the native player cannot ask for a
// fresh URL mid-video), capped by the signer (24 h).
func (h *Handler) urlTTL(ctx context.Context, duration sql.NullFloat64) time.Duration {
	ttl := h.ttl(ctx)
	if duration.Valid && duration.Float64 > 0 {
		if d := time.Duration(duration.Float64*float64(time.Second)) + time.Hour; d > ttl {
			ttl = d
		}
	}
	return ttl
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
