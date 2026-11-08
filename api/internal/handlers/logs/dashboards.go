package logs

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

// maxOffline is the most offline machines the logs role takes (logindex.MaxOffline).
const maxOffline = 1000

// withOffline is the request as the logs role is asked it: on the Overview, offline= names
// the joined machines that are offline now, from the cluster (the home's to say: a machine
// that stopped sending keeps its last values in the store, so by them it ranked last). A
// caller's own offline= never reaches the role. Unreadable, it is left out and logged: the
// board still answers, ranked by values.
func (h *Handler) withOffline(r *http.Request, board string) *http.Request {
	q := r.URL.Query()
	_, had := q["offline"]
	if !had && (board != "overview" || h.Offline == nil) {
		return r
	}
	q.Del("offline")
	if board == "overview" && h.Offline != nil {
		ids, err := h.Offline(r.Context())
		if err != nil {
			slog.Default().Warn("dashboards: offline machines not read", "event", "dashboard_offline_failed",
				"err", err.Error())
		}
		if len(ids) > maxOffline {
			ids = ids[:maxOffline]
		}
		if len(ids) > 0 {
			q.Set("offline", strings.Join(ids, ","))
		}
	}
	r2 := r.Clone(r.Context())
	u := *r.URL
	u.RawQuery = q.Encode()
	r2.URL = &u
	return r2
}

// maxMachineName bounds a machine's name on the dashboards, in characters: a name is the
// user's text (up to 100 bytes on the Machines page), shown as a label and nothing else.
const maxMachineName = 64

// maxBoardBody bounds the Overview answer the API reads to name its machines (≤ 50 lines of
// ≤ 2,000 points and ≤ 1,000 machine ids are a few MB); a larger one passes as it came.
const maxBoardBody = 32 << 20

// machineName is a machine's name as the dashboards show it: control and bidirectional
// override characters dropped, spaces trimmed, at most maxMachineName characters (an
// ellipsis marks a cut); "" when nothing is left.
func machineName(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxMachineName {
		s = strings.TrimSpace(string([]rune(s)[:maxMachineName-1])) + "…"
	}
	return s
}

// bufferWriter keeps a proxied answer so the API can add to it.
type bufferWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferWriter) Header() http.Header { return b.header }
func (b *bufferWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}
func (b *bufferWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// withNames is the Overview answer with names: {<machine id>: <name>} for every machine id
// the answer mentions (the one asked for, machines.all, shown and offline, and each line's
// machine= label), as the Machines page names them (h.Names, from the cluster). The log
// index knows only ids. An id with no name, or every id when the cluster can't be read, is
// named by itself. An answer that is not a 200 JSON object comes back unchanged.
func (h *Handler) withNames(r *http.Request, body []byte) []byte {
	var doc map[string]json.RawMessage
	if json.Unmarshal(body, &doc) != nil || doc == nil {
		return body
	}
	var part struct {
		Machine  string `json:"machine"`
		Machines *struct {
			Shown, All, Offline []string
		} `json:"machines"`
		Series []struct {
			Labels string `json:"labels"`
		} `json:"series"`
	}
	if json.Unmarshal(body, &part) != nil {
		return body
	}
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && len(id) <= maxMachineIDLen && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	add(part.Machine)
	if m := part.Machines; m != nil {
		for _, l := range [][]string{m.All, m.Shown, m.Offline} {
			for _, id := range l {
				add(id)
			}
		}
	}
	for _, s := range part.Series {
		for _, kv := range strings.Split(s.Labels, ",") {
			if v, ok := strings.CutPrefix(kv, "machine="); ok {
				add(v)
			}
		}
	}
	got, err := h.Names(r.Context(), ids)
	if err != nil {
		slog.Default().Warn("dashboards: machine names not read", "event", "dashboard_names_failed",
			"err", err.Error())
		got = nil
	}
	names := make(map[string]string, len(ids))
	for _, id := range ids {
		if n := machineName(got[id]); n != "" {
			names[id] = n
		} else {
			names[id] = id
		}
	}
	raw, err := json.Marshal(names)
	if err != nil {
		return body
	}
	doc["names"] = raw
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}

// maxMachineIDLen is the longest machine id the API names (logindex's machine id bound).
const maxMachineIDLen = 128

// overview proxies the Overview and adds its machines' names (withNames).
func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	buf := &bufferWriter{header: http.Header{}}
	h.proxyIndex("/dashboards/{board}", false)(buf, h.withOffline(r, "overview"))
	body := buf.body.Bytes()
	if buf.status == http.StatusOK && h.Names != nil && len(body) <= maxBoardBody &&
		strings.HasPrefix(buf.header.Get("Content-Type"), "application/json") {
		body = h.withNames(r, body)
		buf.header.Del("Content-Length")
	}
	for k, v := range buf.header {
		w.Header()[k] = v
	}
	if buf.status == 0 {
		buf.status = http.StatusOK
	}
	w.WriteHeader(buf.status)
	_, _ = w.Write(body)
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	switch board := chi.URLParam(r, "board"); board {
	case "overview":
		h.overview(w, r)
		return
	case "use":
	default:
		h.proxyIndex("/dashboards/{board}", false)(w, h.withOffline(r, board))
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
