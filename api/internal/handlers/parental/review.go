// This file is story 57.17's: the review list of the tiers the content screen
// detected from transcripts, and the admin's answer to one of them.
//
//	GET  /api/parental/review              admin: every library; a guardian: the libraries
//	                                       their children can read; anyone else 403
//	POST /api/parental/review/{video_id}   admin
//
// A detected tier is a lower-trust input: it rates a video only at or above the
// home's confidence line and only to tighten (internal/parental.Resolve). The list
// is what an admin has not decided yet: a signal under the line, a detected tier
// nobody confirmed, and a video the screen could not check. The admin's answer is
// stored as the admin's own tier on the video (the first step of Resolve) and marks
// the signal reviewed, after which it rates nothing.
package parental

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Review states: why a video is in the list.
const (
	ReviewLowConfidence = "low_confidence" // a signal under the line: it rates nothing
	ReviewUnconfirmed   = "unconfirmed"    // a detected tier at or above the line, no admin's answer yet
	ReviewNotChecked    = "not_checked"    // no transcript, or a language the screen does not read
)

const (
	reviewDefaultLimit = 50
	reviewMaxLimit     = 200
)

// ReviewHandler serves the review routes.
type ReviewHandler struct {
	DB *sql.DB
	// Recompute is the one writer of a video's tier.
	Recompute *parental.Recomputer
	Logger    *slog.Logger
}

// Mount registers the routes.
func (h *ReviewHandler) Mount(r chi.Router) {
	r.Get("/api/parental/review", h.List)
	r.Post("/api/parental/review/{video_id}", h.Decide)
}

