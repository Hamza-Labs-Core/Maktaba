package queues

// GET /api/queues/fallbacks[?stage=]: how many jobs (or fingerprints) of each first-party
// stage ran in the pipeline worker instead of its plugin runner, per machine and reason
// (Epic 54, story 54.43). The workers add their counts to stage_fallbacks at each refresh;
// this reads them, newest first, with the machine's name. Admin only, like every queue
// route (Mount). Story 54.36 §4 (e) reads it: the in-process path is removed only after a
// full release with none counted on the live home.

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Fallback is one stage_fallbacks row.
type Fallback struct {
	// Machine is "home" or a cluster node's id; MachineName is the node's name (null for
	// the home, or a node no longer listed).
	Machine     string    `json:"machine"`
	MachineName *string   `json:"machine_name"`
	Stage       string    `json:"stage"`
	Reason      string    `json:"reason"`
	Count       int64     `json:"count"`
	FirstAt     time.Time `json:"first_at"`
	LastAt      time.Time `json:"last_at"`
}

// FallbacksResponse is what the route answers.
type FallbacksResponse struct {
	Fallbacks []Fallback `json:"fallbacks"`
	// Total is the sum of every listed row's count.
	Total int64 `json:"total"`
}

const fallbacksSQL = `
SELECT f.machine, cn.name, f.stage, f.reason, f.count, f.first_at, f.last_at
  FROM stage_fallbacks f
  LEFT JOIN cluster_nodes cn ON f.machine <> 'home' AND cn.id::text = f.machine
 WHERE ($1 = '' OR f.stage = $1)
 ORDER BY f.last_at DESC, f.stage, f.reason, f.machine`

// Fallbacks lists the counted in-process fallbacks.
func (h *Handler) Fallbacks(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	stage := r.URL.Query().Get("stage")
	rows, err := h.DB.QueryContext(r.Context(), fallbacksSQL, stage)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("stage fallbacks").Wrap(err))
		return
	}
	defer rows.Close()
	out := FallbacksResponse{Fallbacks: []Fallback{}}
	for rows.Next() {
		var f Fallback
		var name sql.NullString
		if err := rows.Scan(&f.Machine, &name, &f.Stage, &f.Reason, &f.Count, &f.FirstAt, &f.LastAt); err != nil {
			httperror.Write(w, r, httperror.Internal("stage fallbacks").Wrap(err))
			return
		}
		if components.RetiredStage(f.Stage) {
			continue // as every queue reader: a removed stage's rows are left out
		}
		f.MachineName = nullStrPtr(name)
		f.FirstAt, f.LastAt = f.FirstAt.UTC(), f.LastAt.UTC()
		out.Fallbacks = append(out.Fallbacks, f)
		out.Total += f.Count
	}
	if err := rows.Err(); err != nil {
		httperror.Write(w, r, httperror.Internal("stage fallbacks").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}
