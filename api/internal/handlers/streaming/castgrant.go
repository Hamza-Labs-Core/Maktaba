package streaming

// POST /api/cast/grants (Epic 58 story 58.07): a signed-in sender mints a cast grant for
// Maktaba's Cast receiver, naming the one video or the one play queue it casts. The TV
// then opens its stream sessions, reports progress and renews its URLs with the grant
// alone (api/internal/castgrant), so the phone can sleep or leave the house.
//
// Who may: any person signed in on the home who may play the item now: a viewer with the
// video's library, an admin, a restricted account within its limits (the grant carries
// its limit: its user, and the sign-in's device age limit). Who may not: no one (401); a
// cast grant itself (a grant mints no grant); the admin token, which is no person; a
// sign-in through Maktaba Cloud, which may not mint a credential that outlives its link
// (403). An item the caller may not see answers as a missing one (404), as OpenSession's.

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/castgrant"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// CastGrants mints cast grants.
type CastGrants struct {
	DB   *sql.DB
	Keys *keys.Set
	// TTL is a grant's life (0 or more than castgrant.MaxTTL: castgrant.MaxTTL).
	TTL time.Duration
	Now func() time.Time
}

// CastGrantRequest names the item cast: exactly one of the two.
type CastGrantRequest struct {
	VideoID string `json:"video_id,omitempty"`
	QueueID string `json:"queue_id,omitempty"`
}

// CastGrantResponse is the grant and what it names.
type CastGrantResponse struct {
	Grant     string    `json:"grant"`
	Scope     string    `json:"scope"`
	ExpiresAt time.Time `json:"expires_at"`
	VideoID   string    `json:"video_id,omitempty"`
	QueueID   string    `json:"queue_id,omitempty"`
}

// Mount wires the route.
func (h *CastGrants) Mount(r chi.Router) {
	r.Post("/api/cast/grants", h.Create)
}

func (h *CastGrants) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Create implements POST /api/cast/grants.
func (h *CastGrants) Create(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{Type: httperror.TypeUnauthorized, Title: "unauthorized",
			Status: http.StatusUnauthorized, Detail: "sign in to cast"})
		return
	}
	switch {
	case p.Source == principal.SourceCast:
		httperror.Write(w, r, castgrant.ScopeProblem("a cast grant cannot mint another"))
		return
	case p.Source == principal.SourceAdminToken || p.UserID == "" || uuid.Validate(p.UserID) != nil:
		httperror.Write(w, r, httperror.Forbidden("", "a cast grant is minted for a person's own sign-in, not the admin token").
			With("code", "cast_needs_person"))
		return
	case p.ViaCloud():
		httperror.Write(w, r, httperror.Forbidden("", "a sign-in through Maktaba Cloud cannot mint a cast grant: sign in on the home itself").
			With("code", "cast_via_cloud"))
		return
	}
	var req CastGrantRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if (req.VideoID == "") == (req.QueueID == "") {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "video_id", Message: "name exactly one of video_id and queue_id"},
			{Field: "queue_id", Message: "name exactly one of video_id and queue_id"},
		}))
		return
	}
	scope := principal.CastScope{}
	if req.VideoID != "" {
		if !h.videoAllowed(w, r, p, req.VideoID) {
			return
		}
		scope.VideoID = req.VideoID
	} else {
		if !h.queueOwned(w, r, p, req.QueueID) {
			return
		}
		scope.QueueID = req.QueueID
	}
	if h.Keys == nil || h.Keys.Active() == nil {
		w.Header().Set("Retry-After", "5")
		httperror.Write(w, r, httperror.Unavailable(5))
		return
	}
	grant, exp, err := castgrant.Mint(h.Keys, p.UserID, p.AgeCeiling, scope, h.now(), h.TTL)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("mint the cast grant").Wrap(err))
		return
	}
	mlog.From(r.Context()).Info("cast grant minted", "user_id", p.UserID, "video_id", scope.VideoID,
		"queue_id", scope.QueueID, "expires_at", exp, "event", "cast_grant_minted")
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusCreated, CastGrantResponse{
		Grant: grant, Scope: castgrant.Scope, ExpiresAt: exp, VideoID: scope.VideoID, QueueID: scope.QueueID,
	})
}

// videoAllowed checks the caller may play the video now, as OpenSession does, or writes
// the answer: 400 for a malformed id, 404 for a missing or hidden video (and for a video in
// a library a restricted account cannot open), 403 for a library a viewer was not given.
func (h *CastGrants) videoAllowed(w http.ResponseWriter, r *http.Request, p *principal.Principal, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("video_id is not an id"))
		return false
	}
	var lib string
	err := h.DB.QueryRowContext(r.Context(), `SELECT CAST(library_id AS TEXT) FROM videos WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&lib)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return false
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return false
	}
	if !p.AccessAllLibraries && !p.HasLibrary(lib) {
		if parental.From(r.Context()).Limits() {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return false
		}
		httperror.Write(w, r, &httperror.Error{Type: httperror.Type("access-denied"), Title: "access denied",
			Status: http.StatusForbidden, Detail: "no access to this library"})
		return false
	}
	if !parental.Check(r.Context(), h.DB, parental.SubjectVideo, id) {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return false
	}
	return true
}

// queueOwned checks the queue is the caller's own, or writes 404 (a queue is its owner's
// alone, as the play queue routes answer).
func (h *CastGrants) queueOwned(w http.ResponseWriter, r *http.Request, p *principal.Principal, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("play queue "+id))
		return false
	}
	var n int
	err := h.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM play_queues WHERE id = $1 AND user_id = $2`, id, p.UserID).Scan(&n)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load the queue").Wrap(err))
		return false
	}
	if n == 0 {
		httperror.Write(w, r, httperror.NotFound("play queue "+id))
		return false
	}
	return true
}
