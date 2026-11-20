package search

// Keyword search by video (Epic 49, story 49.7): BM25 over each video's search document
// (video_search_docs, stories 49.5 and 49.6) instead of ranking matching lines.
//
// A video's score is its BM25 score on the exact index (maktaba_search: the text as typed,
// normalised) counted twice, plus its score on the stemmed index (maktaba_stem: the word's
// other forms), so the word as typed ranks first and its forms follow. The library, the
// language and an account's limits filter the videos in the same query. For the top
// bm25PhraseTop videos, a query of more than one word that a line of the video's
// transcript says as a phrase (phraseto_tsquery on the lines' search_tsv) lifts the
// video above those whose words are scattered. A video's matching lines are counted only
// for the page shown (savedVideo.L).
//
// The BM25 path is used once both indexes are valid (searchindex.Ready, on Postgres
// with pg_textsearch). Until then, and on SQLite, keyword search ranks the matching lines
// as before (runFTSVideos). While story 49.6's backfill runs, the videos with an active
// transcript and no document yet are found by their lines and fused in.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchindex"
)

const (
	// bm25ExactWeight weighs the exact index's score against the stemmed one's
	bm25ExactWeight = 2.0
	// bm25PhraseTop is how many of the best videos a phrase is looked for in
	bm25PhraseTop = 100
	// bm25TopK is how many videos each index gives: its query scores every row it returns
	// again (0.15 to 0.25 ms a row on a 45,000-document library), so the two legs together
	// return up to 200 videos within the 50 ms the query has
	bm25TopK = 100
	// bm25StateTTL is how long the indexes' readiness and the backfill's state are kept
	bm25StateTTL = time.Minute
)

// bm25State is what the handler knows of the BM25 path, refreshed every bm25StateTTL.
type bm25State struct {
	mu      sync.Mutex
	at      time.Time
	ready   bool // both indexes valid
	missing bool // a video with an active transcript has no document yet
}

