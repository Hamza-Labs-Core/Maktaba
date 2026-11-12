// Package parental holds the parental controls' routes. This file is story 57.01's:
// the age tier an admin sets on a video, on what contains videos and on a library,
// and the ratings table every app maps with.
//
//	PUT /api/videos/{id}/age-tier                               admin
//	PUT /api/libraries/{id}/age-tier                            admin (the library's default)
//	PUT /api/series/{id}/age-tier                               admin
//	PUT /api/collections/{id}/age-tier                          admin
//	PUT /api/channels/{id}/age-tier                             admin
//	PUT /api/libraries/{id}/youtube/sources/{sid}/age-tier      admin
//	GET /api/parental/ratings                                   every signed-in user
//
// A PUT stores what the admin chose and nothing else: videos.age_tier is written by
// the recompute (internal/parental), which the PUT asks for in the same transaction.
package parental

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// TiersHandler serves the age-tier routes.
type TiersHandler struct {
	DB *sql.DB
	// Recompute is the one writer of a video's tier.
	Recompute *parental.Recomputer
	Logger    *slog.Logger
}

// Mount registers the routes.
func (h *TiersHandler) Mount(r chi.Router) {
	r.Put("/api/videos/{id}/age-tier", h.PutVideo)
	r.Put("/api/libraries/{id}/age-tier", h.putContainer(libraryTarget))
	r.Put("/api/series/{id}/age-tier", h.putContainer(seriesTarget))
	r.Put("/api/collections/{id}/age-tier", h.putContainer(collectionTarget))
	r.Put("/api/channels/{id}/age-tier", h.putContainer(channelTarget))
	r.Put("/api/libraries/{id}/youtube/sources/{sid}/age-tier", h.putContainer(youtubeSourceTarget))
	r.Get("/api/parental/ratings", h.Ratings)
}

// TierResponse answers a PUT. For a video, Tier and Source are what the video now
// resolves to (clearing the admin's tier shows what it falls back to). For the rest,
// Tier is what was stored and Source what it will be on the videos that take it.
type TierResponse struct {
	Tier   *int    `json:"tier"`
	Source *string `json:"source"`
	// RecomputeCount is how many videos the change has rated again (a video: 1) or
	// queued to be.
	RecomputeCount int `json:"recompute_count"`
}

// target is what a container route changes.
type target struct {
	what   string // in errors and logs
	update string // $1 the tier, $2 the id, $3 the library of a nested route
	nested bool   // the id is {sid}, inside the library {id}
	kind   parental.Kind
	source parental.Source
}

var (
	libraryTarget = target{what: "library", kind: parental.KindLibrary, source: parental.SourceLibraryDefault,
		update: `UPDATE libraries SET age_tier = $1 WHERE id = $2::uuid`}
	seriesTarget = target{what: "series", kind: parental.KindSeries, source: parental.SourceAdminContainer,
		update: `UPDATE series SET age_tier = $1 WHERE id = $2::uuid`}
	collectionTarget = target{what: "collection", kind: parental.KindCollection, source: parental.SourceAdminContainer,
		update: `UPDATE collections SET age_tier = $1 WHERE id = $2::uuid`}
	// A channel's programmes are a rolling schedule: its tier is read where channels
	// are listed and played (story 57.10) and rates no video, so nothing is queued.
	channelTarget = target{what: "channel", source: parental.SourceAdminContainer,
		update: `UPDATE channels SET age_tier = $1 WHERE id = $2::uuid`}
	youtubeSourceTarget = target{what: "YouTube source", kind: parental.KindYouTubeSource, source: parental.SourceAdminContainer, nested: true,
		update: `UPDATE youtube_sources SET age_tier = $1 WHERE id = $2::uuid AND library_id = $3::uuid`}
)

