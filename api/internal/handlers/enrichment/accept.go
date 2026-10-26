package enrichment

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

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
// and per-field provenance. action is "accept", or "auto_accept" for the
// review-queue sweep (autoaccept.go), whose request carries no principal.
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

	if action == "auto_accept" {
		// Claim the candidate first: a second sweep (another replica) racing
		// this one finds it already accepted and stops, so the recorded
		// previous values stay the originals Revert restores.
		claim, err := tx.ExecContext(r.Context(),
			`UPDATE media_metadata_enrichment SET is_accepted = true
			 WHERE video_id = $1 AND external_id = $2 AND is_accepted = false`, videoID, externalID)
		if err != nil {
			return res, httperror.Internal("claim candidate")
		}
		if n, err := claim.RowsAffected(); err != nil || n == 0 {
			return res, httperror.Conflict("", "candidate already accepted")
		}
	}

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
		httperror.Write(w, r, httperror.Internal("dismiss").Wrap(err))
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
		httperror.Write(w, r, httperror.Internal("load provenance").Wrap(err))
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

// searchRequest is the POST …/search body: the librarian's words and,
// optionally, a year. An empty query re-searches from the video's own
// intel (title, series, the book, the presenter).
type searchRequest struct {
	Query string `json:"query"`
	Year  *int   `json:"year,omitempty"`
}

// maxQueryRunes bounds a manual search (the pipeline cuts it at 150 too).
const maxQueryRunes = 200

// Search runs a manual search (Find metadata, stub audit #4). The metadata
// provider is the pipeline's `enrich` stage (internal intel plus, when
// configured, a web search a language model reads; no outside catalogue):
// this queues the video's enrich job with the query, enqueues the engine
// job so it runs now and shows in the control centre, clears dismissals
// (hidden candidates resurface) and answers 202 with the current
// candidates and the job. Nothing is applied to the video until an
// explicit accept (Story 26.6 AC); the panel polls GET …/enrichment until
// the job is done.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	var body searchRequest
	if e := common.ReadJSON(r, &body, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	body.Query = strings.TrimSpace(body.Query)
	var errs []httperror.FieldError
	if utf8.RuneCountInString(body.Query) > maxQueryRunes {
		errs = append(errs, httperror.FieldError{Field: "query", Message: "at most 200 characters"})
	}
	if body.Year != nil && (*body.Year < 1000 || *body.Year > 2200) {
		errs = append(errs, httperror.FieldError{Field: "year", Message: "a year such as 2019"})
	}
	if len(errs) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(errs))
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
	if _, err := h.loadVideo(r, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return
	}
	if _, err := h.DB.ExecContext(r.Context(),
		`UPDATE media_metadata_enrichment SET is_dismissed = false WHERE video_id = $1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("clear dismissals").Wrap(err))
		return
	}
	note, err := h.queueEnrich(r, id, "manual", body.Query, body.Year)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("queue search").Wrap(err))
		return
	}
	cands, err := h.loadCandidates(r, id, "")
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load candidates").Wrap(err))
		return
	}
	out := map[string]any{"queued": true, "candidates": cands, "job": h.jobStatus(r, id)}
	if note != "" {
		out["note"] = note
	}
	common.WriteJSON(w, r, http.StatusAccepted, out)
}

// Reenrich queues a fresh out-of-band enrich job (Story 26.7) and enqueues
// the engine's `enrich` stage for it, so it runs now and shows in the
// control centre (stub audit #4: nothing ran these jobs).
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
	_, _ = h.DB.ExecContext(r.Context(),
		`UPDATE media_metadata_enrichment SET is_dismissed = false WHERE video_id = $1`, id)
	note, err := h.queueEnrich(r, id, "reenrich", "", nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("queue enrich").Wrap(err))
		return
	}
	out := map[string]any{"enqueued": true, "job": h.jobStatus(r, id)}
	if note != "" {
		out["note"] = note
	}
	common.WriteJSON(w, r, http.StatusAccepted, out)
}

// queueEnrich makes the video's open enrich job carry this request (a
// waiting one is re-armed for now with the new query; a new one is
// inserted), then enqueues the engine job. The partial unique index keeps
// one open job per video, so a job already running keeps its query: the
// note says so. The engine enqueue is best effort: the home's sweep picks
// the queued row up within 30 s anyway.
func (h *Handler) queueEnrich(r *http.Request, videoID, origin, query string, year *int) (string, error) {
	ctx := r.Context()
	now := h.now()
	var y any
	if year != nil {
		y = *year
	}
	res, err := h.DB.ExecContext(ctx, `
		UPDATE enrich_jobs SET force = true, origin = $2, query = $3, year = $4,
		       status = 'pending', not_before = $5, updated_at = $5
		WHERE video_id = $1 AND status IN ('pending', 'deferred')`,
		videoID, origin, nullStr(query), y, now)
	if err != nil {
		return "", err
	}
	note := ""
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := h.DB.ExecContext(ctx, `
			INSERT INTO enrich_jobs (id, video_id, status, force, attempts, not_before, created_at, updated_at, origin, query, year)
			VALUES ($1,$2,'pending',true,0,$3,$3,$3,$4,$5,$6)`,
			uuid.NewString(), videoID, now, origin, nullStr(query), y); err != nil {
			// the open-video unique index: a job is running right now
			note = "a search for this video is running; its suggestions replace these when it finishes"
		}
	}
	if h.Enqueue != nil {
		if err := h.Enqueue(ctx, videoID, map[string]any{"origin": origin}); err != nil && h.Logger != nil {
			h.Logger.Warn("enrichment: engine enqueue failed; the sweep picks the job up",
				"video_id", videoID, "err", err)
		}
	}
	return note, nil
}

// JobStatus is the video's latest enrich job, for the panel's "searching…"
// state and the reason a web part waits (a daily cap, a quota).
type JobStatus struct {
	Status    string `json:"status"`
	Origin    string `json:"origin"`
	Query     string `json:"query,omitempty"`
	Year      *int   `json:"year,omitempty"`
	LastError string `json:"last_error,omitempty"`
	NotBefore string `json:"not_before,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// jobStatus returns the latest enrich job of a video, or nil.
func (h *Handler) jobStatus(r *http.Request, videoID string) *JobStatus {
	if h.DB == nil {
		return nil
	}
	var (
		j              JobStatus
		query, lastErr sql.NullString
		year           sql.NullInt64
		nb, up         any
	)
	if e := h.DB.QueryRowContext(r.Context(), `
		SELECT status, origin, query, year, last_error, not_before, updated_at
		FROM enrich_jobs WHERE video_id = $1 ORDER BY created_at DESC LIMIT 1`, videoID).
		Scan(&j.Status, &j.Origin, &query, &year, &lastErr, &nb, &up); e != nil {
		return nil
	}
	j.Query, j.LastError = query.String, lastErr.String
	if year.Valid {
		v := int(year.Int64)
		j.Year = &v
	}
	j.NotBefore, j.UpdatedAt = tsString(nb), tsString(up)
	return &j
}

// tsString formats a timestamp column whatever the driver hands back
// (time.Time on Postgres, text on SQLite).
func tsString(v any) string {
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case []byte:
		return string(t)
	case string:
		return t
	}
	return ""
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
		httperror.Write(w, r, httperror.Internal("load episodes").Wrap(err))
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
