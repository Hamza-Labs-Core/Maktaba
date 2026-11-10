package queues

// Run now (Epic 59 story 59.02): heavy work waits for the quiet hours of the machine that
// claimed it (the pipeline's engine/quiet.py gives it back, paused with the reason
// quiet_hours:<HH:MM>). An admin starts it at once:
//
//	POST /api/queues/jobs/{id}/run-now   one job
//	POST /api/libraries/{id}/run-now     every live job of a library (its first scan too)
//
// Both mark the jobs run_now (the gate lets them through) and clear the wait of those
// held for the quiet hours; a job waiting for another reason (a debounce, a retry) keeps
// it. Admins only, as every route of this package.

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// runNowSet is the SET clause both routes share.
const runNowSet = `UPDATE processing_jobs
   SET payload    = COALESCE(payload, '{}'::jsonb) || '{"run_now": true}'::jsonb,
       not_before = CASE WHEN paused_reason LIKE 'quiet_hours:%' THEN NULL ELSE not_before END,
       updated_at = $2
 WHERE state IN ('pending', 'paused')`

const runNowJobSQL = runNowSet + ` AND id = $1`

const runNowLibrarySQL = runNowSet + `
   AND (library_id = $1::uuid
        OR video_id IN (SELECT id FROM videos WHERE library_id = $1::uuid))`

// RunNowJob implements POST /api/queues/jobs/{id}/run-now.
func (h *Handler) RunNowJob(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httperror.Write(w, r, httperror.BadRequest("job id"))
		return
	}
	res, err := h.DB.ExecContext(r.Context(), runNowJobSQL, id, h.now())
	if err != nil {
		writeErr(w, r, "run now", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("waiting job "+strconv.FormatInt(id, 10)))
		return
	}
	h.publish(r.Context(), "queue.control", map[string]any{"run_now": id, "by": actor(r)})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"jobs": 1})
}

// RunNowLibrary implements POST /api/libraries/{id}/run-now.
func (h *Handler) RunNowLibrary(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	lib := chi.URLParam(r, "id")
	if _, err := uuid.Parse(lib); err != nil {
		httperror.Write(w, r, httperror.BadRequest("library id"))
		return
	}
	res, err := h.DB.ExecContext(r.Context(), runNowLibrarySQL, lib, h.now())
	if err != nil {
		writeErr(w, r, "run now", err)
		return
	}
	n, _ := res.RowsAffected()
	h.publish(r.Context(), "queue.control", map[string]any{"run_now_library": lib, "by": actor(r)})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"jobs": n})
}
