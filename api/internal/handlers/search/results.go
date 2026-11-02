package search

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// maxVideoPassages bounds the passages one video's list reads (a transcript
// of a few hours has a few thousand segments).
const maxVideoPassages = 10000

// VideoCard is how both lists show a video.
type VideoCard struct {
	VideoID     string   `json:"video_id"`
	Title       string   `json:"title"`
	DurationSec *float64 `json:"duration_sec,omitempty"`
	LibraryID   string   `json:"library_id"`
	LibraryName string   `json:"library_name"`
	PosterURL   string   `json:"poster_url"`
}

// TitleVideo is one video of "In titles". Titles exist from the first scan
// (or import); transcripts only once Whisper has run, so without this a fresh
// library finds nothing at all.
type TitleVideo struct {
	VideoCard
	// Score is the title's match quality (Postgres: trigram similarity).
	Score float64 `json:"score"`
	// InTranscripts: the video is in "In transcripts" too.
	InTranscripts bool `json:"in_transcripts"`
	// TitleSnippet is the title with <mark> around the query's words.
	TitleSnippet string `json:"title_snippet"`
}

// Passage is one matching transcript segment of a video.
type Passage struct {
	SegmentID int64   `json:"segment_id"`
	StartSec  float64 `json:"start_sec"`
	EndSec    float64 `json:"end_sec"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
}

// TranscriptVideo is one video of "In transcripts": one entry per video,
// whatever its number of matching passages.
type TranscriptVideo struct {
	VideoCard
	// BestScore is the video's match quality: its best passage's rank (FTS),
	// similarity (semantic), or the fused rank of the two (hybrid).
	BestScore float64 `json:"best_score"`
	// Matches counts the matching passages of the video's active transcript
	// (FTS: all of them; semantic: those among the top K).
	Matches int `json:"matches"`
	// Passages are the best rules.search.passages_per_video of them.
	Passages []Passage `json:"passages"`
	// InTitle: the video is in "In titles" too.
	InTitle bool `json:"in_title"`
}

// Response is the POST /api/search envelope: the first page of both lists.
type Response struct {
	// SearchID names the saved result set ("" when it is not kept: a caller
	// without a user, or save:false). Paging and passages read it.
	SearchID  string    `json:"search_id"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Q         string    `json:"q"`
	Mode      string    `json:"mode"`
	Filters   Filters   `json:"filters"`
	TookMs    ResponseT `json:"took_ms"`

	// Degraded is true when the semantic leg was abandoned (deadline
	// breach or backend error) and the request was served FTS-only
	// (Story 18.2 AC4 / HLB-333). Clients surface a "results may be
	// incomplete" banner. Omitted on the happy path.
	Degraded bool `json:"degraded,omitempty"`

	PageSize         int `json:"page_size"`
	PassagesPageSize int `json:"passages_page_size"`

	// Videos is "In titles", page 1.
	Videos      []TitleVideo `json:"videos"`
	VideosTotal int          `json:"videos_total"`
	// TranscriptVideos is "In transcripts", page 1.
	TranscriptVideos []TranscriptVideo `json:"transcript_videos"`
	TranscriptTotal  int               `json:"transcript_total"`

	// Hits and Total are deprecated: page 1's passages, best first, as the
	// pre-grouping flat list (at most the request's limit).
	Hits  []Hit `json:"hits"`
	Total int   `json:"total"`

	// YouTube is the optional "From YouTube" augmentation block (Story
	// 26.8). Present only when the caller passed include=["youtube"].
	YouTube *YTBlock `json:"youtube,omitempty"`
}

// savedSet is a search's ordered result, as search_runs keeps it.
type savedSet struct {
	Titles []savedTitle `json:"t"`
	Videos []savedVideo `json:"v"`
	// Sem are the meaning search's passages (semantic and hybrid), with their
	// text: their videos' passages are read from here, not from the index again.
	Sem []Hit `json:"s,omitempty"`
}

type savedTitle struct {
	V string  `json:"v"`
	S float64 `json:"s"`
}

