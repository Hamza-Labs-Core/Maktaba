package queues

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// GET /api/queues/running?host=<node id | home> — the jobs running now
// (claimed, running, resuming) across every queue, oldest first, with the
// worker that claimed each. host narrows them to one machine: "home" is
// the home's own workers (workers.node_id IS NULL), a cluster node id
// (Epic 31) the workers of that joined machine. The Machines page links
// here ("View in queues") instead of drawing per-job progress itself.

// HostHome selects the home's own workers in ?host=.
const HostHome = "home"

var nodeIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	hostHomeSQL = ` AND j.claimed_by IN (SELECT id FROM workers WHERE node_id IS NULL)`
	hostNodeSQL = ` AND j.claimed_by IN (SELECT id FROM workers WHERE node_id = $1::uuid)`
)

// Running implements GET /api/queues/running.
func (h *Handler) Running(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	q := jobRefQuery("") + runningJobsSQLTail
	args := []any{}
	switch {
	case host == "":
	case host == HostHome:
		q += hostHomeSQL
	case nodeIDRe.MatchString(host):
		q += hostNodeSQL
		args = append(args, strings.ToLower(host))
	default:
		httperror.Write(w, r, httperror.BadRequest(`host must be "home" or a machine id`))
		return
	}
	q += ` ORDER BY j.started_at NULLS LAST, j.id`
	jobs, err := queryJobRefs(r.Context(), h.DB, q, args...)
	if err != nil {
		writeErr(w, r, "running jobs", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"host": host, "jobs": jobs})
}
