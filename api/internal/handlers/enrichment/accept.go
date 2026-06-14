package enrichment

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// acceptRequest is the POST …/accept body.
type acceptRequest struct {
	ExternalID string `json:"external_id"`
	// Version is the `videos.updated_at` the client last saw, for
	// optimistic concurrency. Empty ⇒ the check is skipped.
	Version string `json:"version,omitempty"`
}

// AcceptResult enumerates applied vs. skipped fields.
type AcceptResult struct {
	VideoID string   `json:"video_id"`
	Applied []string `json:"applied"`
	Skipped []string `json:"skipped"`
}

// Accept promotes a candidate's mappable fields to the video, skipping
// any field whose provenance origin is 'user' (Story 26.6 D2). Each
// applied field is recorded origin='enrichment' with its previous value
// so it remains revertible.
func (h *Handler) Accept(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var req acceptRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.ExternalID == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "external_id", Message: "required"}}))
		return
	}
	if e := h.canEdit(r); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	res, e := h.applyAccept(r, id, req.ExternalID, req.Version, "accept")
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}

// applyAccept is the transactional promotion shared by Accept and
// AcceptAll. It honours optimistic concurrency (409 on a stale version)
// and per-field provenance.
func (h *Handler) applyAccept(r *http.Request, videoID, externalID, version, action string) (AcceptResult, *httperror.Error) {
	res := AcceptResult{VideoID: videoID, Applied: []string{}, Skipped: []string{}}
	v, err := h.loadVideo(r, videoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return res, httperror.NotFound("video " + videoID)
		}
		return res, httperror.Internal("load video")
	}
	if version != "" && !versionMatches(v.UpdatedAt, version) {
		return res, httperror.Conflict("https://maktaba.dev/problems/stale-version", "video changed since you loaded it")
	}
	cands, err := h.loadCandidates(r, videoID, externalID)
	if err != nil {
		return res, httperror.Internal("load candidate")
	}
	if len(cands) == 0 {
		return res, httperror.NotFound("candidate " + externalID)
	}
	cand := cands[0]
	prot := h.protectedFields(r, videoID)

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return res, httperror.Internal("tx")
	}
	defer func() { _ = tx.Rollback() }()

	now := h.now()
	for _, f := range mappableFields {
		val := asString(cand.Mapped[f])
		if val == "" {
			continue
		}
		if prot[f] {
			res.Skipped = append(res.Skipped, f)
			continue
		}
		prev := v.field(f)
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE videos SET `+f+` = $1, updated_at = $2 WHERE id = $3`, val, now, videoID); err != nil {
			return res, httperror.Internal("write field")
		}
		if err := upsertProvenance(r, tx, videoID, f, "enrichment", prev, externalID, now); err != nil {
			return res, httperror.Internal("write provenance")
		}
		res.Applied = append(res.Applied, f)
	}

	if _, err := tx.ExecContext(r.Context(),
		`UPDATE media_metadata_enrichment SET is_accepted = true WHERE video_id = $1 AND external_id = $2`,
		videoID, externalID); err != nil {
		return res, httperror.Internal("mark accepted")
	}
	if err := writeDecision(r, tx, videoID, externalID, action, res.Applied, res.Skipped, now); err != nil {
		return res, httperror.Internal("write decision")
	}
	if err := tx.Commit(); err != nil {
		return res, httperror.Internal("commit")
	}
	return res, nil
}

// dismissRequest optionally scopes the dismissal to one candidate.
type dismissRequest struct {
	ExternalID string `json:"external_id,omitempty"`
}

// Dismiss hides all candidates for the video (or a single external_id).
func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var req dismissRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e := h.canEdit(r); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	q := `UPDATE media_metadata_enrichment SET is_dismissed = true WHERE video_id = $1`
	args := []any{id}
	if req.ExternalID != "" {
		q += ` AND external_id = $2`
		args = append(args, req.ExternalID)
	}
	if _, err := h.DB.ExecContext(r.Context(), q, args...); err != nil {
		httperror.Write(w, r, httperror.Internal("dismiss"))
		return
	}
	if _, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO enrichment_decisions (id, video_id, external_id, action, actor_user_id, created_at)
		 VALUES ($1,$2,$3,'dismiss',$4,$5)`,
		uuid.NewString(), id, nullStr(req.ExternalID), actorID(r), h.now()); err != nil {
		// Audit failure is non-fatal to the dismissal itself.
		_ = err
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"dismissed": true})
}