type savedVideo struct {
	V string  `json:"v"`
	S float64 `json:"s"`
	N int     `json:"n"`
}

// run is one search: its query and its result set.
type run struct {
	ID        string
	Q         string
	Mode      string
	Filters   Filters
	Degraded  bool
	Set       savedSet
	ExpiresAt time.Time

	once     sync.Once
	inTitles map[string]bool
	inVideos map[string]bool
}

func (rn *run) index() {
	rn.once.Do(func() {
		rn.inTitles = make(map[string]bool, len(rn.Set.Titles))
		for _, t := range rn.Set.Titles {
			rn.inTitles[t.V] = true
		}
		rn.inVideos = make(map[string]bool, len(rn.Set.Videos))
		for _, v := range rn.Set.Videos {
			rn.inVideos[v.V] = true
		}
	})
}

func (rn *run) hasTitle(id string) bool { rn.index(); return rn.inTitles[id] }
func (rn *run) hasVideo(id string) bool { rn.index(); return rn.inVideos[id] }

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now()
}

// isSQLite reports whether db is the SQLite backend (no tsvector, no ILIKE).
func isSQLite(db *sql.DB) bool {
	return strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
}

// Search implements POST /api/search.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	var req Request
	if e := common.ReadJSON(r, &req, 32<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.tv/problems/invalid-query",
			Title:  "invalid query",
			Status: http.StatusBadRequest,
			Detail: "q must be non-empty",
		})
		return
	}
	if len(req.Q) > 1024 {
		req.Q = req.Q[:1024]
	}
	switch req.Mode {
	case "fts", "semantic", "hybrid":
	default:
		req.Mode = "hybrid"
	}
	if req.Limit <= 0 || req.Limit > 200 {
		req.Limit = 50
	}

	// Authz scope: non-admin restricted to their library set.
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	ctx := r.Context()
	rl := h.rules(ctx)
	if !p.AccessAllLibraries {
		// Intersect requested library filter with principal's libraries.
		allowed := map[string]bool{}
		for _, l := range p.Libraries {
			allowed[l] = true
		}
		if len(req.Filters.LibraryID) == 0 {
			req.Filters.LibraryID = p.Libraries
		} else {
			intersected := []string{}
			for _, lid := range req.Filters.LibraryID {
				if allowed[lid] {
					intersected = append(intersected, lid)
				}
			}
			req.Filters.LibraryID = intersected
		}
		if len(req.Filters.LibraryID) == 0 {
			common.WriteJSON(w, r, http.StatusOK, Response{
				Q: req.Q, Mode: req.Mode, Filters: req.Filters, Hits: []Hit{},
				Videos: []TitleVideo{}, TranscriptVideos: []TranscriptVideo{},
				PageSize: rl.pageSize(req.View), PassagesPageSize: rl.PassagesPageSize,
			})
			return
		}
	}

	rn, took, err := h.compute(ctx, req, rl)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("").Wrap(err))
		return
	}
	if h.DB != nil && p.UserID != "" && req.save() {
		if err := h.saveRun(ctx, p.UserID, rn, rl); err != nil {
			// The results are still answered; they just cannot be paged by id.
			h.log().WarnContext(r.Context(), "search: could not keep the results", "event", "search_save_failed", "err", err)
			rn.ID = ""
		}
	}

	pageSize := rl.pageSize(req.View)
	titles, err := h.titlePage(ctx, p, rn, 0, pageSize)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("titles").Wrap(err))
		return
	}
	videos, err := h.transcriptPage(ctx, p, rn, 0, pageSize, rl.PassagesPerVideo)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("passages").Wrap(err))
		return
	}
	hits := flatHits(videos, req.Limit)
	resp := Response{
		SearchID: rn.ID, Q: req.Q, Mode: req.Mode, Filters: req.Filters, TookMs: took,
		Degraded: rn.Degraded, PageSize: pageSize, PassagesPageSize: rl.PassagesPageSize,
		Videos: titles, VideosTotal: len(rn.Set.Titles),
		TranscriptVideos: videos, TranscriptTotal: len(rn.Set.Videos),
		Hits: hits, Total: len(hits),
	}
	if rn.ID != "" {
		resp.ExpiresAt = rn.ExpiresAt.UTC()
	}
	// Story 26.8: the YouTube augmentation is purely additive and never
	// blocks or mutates the local results computed above.
	if req.includes("youtube") {
		resp.YouTube = h.fetchYouTube(ctx, req.Q, youtubeInlineTimeout)
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// flatHits is the deprecated flat list: the page's passages, best first.
func flatHits(videos []TranscriptVideo, limit int) []Hit {
	out := []Hit{}
	for _, v := range videos {
		for _, p := range v.Passages {
			out = append(out, Hit{
				SegmentID: p.SegmentID, VideoID: v.VideoID, StartSec: p.StartSec,
				EndSec: p.EndSec, Snippet: p.Snippet, Score: p.Score,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// compute runs a search: the title matches, the transcript matches grouped
// per video (the legs run at the same time), fused for hybrid.
func (h *Handler) compute(ctx context.Context, req Request, rl Rules) (*run, ResponseT, error) {
	rn := &run{Q: req.Q, Mode: req.Mode, Filters: req.Filters}
	var (
		took             ResponseT
		wg               sync.WaitGroup
		titles           []savedTitle
		ftsVideos        []savedVideo
		sem              []Hit
		errTitles, errFT error
	)
	t0 := time.Now()
	if h.DB != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer safego.Recover(nil, "search_titles")
			errTitles = errSearchPanicked
			titles, errTitles = h.runTitles(ctx, req.Q, rl.MaxVideos, req.Filters)
		}()
		if req.Mode == "fts" || req.Mode == "hybrid" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer safego.Recover(nil, "search_fts")
				errFT = errSearchPanicked
				ftsVideos, errFT = h.runFTSVideos(ctx, req.Q, rl.MaxVideos, req.Filters)
				took.FTS = time.Since(t0).Milliseconds()
			}()
		}
	}
	if (req.Mode == "semantic" || req.Mode == "hybrid") && h.Semantic != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer safego.Recover(nil, "search_semantic")
			t1 := time.Now()
			sem, rn.Degraded = h.runSemantic(ctx, req.Q, rl.SemanticTopK, req.Filters)
			took.Semantic = time.Since(t1).Milliseconds()
		}()
	}
	wg.Wait()
	if errTitles != nil {
		return nil, took, fmt.Errorf("titles: %w", errTitles)
	}
	if errFT != nil {
		return nil, took, fmt.Errorf("fts: %w", errFT)
	}

	t2 := time.Now()
	if titles == nil {
		titles = []savedTitle{}
	}
	rn.Set.Titles = titles
	rn.Set.Sem = sem
	switch req.Mode {
	case "fts":
		rn.Set.Videos = ftsVideos
	case "semantic":
		rn.Set.Videos = groupSemantic(sem)
	default:
		matched, err := h.ftsMatching(ctx, req.Q, sem)
		if err != nil {
			return nil, took, fmt.Errorf("hybrid: %w", err)
		}
		wFTS, wSem := h.weights(ctx)
		rn.Set.Videos = fuseVideos(ftsVideos, sem, matched, wFTS, wSem)
	}
	if rn.Set.Videos == nil {
		rn.Set.Videos = []savedVideo{}
	}
	if err := h.tiesByAdded(ctx, rn); err != nil {
		return nil, took, fmt.Errorf("ties: %w", err)
	}
	if len(rn.Set.Videos) > rl.MaxVideos {
		rn.Set.Videos = rn.Set.Videos[:rl.MaxVideos]
	}
	took.Fusion = time.Since(t2).Milliseconds()
	return rn, took, nil
}