// PutVideo implements PUT /api/videos/{id}/age-tier: the admin's own tier on one
// video, which beats every other source; null gives the video back to them.
func (h *TiersHandler) PutVideo(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r, "id", "video")
	if !ok {
		return
	}
	tier, ok := readTier(w, r)
	if !ok {
		return
	}
	// The tier and the job that works it out are stored together: if the answer
	// below cannot be given (the client went, the recompute failed), the job is still
	// there and the loop rates the video. The admin's tier never sits beside an older,
	// looser videos.age_tier with nothing left to rework it.
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("set the video's age tier").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE videos SET age_tier_admin = $1 WHERE id = $2::uuid`, tier, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("set the video's age tier").Wrap(err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("video "+id))
		return
	}
	if err := h.Recompute.Enqueue(ctx, tx, parental.KindVideo, id); err != nil {
		httperror.Write(w, r, httperror.Internal("queue the recompute").Wrap(err))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("set the video's age tier").Wrap(err))
		return
	}
	// rated now, for the answer; the queued job then finds nothing to change
	got, err := h.Recompute.RecomputeVideo(ctx, id)
	if err != nil {
		h.Recompute.Wake()
		httperror.Write(w, r, httperror.Internal("rate the video").Wrap(err))
		return
	}
	h.log(r, "video", id, tier, 1)
	out := TierResponse{Tier: got.Tier, RecomputeCount: 1}
	if got.Source != parental.SourceNone {
		s := string(got.Source)
		out.Source = &s
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// putContainer implements the PUT of a library's default and of a container's tier:
// the tier is stored and its videos' recompute queued in one transaction.
func (h *TiersHandler) putContainer(t target) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathID(w, r, "id", "library")
		if !ok {
			return
		}
		args := []any{nil, id}
		if t.nested {
			lib := id
			if id, ok = pathID(w, r, "sid", t.what); !ok {
				return
			}
			args = []any{nil, id, lib}
		}
		tier, ok := readTier(w, r)
		if !ok {
			return
		}
		args[0] = tier
		ctx := r.Context()
		tx, err := h.DB.BeginTx(ctx, nil)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("set the age tier").Wrap(err))
			return
		}
		defer func() { _ = tx.Rollback() }()
		res, err := tx.ExecContext(ctx, t.update, args...)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("set the age tier").Wrap(err))
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			httperror.Write(w, r, httperror.NotFound(t.what+" "+id))
			return
		}
		if t.kind == parental.KindLibrary && tier.Valid {
			// the first library default tier turns parental controls on (story 57.12)
			actor := ""
			if p := principal.FromContext(ctx); p != nil {
				actor = p.UserID
			}
			if err := parental.EnableTx(ctx, tx, actor); err != nil {
				httperror.Write(w, r, httperror.Internal("turn parental controls on").Wrap(err))
				return
			}
		}
		count := 0
		if t.kind != "" {
			if err := h.Recompute.Enqueue(ctx, tx, t.kind, id); err != nil {
				httperror.Write(w, r, httperror.Internal("queue the recompute").Wrap(err))
				return
			}
		}
		if err := tx.Commit(); err != nil {
			httperror.Write(w, r, httperror.Internal("set the age tier").Wrap(err))
			return
		}
		if t.kind != "" {
			h.Recompute.Wake()
			// The number is for the admin's eyes; the change is already stored and
			// queued, so a count that fails is logged and answered as 0.
			if count, err = h.Recompute.CountVideos(ctx, t.kind, id); err != nil {
				h.logger().Warn("parental: the videos of an age-tier change cannot be counted",
					"event", "parental.age_tier_count_failed", "error", err.Error())
			}
		}
		h.log(r, t.what, id, tier, count)
		out := TierResponse{RecomputeCount: count}
		if tier.Valid {
			n, s := int(tier.Int64), string(t.source)
			out.Tier, out.Source = &n, &s
		}
		common.WriteJSON(w, r, http.StatusOK, out)
	}
}

// Ratings implements GET /api/parental/ratings: the scale and every rating system's
// table, the file the API itself maps with, so no app keeps its own copy.
func (h *TiersHandler) Ratings(w http.ResponseWriter, r *http.Request) {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, tiersUnauthorized())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(parental.RatingsJSON())
}

func (h *TiersHandler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func (h *TiersHandler) log(r *http.Request, what, id string, tier sql.NullInt64, count int) {
	var t any
	if tier.Valid {
		t = tier.Int64
	}
	actor := ""
	if p := principal.FromContext(r.Context()); p != nil {
		actor = p.UserID
	}
	h.logger().Info("parental: age tier set", "event", "parental.age_tier_set",
		"on", what, "id", id, "tier", t, "videos", count, "actor", actor)
}

// tiersUnauthorized is the 401 of the age tier routes (accounts.go has the account
// routes' own, with another problem type).
func tiersUnauthorized() *httperror.Error {
	return &httperror.Error{Type: httperror.TypeForbidden, Title: "unauthorized",
		Status: http.StatusUnauthorized, Detail: "authentication required"}
}

// requireAdmin answers 401 to no one and 403 to anyone who is not a home admin (a
// viewer, and a guardian who is not an admin: looking after a child's account gives
// no say over the home's ratings).
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, tiersUnauthorized())
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request, param, what string) (string, bool) {
	id := chi.URLParam(r, param)
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest(what+" id must be a UUID"))
		return "", false
	}
	return id, true
}

// readTier reads {"tier": 0|9|13|16|18|null}. The key must be there: a body
// without it is a mistake, not a request to clear the tier.
func readTier(w http.ResponseWriter, r *http.Request) (sql.NullInt64, bool) {
	var body struct {
		Tier json.RawMessage `json:"tier"`
	}
	if e := common.ReadJSON(r, &body, 1024); e != nil {
		httperror.Write(w, r, e)
		return sql.NullInt64{}, false
	}
	raw := bytes.TrimSpace(body.Tier)
	if len(raw) == 0 {
		httperror.Write(w, r, httperror.BadRequest("tier is required: 0, 9, 13, 16, 18 or null"))
		return sql.NullInt64{}, false
	}
	if bytes.Equal(raw, []byte("null")) {
		return sql.NullInt64{}, true
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil || !parental.ValidTier(n) {
		httperror.Write(w, r, httperror.BadRequest("tier must be 0, 9, 13, 16, 18 or null"))
		return sql.NullInt64{}, false
	}
	return sql.NullInt64{Int64: int64(n), Valid: true}, true
}