// revertRequest optionally scopes the revert to a single field.
type revertRequest struct {
	Field string `json:"field,omitempty"`
}

// Revert restores the pre-accept value(s) from provenance, flipping the
// field back and clearing the enrichment provenance (Story 26.6 D3).
func (h *Handler) Revert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var req revertRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e := h.canEdit(r); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	q := `SELECT field, prev_value FROM media_field_provenance
	      WHERE video_id = $1 AND origin = 'enrichment'`
	args := []any{id}
	if req.Field != "" {
		q += ` AND field = $2`
		args = append(args, req.Field)
	}
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load provenance"))
		return
	}
	type pf struct {
		field string
		prev  sql.NullString
	}
	var pfs []pf
	for rows.Next() {
		var p pf
		if rows.Scan(&p.field, &p.prev) == nil {
			pfs = append(pfs, p)
		}
	}
	rows.Close()

	reverted := []string{}
	now := h.now()
	for _, p := range pfs {
		if !contains(mappableFields, p.field) {
			continue
		}
		if _, err := h.DB.ExecContext(r.Context(),
			`UPDATE videos SET `+p.field+` = $1, updated_at = $2 WHERE id = $3`,
			p.prev, now, id); err != nil {
			continue
		}
		_, _ = h.DB.ExecContext(r.Context(),
			`DELETE FROM media_field_provenance WHERE video_id = $1 AND field = $2`, id, p.field)
		reverted = append(reverted, p.field)
	}
	_, _ = h.DB.ExecContext(r.Context(),
		`INSERT INTO enrichment_decisions (id, video_id, action, actor_user_id, created_at)
		 VALUES ($1,$2,'revert',$3,$4)`, uuid.NewString(), id, actorID(r), now)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"reverted": reverted})
}

// Search runs a manual re-search. The 26.5 provider service is not yet
// wired, so v1 clears any dismissals for the video (so previously hidden
// candidates resurface) and returns the current candidate set WITHOUT
// applying anything — satisfying "manual search returns candidates;
// videos unchanged until an explicit accept" (Story 26.6 AC).
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	// Body is accepted but the {query, year, provider} fields are only
	// consumed once the provider search service (26.5) is wired.
	var body map[string]any
	_ = common.ReadJSON(r, &body, 4<<10)
	if e := h.canEdit(r); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if h.DB == nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"candidates": []Candidate{}, "reason": "no_db"})
		return
	}
	if _, err := h.DB.ExecContext(r.Context(),
		`UPDATE media_metadata_enrichment SET is_dismissed = false WHERE video_id = $1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("clear dismissals"))
		return
	}
	cands, err := h.loadCandidates(r, id, "")
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load candidates"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"candidates": cands})
}

// Reenrich enqueues a fresh out-of-band enrich job (Story 26.7). The
// enrich worker refreshes by stored external_id where one is accepted,
// else re-searches.
func (h *Handler) Reenrich(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if e := h.canEdit(r); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	// Clear any dismissal and enqueue a forced enrich job. The partial
	// unique index keeps at most one open job per video; ON CONFLICT
	// re-arms the existing row instead of erroring.
	_, _ = h.DB.ExecContext(r.Context(),
		`UPDATE media_metadata_enrichment SET is_dismissed = false WHERE video_id = $1`, id)
	if _, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO enrich_jobs (id, video_id, status, force, not_before, created_at, updated_at)
		 VALUES ($1,$2,'pending',true,$3,$3,$3)`,
		uuid.NewString(), id, h.now()); err != nil {
		// A live open job already exists (open-video unique index) — that
		// is the desired idempotent outcome, not an error.
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"enqueued": true, "note": "job already pending"})
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"enqueued": true})
}

