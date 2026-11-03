package queues

// GET /api/queues/libraries/activity?days=14 — each library's work done per day:
// the jobs that finished done or failed on each UTC day of the window, oldest
// first. The library dashboard draws it as each tile's trend and the library
// page as its activity chart. One grouped scan of the finished jobs, kept for a
// minute per window; admin-only like every route of the package.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	activityDefaultDays = 14
	activityMaxDays     = 60
	activityCacheTTL    = time.Minute
)

const libraryActivitySQL = `
SELECT library_id::text, (finished_at AT TIME ZONE 'UTC')::date,
       count(*) FILTER (WHERE state = 'done'), count(*) FILTER (WHERE state = 'failed')
  FROM processing_jobs
 WHERE library_id IS NOT NULL AND finished_at >= $1 AND state IN ('done', 'failed')
 GROUP BY 1, 2`

// LibraryActivity is one library's finished jobs per day, aligned with Days.
type LibraryActivity struct {
	LibraryID string `json:"library_id"`
	Done      []int  `json:"done"`
	Failed    []int  `json:"failed"`
}

// LibraryActivityResponse is the window's days (YYYY-MM-DD, UTC) and every
// library that finished a job in it.
type LibraryActivityResponse struct {
	Days  []string          `json:"days"`
	Items []LibraryActivity `json:"items"`
}

type activityCache struct {
	at  time.Time
	out LibraryActivityResponse
}

// LibraryActivity implements GET /api/queues/libraries/activity.
func (h *Handler) LibraryActivity(w http.ResponseWriter, r *http.Request) {
	days := activityDefaultDays
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > activityMaxDays {
			httperror.Write(w, r, httperror.BadRequest("days must be 1 to 60"))
			return
		}
		days = n
	}
	now := h.now()
	h.libMu.Lock()
	if c, ok := h.actCache[days]; ok && now.Sub(c.at) < activityCacheTTL {
		h.libMu.Unlock()
		common.WriteJSON(w, r, http.StatusOK, c.out)
		return
	}
	h.libMu.Unlock()

	u := now.UTC()
	today := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	first := today.AddDate(0, 0, -(days - 1))
	out := LibraryActivityResponse{Days: make([]string, days), Items: []LibraryActivity{}}
	index := map[string]int{}
	for i := 0; i < days; i++ {
		d := first.AddDate(0, 0, i).Format("2006-01-02")
		out.Days[i] = d
		index[d] = i
	}
	rows, err := h.DB.QueryContext(r.Context(), libraryActivitySQL, first)
	if err != nil {
		writeErr(w, r, "library activity", err)
		return
	}
	defer rows.Close()
	byLib := map[string]int{}
	for rows.Next() {
		var lib string
		var day time.Time
		var done, failed int
		if err := rows.Scan(&lib, &day, &done, &failed); err != nil {
			writeErr(w, r, "library activity", err)
			return
		}
		i, ok := index[day.UTC().Format("2006-01-02")]
		if !ok {
			continue
		}
		k, seen := byLib[lib]
		if !seen {
			k = len(out.Items)
			byLib[lib] = k
			out.Items = append(out.Items, LibraryActivity{LibraryID: lib, Done: make([]int, days), Failed: make([]int, days)})
		}
		out.Items[k].Done[i] += done
		out.Items[k].Failed[i] += failed
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "library activity", err)
		return
	}
	h.libMu.Lock()
	if h.actCache == nil {
		h.actCache = map[int]activityCache{}
	}
	h.actCache[days] = activityCache{at: now, out: out}
	h.libMu.Unlock()
	common.WriteJSON(w, r, http.StatusOK, out)
}