func (h *Handler) bm25Now(ctx context.Context) (ready, missing bool) {
	if h.DB == nil || isSQLite(h.DB) {
		return false, false
	}
	st := &h.bm25
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.at.IsZero() && time.Since(st.at) < bm25StateTTL {
		return st.ready, st.missing
	}
	ready, err := searchindex.Ready(ctx, h.DB)
	if err != nil {
		ready = false
	}
	missing = false
	if ready {
		if err := h.DB.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM transcripts t
			 WHERE t.is_active
			   AND NOT EXISTS (SELECT 1 FROM video_search_docs d WHERE d.video_id = t.video_id))`).Scan(&missing); err != nil {
			missing = true
		}
	}
	st.ready, st.missing, st.at = ready, missing, time.Now()
	return ready, missing
}

// keywordVideos is the keyword leg of a search: BM25 by video when the indexes are
// ready, the matching lines otherwise.
func (h *Handler) keywordVideos(ctx context.Context, q string, limit int, f Filters) ([]savedVideo, error) {
	ready, missing := h.bm25Now(ctx)
	if !ready {
		return h.runFTSVideos(ctx, q, limit, f)
	}
	vs, err := h.runBM25Videos(ctx, q, limit, f)
	if err != nil {
		return nil, err
	}
	if !missing {
		return vs, nil
	}
	lines, err := h.runFTSVideosWhere(ctx, q, limit, f,
		`NOT EXISTS (SELECT 1 FROM video_search_docs d WHERE d.video_id = t.video_id)`)
	if err != nil {
		return nil, err
	}
	return fuseKeyword(vs, lines, limit), nil
}

// bm25LegSQL is one index's query and its arguments: col is the document's column the
// index is on (slot 0234), fn the function that normalises the query as col was.
func bm25LegSQL(ctx context.Context, q string, limit int, f Filters, index, col, fn string) (string, []any) {
	args := []any{q}
	score := `d.` + col + ` OPERATOR(textsearch.<@>) textsearch.to_bm25query(public.` + fn + `($1), '` + index + `')`
	where := append([]string{"true"}, whereVideos(f, &args)...)
	where = append(where, limitWhere(ctx)...)
	args = append(args, limit)
	// ORDER BY the score's alias: the index scan's own value. Written out again in the
	// select list above a join, the score is worked out once more for every row
	return `
		SELECT d.video_id::text, ` + score + ` AS score
		  FROM video_search_docs d
		  JOIN videos v ON v.id = d.video_id AND v.deleted_at IS NULL
		 WHERE ` + strings.Join(where, " AND ") + `
		 ORDER BY score
		 LIMIT $` + strconv.Itoa(len(args)), args
}

// bm25Leg is one index's top videos: (video id, score), best first.
func (h *Handler) bm25Leg(ctx context.Context, q string, limit int, f Filters, index, col, fn string) ([]savedVideo, error) {
	sqlText, args := bm25LegSQL(ctx, q, limit, f, index, col, fn)
	rows, err := h.DB.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("bm25 %s: %w", index, err)
	}
	defer rows.Close()
	var out []savedVideo
	for rows.Next() {
		var v savedVideo
		if err := rows.Scan(&v.V, &v.S); err != nil {
			return nil, err
		}
		v.S = -v.S   // <@> is a distance: the BM25 score negated
		if v.S > 0 { // a document none of whose words is the query's
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// runBM25Videos ranks the videos by their documents (see the file's words).
func (h *Handler) runBM25Videos(ctx context.Context, q string, limit int, f Filters) ([]savedVideo, error) {
	var (
		wg          sync.WaitGroup
		exact, stem []savedVideo
		e1, e2      error
	)
	leg := min(limit, bm25TopK)
	wg.Add(2)
	go func() {
		defer wg.Done()
		exact, e1 = h.bm25Leg(ctx, q, leg, f, "video_search_docs_bm25_text", "search_text", "maktaba_normalize")
	}()
	go func() {
		defer wg.Done()
		stem, e2 = h.bm25Leg(ctx, q, leg, f, "video_search_docs_bm25_stem_text", "stem_text", "maktaba_stem_prepare")
	}()
	wg.Wait()
	if e1 != nil {
		return nil, e1
	}
	if e2 != nil {
		return nil, e2
	}
	by := map[string]*savedVideo{}
	var order []string
	add := func(vs []savedVideo, w float64) {
		for _, v := range vs {
			s, ok := by[v.V]
			if !ok {
				s = &savedVideo{V: v.V, L: true}
				by[v.V] = s
				order = append(order, v.V)
			}
			s.S += w * v.S
		}
	}
	add(exact, bm25ExactWeight)
	add(stem, 1)
	out := make([]savedVideo, 0, len(order))
	for _, id := range order {
		out = append(out, *by[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].S != out[j].S {
			return out[i].S > out[j].S
		}
		return out[i].V < out[j].V
	})
	if len(out) > limit {
		out = out[:limit]
	}
	if err := h.phraseBoost(ctx, q, out); err != nil {
		return nil, err
	}
	return out, nil
}

// phraseBoost lifts, among the first bm25PhraseTop videos, those a line of whose active
// transcript says the query's words together (a query of one word has no phrase): each
// gains the best score of the list, so it ranks above every video without the phrase and
// in its BM25 order among those with it.
func (h *Handler) phraseBoost(ctx context.Context, q string, vs []savedVideo) error {
	if len(strings.Fields(q)) < 2 || len(vs) == 0 {
		return nil
	}
	top := vs
	if len(top) > bm25PhraseTop {
		top = top[:bm25PhraseTop]
	}
	ids := make([]string, len(top))
	for i, v := range top {
		ids[i] = v.V
	}
	args := []any{q}
	rows, err := h.DB.QueryContext(ctx, `
		SELECT DISTINCT t.video_id::text FROM transcripts t
		  JOIN transcript_segments s ON s.transcript_id = t.id
		 WHERE t.is_active AND t.video_id IN (`+placeholders(ids, &args)+`)
		   AND s.search_tsv @@ phraseto_tsquery('maktaba_search', $1)`, args...)
	if err != nil {
		return fmt.Errorf("phrase: %w", err)
	}
	defer rows.Close()
	phrase := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		phrase[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(phrase) == 0 {
		return nil
	}
	lift := vs[0].S
	for i := range top {
		if phrase[vs[i].V] {
			vs[i].S += lift
		}
	}
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].S > vs[j].S })
	return nil
}

// fuseKeyword merges the BM25 videos with the videos found by their lines that have no
// document yet (story 49.6's backfill running): each scores 1/(60+rank) in its list, a
// video found by its lines half a place lower.
func fuseKeyword(bm25, lines []savedVideo, limit int) []savedVideo {
	const k = 60
	type scored struct {
		v savedVideo
		r float64
	}
	var all []scored
	for i, v := range bm25 {
		all = append(all, scored{v, 1 / float64(k+i+1)})
	}
	// half a place behind the BM25 video of the same rank: a tie goes to the document
	for i, v := range lines {
		all = append(all, scored{v, 1 / (float64(k+i+1) + 0.5)})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].r > all[j].r })
	out := make([]savedVideo, 0, len(all))
	for _, s := range all {
		v := s.v
		v.S = s.r
		out = append(out, v)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// countLines fills in the matching lines of the page's BM25 videos (savedVideo.L): their
// active transcript's lines the query matches, as the line path counts them.
func (h *Handler) countLines(ctx context.Context, q string, page []savedVideo) (map[string]int, error) {
	var ids []string
	for _, v := range page {
		if v.L {
			ids = append(ids, v.V)
		}
	}
	out := map[string]int{}
	if len(ids) == 0 || h.DB == nil || isSQLite(h.DB) {
		return out, nil
	}
	args := []any{q}
	rows, err := h.DB.QueryContext(ctx, `
		SELECT t.video_id::text, count(*) FROM transcripts t
		  JOIN transcript_segments s ON s.transcript_id = t.id
		 WHERE t.is_active AND t.video_id IN (`+placeholders(ids, &args)+`)
		   AND s.search_tsv @@ plainto_tsquery('maktaba_search', $1)
		 GROUP BY t.video_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("count lines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