// sortVideos orders videos by match quality, then by their number of matches.
func sortVideos(vs []savedVideo) {
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].S != vs[j].S {
			return vs[i].S > vs[j].S
		}
		if vs[i].N != vs[j].N {
			return vs[i].N > vs[j].N
		}
		return vs[i].V < vs[j].V
	})
}

// tiesByAdded orders the matches of equal quality (and, for the transcript
// matches, of as many matches) newest-added first, then by id: a search
// ranks by relevance, and its ties follow the order of every list of videos.
func (h *Handler) tiesByAdded(ctx context.Context, rn *run) error {
	if h.DB == nil || (len(rn.Set.Titles) == 0 && len(rn.Set.Videos) == 0) {
		return nil
	}
	seen := map[string]bool{}
	ids := []string{}
	// on Postgres a semantic hit's id that is no UUID would fail the whole
	// statement: it is left out and ends its ties
	pg := !isSQLite(h.DB)
	for _, t := range rn.Set.Titles {
		if !seen[t.V] {
			seen[t.V] = true
			ids = append(ids, t.V)
		}
	}
	for _, v := range rn.Set.Videos {
		if !seen[v.V] {
			seen[v.V] = true
			ids = append(ids, v.V)
		}
	}
	if pg {
		kept := ids[:0]
		for _, id := range ids {
			if _, err := uuid.Parse(id); err == nil {
				kept = append(kept, id)
			}
		}
		ids = kept
	}
	if len(ids) == 0 {
		return nil
	}
	idCol := "id::text"
	if isSQLite(h.DB) {
		idCol = "id"
	}
	var args []any
	rows, err := h.DB.QueryContext(ctx, `SELECT `+idCol+` FROM videos WHERE id IN (`+placeholders(ids, &args)+`)
		ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	newer := make(map[string]int, len(ids)) // 0 is the most recently added
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		newer[id] = len(newer)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// a video gone since is last among its ties
	at := func(id string) int {
		if n, ok := newer[id]; ok {
			return n
		}
		return len(ids)
	}
	ts := rn.Set.Titles
	sort.SliceStable(ts, func(i, j int) bool {
		if ts[i].S != ts[j].S {
			return ts[i].S > ts[j].S
		}
		return at(ts[i].V) < at(ts[j].V)
	})
	vs := rn.Set.Videos
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].S != vs[j].S {
			return vs[i].S > vs[j].S
		}
		if vs[i].N != vs[j].N {
			return vs[i].N > vs[j].N
		}
		return at(vs[i].V) < at(vs[j].V)
	})
	return nil
}

// groupSemantic groups the meaning search's passages per video: its best
// similarity and its number of passages among the top K.
func groupSemantic(sem []Hit) []savedVideo {
	byVideo := map[string]*savedVideo{}
	out := []savedVideo{}
	order := []string{}
	for _, hit := range sem {
		v, ok := byVideo[hit.VideoID]
		if !ok {
			v = &savedVideo{V: hit.VideoID, S: hit.Score}
			byVideo[hit.VideoID] = v
			order = append(order, hit.VideoID)
		}
		v.N++
		if hit.Score > v.S {
			v.S = hit.Score
		}
	}
	for _, id := range order {
		out = append(out, *byVideo[id])
	}
	sortVideos(out)
	return out
}

// fuseVideos is the hybrid list: each video scores wFTS/(60+rank) in the FTS
// video ranking plus wSem/(60+rank) in the semantic one (weighted RRF, as the
// passages are fused). Its matches are its FTS matches plus its semantic
// passages the FTS did not match (matched: those it did).
func fuseVideos(fts []savedVideo, sem []Hit, matched map[int64]bool, wFTS, wSem float64) []savedVideo {
	const k = 60
	semVideos := groupSemantic(sem)
	extra := map[string]int{}
	for _, hit := range sem {
		if !matched[hit.SegmentID] {
			extra[hit.VideoID]++
		}
	}
	byVideo := map[string]*savedVideo{}
	order := []string{}
	get := func(id string) *savedVideo {
		v, ok := byVideo[id]
		if !ok {
			v = &savedVideo{V: id}
			byVideo[id] = v
			order = append(order, id)
		}
		return v
	}
	for rank, v := range fts {
		f := get(v.V)
		f.S += wFTS / float64(k+rank+1)
		f.N += v.N
	}
	for rank, v := range semVideos {
		f := get(v.V)
		f.S += wSem / float64(k+rank+1)
		f.N += extra[v.V]
	}
	out := make([]savedVideo, 0, len(order))
	for _, id := range order {
		out = append(out, *byVideo[id])
	}
	sortVideos(out)
	return out
}

// whereVideos adds the library and language filters on videos v.
func whereVideos(f Filters, args *[]any) []string {
	where := []string{}
	if len(f.LibraryID) > 0 {
		ph := make([]string, len(f.LibraryID))
		for i, lid := range f.LibraryID {
			*args = append(*args, lid)
			ph[i] = "$" + strconv.Itoa(len(*args))
		}
		where = append(where, "v.library_id IN ("+strings.Join(ph, ",")+")")
	}
	if len(f.Language) > 0 {
		ph := make([]string, len(f.Language))
		for i, l := range f.Language {
			*args = append(*args, l)
			ph[i] = "$" + strconv.Itoa(len(*args))
		}
		where = append(where, "v.detected_language IN ("+strings.Join(ph, ",")+")")
	}
	return where
}

// placeholders appends vals to args and returns their "$n" list.
func placeholders[T any](vals []T, args *[]any) string {
	ph := make([]string, len(vals))
	for i, v := range vals {
		*args = append(*args, v)
		ph[i] = "$" + strconv.Itoa(len(*args))
	}
	return strings.Join(ph, ",")
}

// sqliteScore is SQLite's stand-in for ts_rank: how many times the query
// occurs in the segment ($2 is the lowercased query).
const sqliteScore = `((length(lower(s.text)) - length(replace(lower(s.text), $2, ''))) * 1.0 / max(length($2), 1))`

// ftsCandidates caps the matching segments one keyword search ranks. A word in
// millions of segments (الله in a lecture library) would otherwise rank them all
// and take a minute; a rarer word has fewer matches and loses none.
var ftsCandidates = 5000

// runFTSVideos groups the full-text matches per video: the matching segments of
// every video's active transcript are counted (up to ftsCandidates of them), the
// best rank is the video's match quality. Postgres: the GIN index on
// search_tsv finds the segments; SQLite: LIKE on the text.
func (h *Handler) runFTSVideos(ctx context.Context, q string, limit int, f Filters) ([]savedVideo, error) {
	var query string
	var args []any
	if isSQLite(h.DB) {
		args = []any{"%" + escapeLike(strings.ToLower(q)) + "%", strings.ToLower(q)}
		where := append([]string{`lower(s.text) LIKE $1 ESCAPE '\'`}, whereVideos(f, &args)...)
		args = append(args, limit)
		query = `
			SELECT t.video_id, count(*), max(` + sqliteScore + `) AS best
			FROM transcript_segments s
			JOIN transcripts t ON t.id = s.transcript_id AND t.is_active
			JOIN videos v ON v.id = t.video_id AND v.deleted_at IS NULL
			WHERE ` + strings.Join(where, " AND ") + `
			GROUP BY t.video_id
			ORDER BY best DESC, count(*) DESC, max(v.created_at) DESC, t.video_id
			LIMIT $` + strconv.Itoa(len(args))
	} else {
		args = []any{q}
		where := append([]string{`s.search_tsv @@ plainto_tsquery('maktaba_search', $1)`}, whereVideos(f, &args)...)
		args = append(args, limit)
		args = append(args, ftsCandidates)
		query = `
			SELECT m.video_id::text, count(*),
			       max(ts_rank(m.search_tsv, plainto_tsquery('maktaba_search', $1))) AS best
			FROM (
				SELECT t.video_id, v.created_at, s.search_tsv
				FROM transcript_segments s
				JOIN transcripts t ON t.id = s.transcript_id AND t.is_active
				JOIN videos v ON v.id = t.video_id AND v.deleted_at IS NULL
				WHERE ` + strings.Join(where, " AND ") + `
				LIMIT $` + strconv.Itoa(len(args)) + `
			) m
			GROUP BY m.video_id
			ORDER BY best DESC, count(*) DESC, max(m.created_at) DESC, m.video_id
			LIMIT $` + strconv.Itoa(len(args)-1)
	}
	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []savedVideo{}
	for rows.Next() {
		var v savedVideo
		var best sql.NullFloat64
		if err := rows.Scan(&v.V, &v.N, &best); err != nil {
			return nil, err
		}
		v.S = best.Float64
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortVideos(out) // already in this order; ties stay put
	return out, nil
}

// ftsMatching is which of the meaning search's passages the full-text query
// matches too (they count once in a hybrid video's matches).
func (h *Handler) ftsMatching(ctx context.Context, q string, sem []Hit) (map[int64]bool, error) {
	out := map[int64]bool{}
	if h.DB == nil || len(sem) == 0 {
		return out, nil
	}
	ids := make([]int64, len(sem))
	for i, hit := range sem {
		ids[i] = hit.SegmentID
	}
	var args []any
	var query string
	if isSQLite(h.DB) {
		args = []any{"%" + escapeLike(strings.ToLower(q)) + "%"}
		query = `SELECT id FROM transcript_segments s WHERE lower(s.text) LIKE $1 ESCAPE '\' AND s.id IN (` + placeholders(ids, &args) + `)`
	} else {
		args = []any{q}
		query = `SELECT id FROM transcript_segments s WHERE s.search_tsv @@ plainto_tsquery('maktaba_search', $1) AND s.id IN (` + placeholders(ids, &args) + `)`
	}
	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// runTitles matches video titles by substring (case-insensitive), most similar
// first, then the most recently added. On Postgres the trigram index (0098) serves both the ILIKE and the
// ordering; SQLite scans with LIKE, shortest title first.
func (h *Handler) runTitles(ctx context.Context, q string, limit int, f Filters) ([]savedTitle, error) {
	args := []any{"%" + escapeLike(q) + "%", q}
	where := append([]string{"v.deleted_at IS NULL"}, whereVideos(f, &args)...)
	args = append(args, limit)
	var query string
	if isSQLite(h.DB) {
		query = `SELECT v.id, 1.0 / (1 + length(v.title)) + 0 * length($2) AS score FROM videos v
			WHERE v.title LIKE $1 ESCAPE '\' AND ` + strings.Join(where, " AND ") + `
			ORDER BY length(v.title), v.created_at DESC, v.id LIMIT $` + strconv.Itoa(len(args))
	} else {
		query = `SELECT v.id::text, similarity(v.title, $2) AS score FROM videos v
			WHERE v.title ILIKE $1 ESCAPE '\' AND ` + strings.Join(where, " AND ") + `
			ORDER BY score DESC, v.created_at DESC, v.id LIMIT $` + strconv.Itoa(len(args))
	}
	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []savedTitle{}
	for rows.Next() {
		var t savedTitle
		if err := rows.Scan(&t.V, &t.S); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// escapeLike makes q literal inside a LIKE pattern (ESCAPE '\\').
func escapeLike(q string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q)
}

// cards reads the videos' cards, leaving out a video deleted since the search
// or in a library the caller may no longer see (the saved set was made with
// the caller's libraries; they are checked again on every read).
func (h *Handler) cards(ctx context.Context, p *principal.Principal, ids []string) (map[string]VideoCard, error) {
	out := make(map[string]VideoCard, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if h.DB == nil {
		for _, id := range ids {
			out[id] = VideoCard{VideoID: id, PosterURL: posterURL(id)}
		}
		return out, nil
	}
	var args []any
	where := []string{"v.id IN (" + placeholders(ids, &args) + ")", "v.deleted_at IS NULL"}
	if p != nil && !p.AccessAllLibraries {
		if len(p.Libraries) == 0 {
			return out, nil
		}
		where = append(where, "v.library_id IN ("+placeholders(p.Libraries, &args)+")")
	}
	idCol, libCol := "v.id::text", "COALESCE(v.library_id::text, '')"
	if isSQLite(h.DB) {
		idCol, libCol = "v.id", "COALESCE(v.library_id, '')"
	}
	rows, err := h.DB.QueryContext(ctx, `
		SELECT `+idCol+`, COALESCE(NULLIF(v.title, ''), v.filename, ''), v.duration_sec,
		       `+libCol+`, COALESCE(l.name, '')
		FROM videos v LEFT JOIN libraries l ON l.id = v.library_id
		WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c VideoCard
		var dur sql.NullFloat64
		if err := rows.Scan(&c.VideoID, &c.Title, &dur, &c.LibraryID, &c.LibraryName); err != nil {
			return nil, err
		}
		if dur.Valid {
			d := dur.Float64
			c.DurationSec = &d
		}
		c.PosterURL = posterURL(c.VideoID)
		out[c.VideoID] = c
	}
	return out, rows.Err()
}

func posterURL(id string) string { return "/api/videos/" + id + "/poster" }

// window clamps [offset, offset+limit) to n.
func window(n, offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > n {
		offset = n
	}
	end := offset + limit
	if end > n || limit < 0 {
		end = n
	}
	return offset, end
}

// titlePage is a page of "In titles".
func (h *Handler) titlePage(ctx context.Context, p *principal.Principal, rn *run, offset, limit int) ([]TitleVideo, error) {
	from, to := window(len(rn.Set.Titles), offset, limit)
	page := rn.Set.Titles[from:to]
	ids := make([]string, len(page))
	for i, t := range page {
		ids[i] = t.V
	}
	cards, err := h.cards(ctx, p, ids)
	if err != nil {
		return nil, err
	}
	out := make([]TitleVideo, 0, len(page))
	for _, t := range page {
		c, ok := cards[t.V]
		if !ok {
			continue
		}
		out = append(out, TitleVideo{
			VideoCard: c, Score: t.S, InTranscripts: rn.hasVideo(t.V),
			TitleSnippet: highlightSnippet(c.Title, rn.Q, 500),
		})
	}
	return out, nil
}

// transcriptPage is a page of "In transcripts", each video with its best
// passages.
func (h *Handler) transcriptPage(ctx context.Context, p *principal.Principal, rn *run, offset, limit, perVideo int) ([]TranscriptVideo, error) {
	from, to := window(len(rn.Set.Videos), offset, limit)
	page := rn.Set.Videos[from:to]
	ids := make([]string, len(page))
	for i, v := range page {
		ids[i] = v.V
	}
	cards, err := h.cards(ctx, p, ids)
	if err != nil {
		return nil, err
	}
	visible := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := cards[id]; ok {
			visible = append(visible, id)
		}
	}
	passages, err := h.passagesOf(ctx, rn, visible, perVideo)
	if err != nil {
		return nil, err
	}
	out := make([]TranscriptVideo, 0, len(page))
	for _, v := range page {
		c, ok := cards[v.V]
		if !ok {
			continue
		}
		ps := passages[v.V]
		if ps == nil {
			ps = []Passage{}
		}
		out = append(out, TranscriptVideo{
			VideoCard: c, BestScore: v.S, Matches: v.N, Passages: ps, InTitle: rn.hasTitle(v.V),
		})
	}
	return out, nil
}

// passagesOf ranks each video's matching passages, best first, and keeps
// the first perVideo of each (0: all of them, up to maxVideoPassages).
func (h *Handler) passagesOf(ctx context.Context, rn *run, videoIDs []string, perVideo int) (map[string][]Passage, error) {
	out := map[string][]Passage{}
	if len(videoIDs) == 0 {
		return out, nil
	}
	if perVideo <= 0 || perVideo > maxVideoPassages {
		perVideo = maxVideoPassages
	}
	var fts map[string][]Hit
	if rn.Mode != "semantic" && h.DB != nil {
		var err error
		// Hybrid: a passage outside a video's FTS top perVideo that the meaning
		// search did not find ranks below all of them, so perVideo is enough.
		if fts, err = h.ftsPassages(ctx, rn.Q, videoIDs, perVideo); err != nil {
			return nil, err
		}
	}
	want := map[string]bool{}
	for _, id := range videoIDs {
		want[id] = true
	}
	sem := map[string][]Hit{}
	if rn.Mode != "fts" {
		for _, hit := range rn.Set.Sem {
			if want[hit.VideoID] {
				sem[hit.VideoID] = append(sem[hit.VideoID], hit)
			}
		}
	}
	wFTS, wSem := 1.0, 1.0
	if rn.Mode == "hybrid" {
		wFTS, wSem = h.weights(ctx)
	}
	for _, id := range videoIDs {
		var ranked []Hit
		switch rn.Mode {
		case "fts":
			ranked = fts[id]
		case "semantic":
			ranked = append([]Hit(nil), sem[id]...)
			sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
		default:
			ranked = RRFuseWeighted(fts[id], sem[id], 60, wFTS, wSem)
		}
		if len(ranked) > perVideo {
			ranked = ranked[:perVideo]
		}
		ps := make([]Passage, len(ranked))
		for i, hit := range ranked {
			ps[i] = Passage{
				SegmentID: hit.SegmentID, StartSec: hit.StartSec, EndSec: hit.EndSec,
				Snippet: highlightSnippet(hit.Snippet, rn.Q, 240), Score: hit.Score,
			}
		}
		out[id] = ps
	}
	return out, nil
}

// ftsPassages reads the videos' full-text matching segments of their active
// transcripts, best rank first, at most perVideo per video.
func (h *Handler) ftsPassages(ctx context.Context, q string, videoIDs []string, perVideo int) (map[string][]Hit, error) {
	var args []any
	var query string
	if isSQLite(h.DB) {
		args = []any{"%" + escapeLike(strings.ToLower(q)) + "%", strings.ToLower(q)}
		vids := placeholders(videoIDs, &args)
		args = append(args, perVideo)
		query = `
			SELECT id, video_id, start_sec, end_sec, text, score FROM (
				SELECT s.id, t.video_id, s.start_sec, s.end_sec, s.text, ` + sqliteScore + ` AS score,
				       row_number() OVER (PARTITION BY t.video_id ORDER BY ` + sqliteScore + ` DESC, s.id) AS rn
				FROM transcripts t JOIN transcript_segments s ON s.transcript_id = t.id
				WHERE t.video_id IN (` + vids + `) AND t.is_active AND lower(s.text) LIKE $1 ESCAPE '\'
			) x WHERE rn <= $` + strconv.Itoa(len(args)) + ` ORDER BY video_id, rn`
	} else {
		args = []any{q}
		vids := placeholders(videoIDs, &args)
		args = append(args, perVideo)
		query = `
			SELECT id, video_id, start_sec, end_sec, text, score FROM (
				SELECT s.id, t.video_id::text AS video_id, s.start_sec, s.end_sec, s.text,
				       ts_rank(s.search_tsv, plainto_tsquery('maktaba_search', $1)) AS score,
				       row_number() OVER (PARTITION BY t.video_id
				           ORDER BY ts_rank(s.search_tsv, plainto_tsquery('maktaba_search', $1)) DESC, s.id) AS rn
				FROM transcripts t JOIN transcript_segments s ON s.transcript_id = t.id
				WHERE t.video_id IN (` + vids + `) AND t.is_active
				  AND s.search_tsv @@ plainto_tsquery('maktaba_search', $1)
			) x WHERE rn <= $` + strconv.Itoa(len(args)) + ` ORDER BY video_id, rn`
	}
	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Hit{}
	for rows.Next() {
		var hit Hit
		if err := rows.Scan(&hit.SegmentID, &hit.VideoID, &hit.StartSec, &hit.EndSec, &hit.Snippet, &hit.Score); err != nil {
			return nil, err
		}
		out[hit.VideoID] = append(out[hit.VideoID], hit)
	}
	return out, rows.Err()
}
