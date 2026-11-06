package logs

import (
	"database/sql"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Dashboards (Epic 53 story 53.12): GET /api/admin/dashboards/{board}?range= answers a
// board's series in one call, admins only. Overview, Queue, Processing, Streaming and
// Storage come from the logs role's metric store; Use is counted here from watch_sessions
// (counts and titles only: never one person's history).

// useRanges are the Use board's ranges (its points are days).
var useRanges = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour, "1h": time.Hour}

type useSeries struct {
	Name   string       `json:"name"`
	Labels string       `json:"labels,omitempty"`
	Kind   string       `json:"kind"`
	Points [][2]float64 `json:"points"`
}

type useTop struct {
	Title string `json:"title"`
	Plays int    `json:"plays"`
}

type useDoc struct {
	Board  string      `json:"board"`
	Range  string      `json:"range"`
	Step   int64       `json:"step"`
	Series []useSeries `json:"series"`
	Top    []useTop    `json:"top"`
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "board") != "use" {
		h.proxyIndex("/dashboards/{board}", false)(w, r)
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "7d"
	}
	d, ok := useRanges[rng]
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("range"))
		return
	}
	doc := useDoc{Board: "use", Range: rng, Step: 86400, Series: []useSeries{}, Top: []useTop{}}
	if h.DB == nil {
		common.WriteJSON(w, r, http.StatusOK, doc)
		return
	}
	now := time.Now().UTC()
	since := now.Add(-d)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT started_at, COALESCE(platform, ''), user_id, video_id
		FROM watch_sessions WHERE started_at >= $1`, since)
	if err != nil {
		httperror.Write(w, r, err)
		return
	}
	plays := map[string]map[int64]int{} // platform → day → plays
	people := map[int64]map[string]bool{}
	byVideo := map[string]int{}
	for rows.Next() {
		var at time.Time
		var platform, user, video string
		if rows.Scan(&at, &platform, &user, &video) != nil {
			continue
		}
		day := at.UTC().Truncate(24 * time.Hour).Unix()
		if platform == "" {
			platform = "other"
		}
		if plays[platform] == nil {
			plays[platform] = map[int64]int{}
		}
		plays[platform][day]++
		if people[day] == nil {
			people[day] = map[string]bool{}
		}
		people[day][user] = true
		byVideo[video]++
	}
	_ = rows.Close()
	points := func(m map[int64]int) [][2]float64 {
		var days []int64
		for d := range m {
			days = append(days, d)
		}
		sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
		out := make([][2]float64, 0, len(days))
		for _, d := range days {
			out = append(out, [2]float64{float64(d), float64(m[d])})
		}
		return out
	}
	var platforms []string
	for p := range plays {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	for _, p := range platforms {
		doc.Series = append(doc.Series, useSeries{Name: "maktaba_plays_per_day", Labels: "platform=" + p, Kind: "gauge",
			Points: points(plays[p])})
	}
	ppl := map[int64]int{}
	for d, s := range people {
		ppl[d] = len(s)
	}
	doc.Series = append(doc.Series, useSeries{Name: "maktaba_people_per_day", Kind: "gauge", Points: points(ppl)})
	type vc struct {
		id string
		n  int
	}
	var vs []vc
	for id, n := range byVideo {
		vs = append(vs, vc{id, n})
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].n > vs[j].n || (vs[i].n == vs[j].n && vs[i].id < vs[j].id) })
	if len(vs) > 10 {
		vs = vs[:10]
	}
	for _, v := range vs {
		var t sql.NullString
		_ = h.DB.QueryRowContext(r.Context(), `SELECT title FROM videos WHERE id = $1`, v.id).Scan(&t)
		title := t.String
		if title == "" {
			title = v.id
		}
		doc.Top = append(doc.Top, useTop{Title: title, Plays: v.n})
	}
	common.WriteJSON(w, r, http.StatusOK, doc)
}