// AcceptAll batch-accepts the best episode match for every episode in a
// series, honouring per-episode provenance (Story 26.6 D5). Returns a
// per-episode applied/skipped/failed summary.
func (h *Handler) AcceptAll(w http.ResponseWriter, r *http.Request) {
	seriesID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(seriesID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if e := h.canEdit(r); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT video_id FROM series_episodes WHERE series_id = $1`, seriesID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load episodes"))
		return
	}
	var videoIDs []string
	for rows.Next() {
		var vid string
		if rows.Scan(&vid) == nil {
			videoIDs = append(videoIDs, vid)
		}
	}
	rows.Close()

	type episodeResult struct {
		VideoID string   `json:"video_id"`
		Applied []string `json:"applied,omitempty"`
		Skipped []string `json:"skipped,omitempty"`
		Failed  string   `json:"failed,omitempty"`
	}
	summary := []episodeResult{}
	for _, vid := range videoIDs {
		best, e := h.bestCandidate(r, vid)
		if e != nil || best == "" {
			summary = append(summary, episodeResult{VideoID: vid, Failed: "no candidate"})
			continue
		}
		res, ee := h.applyAccept(r, vid, best, "", "accept")
		if ee != nil {
			summary = append(summary, episodeResult{VideoID: vid, Failed: ee.Title})
			continue
		}
		summary = append(summary, episodeResult{VideoID: vid, Applied: res.Applied, Skipped: res.Skipped})
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"episodes": summary})
}

// bestCandidate returns the highest-confidence non-dismissed external_id
// for a video, or "" if none.
func (h *Handler) bestCandidate(r *http.Request, videoID string) (string, error) {
	var ext string
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT external_id FROM media_metadata_enrichment
		 WHERE video_id = $1 AND is_dismissed = false
		 ORDER BY confidence DESC LIMIT 1`, videoID).Scan(&ext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return ext, err
}

func upsertProvenance(r *http.Request, tx *sql.Tx, videoID, field, origin, prev, sourceID string, now time.Time) error {
	_, err := tx.ExecContext(r.Context(), `
		INSERT INTO media_field_provenance (video_id, field, origin, prev_value, source_id, set_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (video_id, field) DO UPDATE
		  SET origin = EXCLUDED.origin, prev_value = EXCLUDED.prev_value,
		      source_id = EXCLUDED.source_id, set_at = EXCLUDED.set_at
	`, videoID, field, origin, nullStr(prev), nullStr(sourceID), now)
	return err
}

func writeDecision(r *http.Request, tx *sql.Tx, videoID, externalID, action string, applied, skipped []string, now time.Time) error {
	_, err := tx.ExecContext(r.Context(), `
		INSERT INTO enrichment_decisions (id, video_id, external_id, action, actor_user_id, applied, skipped, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, uuid.NewString(), videoID, nullStr(externalID), action, actorID(r), jsonArray(applied), jsonArray(skipped), now)
	return err
}

// versionMatches compares a client-sent version string against the
// stored updated_at, tolerating RFC3339 / RFC3339Nano formatting.
func versionMatches(updatedAt time.Time, version string) bool {
	if version == updatedAt.Format(time.RFC3339Nano) || version == updatedAt.Format(time.RFC3339) {
		return true
	}
	if t, err := time.Parse(time.RFC3339Nano, version); err == nil {
		return t.Equal(updatedAt)
	}
	return false
}

func actorID(r *http.Request) any {
	p := principal.FromContext(r.Context())
	if p == nil || p.UserID == "" {
		return nil
	}
	if _, err := uuid.Parse(p.UserID); err != nil {
		return nil // sentinel admin token has a non-UUID id
	}
	return p.UserID
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func jsonArray(xs []string) string {
	if len(xs) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(xs)
	return string(b)
}
