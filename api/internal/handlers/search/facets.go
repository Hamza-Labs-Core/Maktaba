package search

// Facets and sort (Epic 49 story 49.8).
//
// Facets count the videos a search found: the union of its "In titles" and "In
// transcripts" lists, which are already the caller's (library filter, parental limit, the
// meaning search's hits kept to what the account may see). They come with the first page
// of a search. Each bucket says how to narrow the search to it: a library or language id,
// a speaker's name, a year's dates, a duration band's seconds (the request's filters).
//
// Sort orders a search's lists: relevance (the default), newest (the date a video shows:
// published, else added) or most matches (its transcript lines that match). The order is
// the kept search's, so every page of it follows.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sorts a search accepts.
const (
	SortRelevance = "relevance"
	SortNewest    = "newest"
	SortMatches   = "matches"
)

// facetVideos is how many of a search's videos the facets count at most (its lists are
// rules.search.max_videos long, 500 by default).
const facetVideos = 1000

// facetTop is how many buckets the speaker facet shows at most (the others are counted
// as none): a library with many speakers would otherwise send them all.
const facetTop = 20

// Bucket is one value of a facet and how many of the search's videos have it.
type Bucket struct {
	Value string `json:"value"`
	// Label is the library's name (library facet).
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
	// Filter narrows the search to this bucket (year: dates; duration: seconds).
	Filter *BucketFilter `json:"filter,omitempty"`
}

// BucketFilter is the request filter a year or duration bucket stands for.
type BucketFilter struct {
	Date        *RangeStr `json:"date,omitempty"`
	DurationSec *RangeF   `json:"duration_sec,omitempty"`
}

// Facets are a search's counts by library, language, speaker, year and duration band,
// each most first (year: newest first; duration: shortest first).
type Facets struct {
	Library  []Bucket `json:"library"`
	Language []Bucket `json:"language"`
	Speaker  []Bucket `json:"speaker"`
	Year     []Bucket `json:"year"`
	Duration []Bucket `json:"duration"`
	// Videos is how many videos were counted.
	Videos int `json:"videos"`
}

// durationBands are the duration facet's bands, in seconds: [gte, lt), lt 0 for none.
var durationBands = []struct {
	name    string
	gte, lt float64
}{
	{"under_5m", 0, 300},
	{"5m_20m", 300, 1200},
	{"20m_60m", 1200, 3600},
	{"over_60m", 3600, 0},
}

func f64(v float64) *float64 { return &v }

// resultIDs are a search's videos, titles and transcripts, once each, in list order.
func resultIDs(rn *run, max int) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if !seen[id] && len(out) < max {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, v := range rn.Set.Videos {
		add(v.V)
	}
	for _, t := range rn.Set.Titles {
		add(t.V)
	}
	return out
}

// canonIDs are the ids a query on videos may take: on Postgres, the uuids among them (a
// meaning search's id that is not one would fail the whole statement).
func (h *Handler) canonIDs(ids []string) []string {
	if isSQLite(h.DB) {
		return ids
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if u, err := uuid.Parse(id); err == nil {
			out = append(out, u.String())
		}
	}
	return out
}