// ReviewItem is one video of the review list.
type ReviewItem struct {
	VideoID     string `json:"video_id"`
	Title       string `json:"title"`
	LibraryID   string `json:"library_id"`
	LibraryName string `json:"library_name"`
	State       string `json:"state"`
	// DetectedTier is what the screen read from the transcript; null when it could
	// not say or did not check.
	DetectedTier *int    `json:"detected_tier"`
	Confidence   float64 `json:"confidence"`
	// Categories maps a category to its level (0 none to 3 strong) and up to three
	// transcript times, in seconds, where it was heard.
	Categories json.RawMessage `json:"categories"`
	// NotChecked is why the screen did not check: no_transcript or
	// unsupported_language; null when it did.
	NotChecked *string `json:"not_checked"`
	Model      string  `json:"model"`
	// Tier and Source are what the video is rated now, from every source.
	Tier      *int      `json:"tier"`
	Source    *string   `json:"source"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ReviewList answers GET /api/parental/review.
type ReviewList struct {
	Items []ReviewItem `json:"items"`
	Total int          `json:"total"`
	// MinConfidence is the home's line (app_settings parental.detect_min_confidence).
	MinConfidence float64 `json:"min_confidence"`
	Limit         int     `json:"limit"`
	Offset        int     `json:"offset"`
}

// reviewWhere is the list: a signal no admin answered, on a video no admin rated,
// that is under the line ($1), carries a tier, or was not checked.
const reviewWhere = ` FROM video_content_signals cs
	JOIN videos v ON v.id = cs.video_id
	JOIN libraries l ON l.id = v.library_id
	WHERE cs.reviewed_at IS NULL AND v.age_tier_admin IS NULL AND v.deleted_at IS NULL
	  AND (cs.not_checked IS NOT NULL OR cs.tier IS NOT NULL OR cs.confidence < $1::real)`

// reviewState is the SQL of an item's state, with the line as $1.
const reviewState = `CASE WHEN cs.not_checked IS NOT NULL THEN 'not_checked'
	WHEN cs.tier IS NOT NULL AND cs.confidence >= $1::real THEN 'unconfirmed'
	ELSE 'low_confidence' END`

// List implements GET /api/parental/review.
func (h *ReviewHandler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, tiersUnauthorized())
		return
	}
	ctx := r.Context()
	// Who may read is asked of the database on every request: a guardian is whoever
	// parental_guardians names now, and an error is a refusal.
	guardian := false
	if !p.IsAdmin {
		restricted := false
		if _, err := uuid.Parse(p.UserID); err == nil {
			if err := h.DB.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM parental_guardians WHERE guardian_id = $1::uuid),
				        EXISTS (SELECT 1 FROM parental_policies WHERE user_id = $1::uuid)`,
				p.UserID).Scan(&guardian, &restricted); err != nil {
				httperror.Write(w, r, httperror.Internal("parental access").Wrap(err))
				return
			}
		}
		if restricted {
			httperror.Write(w, r, Restricted())
			return
		}
		if !guardian {
			httperror.Write(w, r, httperror.Forbidden("", "only an admin or a guardian can read the review list"))
			return
		}
	}
	q := r.URL.Query()
	limit, offset := reviewDefaultLimit, 0
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > reviewMaxLimit {
			httperror.Write(w, r, httperror.BadRequest("limit must be from 1 to "+strconv.Itoa(reviewMaxLimit)))
			return
		}
		limit = n
	}
	if s := q.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			httperror.Write(w, r, httperror.BadRequest("offset must be 0 or more"))
			return
		}
		offset = n
	}
	state := q.Get("state")
	switch state {
	case "", ReviewLowConfidence, ReviewUnconfirmed, ReviewNotChecked:
	default:
		httperror.Write(w, r, httperror.BadRequest("state must be low_confidence, unconfirmed or not_checked"))
		return
	}
	line, err := parental.DetectLine(ctx, h.DB)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read the confidence line").Wrap(err))
		return
	}
	where, args := reviewWhere, []any{line}
	if guardian {
		// the libraries the guardian's children can read; a restricted account is
		// never an admin, so library_acl is all it reads
		args = append(args, p.UserID)
		where += ` AND v.library_id IN (SELECT a.library_id FROM library_acl a
			JOIN parental_guardians g ON g.child_id = a.user_id WHERE g.guardian_id = $2::uuid)`
	}
	if state != "" {
		args = append(args, state)
		where += ` AND ` + reviewState + ` = $` + strconv.Itoa(len(args))
	}
	out := ReviewList{Items: []ReviewItem{}, MinConfidence: line, Limit: limit, Offset: offset}
	if err := h.DB.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&out.Total); err != nil {
		httperror.Write(w, r, httperror.Internal("count the review list").Wrap(err))
		return
	}
	args = append(args, limit, offset)
	rows, err := h.DB.QueryContext(ctx, `SELECT cs.video_id, COALESCE(NULLIF(v.title, ''), v.filename), v.library_id, l.name, `+
		reviewState+`, cs.tier, cs.confidence, cs.categories, cs.not_checked, cs.model, v.age_tier, v.age_tier_source, cs.updated_at`+
		where+` ORDER BY cs.updated_at DESC, cs.video_id LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read the review list").Wrap(err))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var (
			it             ReviewItem
			detected, tier sql.NullInt64
			notChecked     sql.NullString
			source         sql.NullString
			categories     []byte
		)
		if err := rows.Scan(&it.VideoID, &it.Title, &it.LibraryID, &it.LibraryName, &it.State, &detected, &it.Confidence,
			&categories, &notChecked, &it.Model, &tier, &source, &it.UpdatedAt); err != nil {
			httperror.Write(w, r, httperror.Internal("read the review list").Wrap(err))
			return
		}
		it.Categories = json.RawMessage(categories)
		if !json.Valid(categories) {
			it.Categories = json.RawMessage(`{}`)
		}
		if detected.Valid {
			n := int(detected.Int64)
			it.DetectedTier = &n
		}
		if tier.Valid {
			n := int(tier.Int64)
			it.Tier = &n
		}
		if notChecked.Valid {
			it.NotChecked = &notChecked.String
		}
		if source.Valid && source.String != "" {
			it.Source = &source.String
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		httperror.Write(w, r, httperror.Internal("read the review list").Wrap(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Decide implements POST /api/parental/review/{video_id} {"tier": 0|9|13|16|18|null}:
// the admin confirms the detected tier, gives another, or clears it (null). The tier
// becomes the admin's own on the video, the signal is marked reviewed, and the answer
// is what the video is rated now.
func (h *ReviewHandler) Decide(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r, "video_id", "video")
	if !ok {
		return
	}
	tier, ok := readTier(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	p := principal.FromContext(ctx)
	// the admin token is an admin with no user row: its review has no reviewer
	reviewer := sql.NullString{}
	if _, err := uuid.Parse(p.UserID); err == nil {
		reviewer = sql.NullString{String: p.UserID, Valid: true}
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("review the video").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM video_content_signals cs JOIN videos v ON v.id = cs.video_id
		WHERE cs.video_id = $1::uuid AND v.deleted_at IS NULL FOR UPDATE OF cs`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("review of video "+id))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("review the video").Wrap(err))
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE videos SET age_tier_admin = $1 WHERE id = $2::uuid`, tier, id); err != nil {
		httperror.Write(w, r, httperror.Internal("set the video's age tier").Wrap(err))
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE video_content_signals
		SET reviewed_at = now(), reviewed_by = (SELECT u.id FROM users u WHERE u.id = $2::uuid)
		WHERE video_id = $1::uuid`, id, reviewer); err != nil {
		httperror.Write(w, r, httperror.Internal("mark the signal reviewed").Wrap(err))
		return
	}
	// queued with the change, as PUT /api/videos/{id}/age-tier does: the video is
	// rated even if the answer below cannot be given
	if err := h.Recompute.Enqueue(ctx, tx, parental.KindVideo, id); err != nil {
		httperror.Write(w, r, httperror.Internal("queue the recompute").Wrap(err))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("review the video").Wrap(err))
		return
	}
	got, err := h.Recompute.RecomputeVideo(ctx, id)
	if err != nil {
		h.Recompute.Wake()
		httperror.Write(w, r, httperror.Internal("rate the video").Wrap(err))
		return
	}
	var t any
	if tier.Valid {
		t = tier.Int64
	}
	logger := h.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("parental: detected tier reviewed", "event", "parental.review_decided",
		"video", id, "tier", t, "actor", p.UserID)
	out := TierResponse{Tier: got.Tier, RecomputeCount: 1}
	if got.Source != parental.SourceNone {
		s := string(got.Source)
		out.Source = &s
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}