// facets counts the search's videos.
func (h *Handler) facets(ctx context.Context, rn *run) (*Facets, error) {
	out := &Facets{Library: []Bucket{}, Language: []Bucket{}, Speaker: []Bucket{}, Year: []Bucket{}, Duration: []Bucket{}}
	if h.DB == nil {
		return out, nil
	}
	ids := h.canonIDs(resultIDs(rn, facetVideos))
	if len(ids) == 0 {
		return out, nil
	}
	idCol, published := "v.id::text", "COALESCE(v.published_at, v.created_at)"
	if isSQLite(h.DB) {
		idCol = "v.id"
	}
	var args []any
	in := placeholders(ids, &args)
	rows, err := h.DB.QueryContext(ctx, `SELECT `+idCol+`, `+strings.Replace(idCol, "v.id", "v.library_id", 1)+`, COALESCE(l.name, ''),
			COALESCE(v.detected_language, ''), `+published+`, v.duration_sec
		  FROM videos v LEFT JOIN libraries l ON l.id = v.library_id
		 WHERE v.deleted_at IS NULL AND v.id IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}
	type tally struct {
		label string
		n     int
	}
	lib, lang, year := map[string]*tally{}, map[string]int{}, map[int]int{}
	band := make([]int, len(durationBands))
	count := func(m map[string]*tally, k, label string) {
		if m[k] == nil {
			m[k] = &tally{label: label}
		}
		m[k].n++
	}
	for rows.Next() {
		var id, libID, libName, language string
		var shown any
		var dur sql.NullFloat64
		if err := rows.Scan(&id, &libID, &libName, &language, &shown, &dur); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("facets: %w", err)
		}
		out.Videos++
		count(lib, libID, libName)
		if language != "" {
			lang[language]++
		}
		if y, ok := yearOf(shown); ok {
			year[y]++
		}
		if dur.Valid {
			for i, b := range durationBands {
				if dur.Float64 >= b.gte && (b.lt == 0 || dur.Float64 < b.lt) {
					band[i]++
					break
				}
			}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("facets: %w", err)
	}
	for k, t := range lib {
		out.Library = append(out.Library, Bucket{Value: k, Label: t.label, Count: t.n})
	}
	for k, n := range lang {
		out.Language = append(out.Language, Bucket{Value: k, Count: n})
	}
	byCount(out.Library)
	byCount(out.Language)
	years := make([]int, 0, len(year))
	for y := range year {
		years = append(years, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	for _, y := range years {
		out.Year = append(out.Year, Bucket{Value: strconv.Itoa(y), Count: year[y], Filter: &BucketFilter{
			// whole UTC instants, as the year was counted (and as SQLite's ISO text sorts)
			Date: &RangeStr{Gte: fmt.Sprintf("%04d-01-01T00:00:00.000Z", y), Lt: fmt.Sprintf("%04d-01-01T00:00:00.000Z", y+1)}}})
	}
	for i, b := range durationBands {
		if band[i] == 0 {
			continue
		}
		r := &RangeF{Gte: f64(b.gte)}
		if b.lt > 0 {
			r.Lt = f64(b.lt)
		}
		out.Duration = append(out.Duration, Bucket{Value: b.name, Count: band[i], Filter: &BucketFilter{DurationSec: r}})
	}
	speakers, err := h.speakerFacet(ctx, ids)
	if err != nil {
		return nil, err
	}
	out.Speaker = speakers
	return out, nil
}

// speakerFacet counts the videos by their named speakers (a video counts once for each).
func (h *Handler) speakerFacet(ctx context.Context, ids []string) ([]Bucket, error) {
	var args []any
	in := placeholders(ids, &args)
	rows, err := h.DB.QueryContext(ctx, `SELECT s.name, count(DISTINCT s.video_id) AS n FROM speakers s
		 WHERE s.unknown_index IS NULL AND s.video_id IN (`+in+`) AND trim(s.name) <> ''
		 GROUP BY s.name ORDER BY n DESC, s.name LIMIT `+strconv.Itoa(facetTop), args...)
	if err != nil {
		return nil, fmt.Errorf("speaker facet: %w", err)
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Value, &b.Count); err != nil {
			return nil, fmt.Errorf("speaker facet: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// byCount orders buckets most first, then by value.
func byCount(bs []Bucket) {
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].Count != bs[j].Count {
			return bs[i].Count > bs[j].Count
		}
		return bs[i].Value < bs[j].Value
	})
}

// yearOf is the year of a date as the driver gives it (a time on Postgres, ISO text on
// SQLite).
func yearOf(v any) (int, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Year(), true
	case string:
		return yearOfText(t)
	case []byte:
		return yearOfText(string(t))
	}
	return 0, false
}

func yearOfText(s string) (int, bool) {
	if len(s) < 4 {
		return 0, false
	}
	y, err := strconv.Atoi(s[:4])
	return y, err == nil && y > 0
}

// sortRun orders a computed search's lists as asked (relevance: as they are).
func (h *Handler) sortRun(ctx context.Context, rn *run, q, how string) error {
	if h.DB == nil {
		return nil
	}
	switch how {
	case SortNewest:
		ids := h.canonIDs(resultIDs(rn, len(rn.Set.Videos)+len(rn.Set.Titles)))
		dates, err := h.shownDates(ctx, ids)
		if err != nil {
			return err
		}
		newer := func(a, b string) bool {
			da, db := dates[canonID(a)], dates[canonID(b)]
			if da != db {
				return da > db
			}
			return false
		}
		sort.SliceStable(rn.Set.Videos, func(i, j int) bool { return newer(rn.Set.Videos[i].V, rn.Set.Videos[j].V) })
		sort.SliceStable(rn.Set.Titles, func(i, j int) bool { return newer(rn.Set.Titles[i].V, rn.Set.Titles[j].V) })
	case SortMatches:
		n, err := h.countAllMatches(ctx, rn, q)
		if err != nil {
			return err
		}
		sort.SliceStable(rn.Set.Videos, func(i, j int) bool { return n[rn.Set.Videos[i].V] > n[rn.Set.Videos[j].V] })
	}
	return nil
}

func canonID(id string) string {
	if u, err := uuid.Parse(id); err == nil {
		return u.String()
	}
	return id
}

// shownDates are the videos' shown dates as sortable text (ISO, UTC).
func (h *Handler) shownDates(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	idCol := "v.id::text"
	if isSQLite(h.DB) {
		idCol = "v.id"
	}
	var args []any
	rows, err := h.DB.QueryContext(ctx, `SELECT `+idCol+`, COALESCE(v.published_at, v.created_at) FROM videos v
		 WHERE v.id IN (`+placeholders(ids, &args)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("sort: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var d any
		if err := rows.Scan(&id, &d); err != nil {
			return nil, fmt.Errorf("sort: %w", err)
		}
		switch t := d.(type) {
		case time.Time:
			out[canonID(id)] = t.UTC().Format("2006-01-02T15:04:05.000Z")
		case string:
			out[canonID(id)] = t
		case []byte:
			out[canonID(id)] = string(t)
		}
	}
	return out, rows.Err()
}

// countAllMatches is each transcript video's number of matching lines, by its id: what the search counted, and for a BM25 video (whose lines the page counts:
// L) its lines that match the query's words as written (the lines' full-text index, as
// the line path counts them). The page still shows its own count.
func (h *Handler) countAllMatches(ctx context.Context, rn *run, q string) (map[string]int, error) {
	out := make(map[string]int, len(rn.Set.Videos))
	var ids []string
	for _, v := range rn.Set.Videos {
		out[v.V] = v.N
		if v.L {
			ids = append(ids, v.V)
		}
	}
	ids = h.canonIDs(ids)
	if len(ids) == 0 {
		return out, nil
	}
	var args []any
	var query string
	if isSQLite(h.DB) {
		args = []any{"%" + escapeLike(strings.ToLower(q)) + "%"}
		query = `SELECT t.video_id, count(*) FROM transcript_segments s
			JOIN transcripts t ON t.id = s.transcript_id AND t.is_active
			WHERE lower(s.text) LIKE $1 ESCAPE '\' AND t.video_id IN (` + placeholders(ids, &args) + `)
			GROUP BY t.video_id`
	} else {
		// at most ftsCandidates matching lines in all, as the line path reads them: a
		// common word is in millions of lines, and counting them all was the 60 s search
		// this epic replaced. Past the cap the order is that of the lines read.
		args = []any{q}
		in := placeholders(ids, &args)
		args = append(args, ftsCandidates)
		query = `SELECT m.video_id::text, count(*) FROM (
				SELECT t.video_id FROM transcript_segments s
				JOIN transcripts t ON t.id = s.transcript_id AND t.is_active
				WHERE s.search_tsv @@ plainto_tsquery('maktaba_search', $1) AND t.video_id IN (` + in + `)
				LIMIT $` + strconv.Itoa(len(args)) + `
			) m GROUP BY m.video_id`
	}
	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("matches: %w", err)
	}
	defer rows.Close()
	n := map[string]int{}
	for rows.Next() {
		var id string
		var c int
		if err := rows.Scan(&id, &c); err != nil {
			return nil, fmt.Errorf("matches: %w", err)
		}
		n[canonID(id)] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("matches: %w", err)
	}
	for _, v := range rn.Set.Videos {
		if v.L {
			out[v.V] = n[canonID(v.V)]
		}
	}
	return out, nil
}
