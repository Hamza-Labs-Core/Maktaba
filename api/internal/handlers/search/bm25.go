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
// video above those whose words are scattered. A video's matching lines are counted, and
// its passages picked and marked, for the page shown only, matched as the ranking matched
// them: normalised, then by the word's forms (normalisedLines, savedVideo.L).
//
// The BM25 path is used once both indexes are valid (searchindex.Ready, on Postgres
// with pg_textsearch). Until then, and on SQLite, keyword search ranks the matching lines
// as before (runFTSVideos). While story 49.6's backfill runs, the videos with an active
// transcript and no document yet are found by their lines and fused in.

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lib/pq"
	"golang.org/x/text/unicode/norm"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchindex"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchterms"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/textnorm"
)

const (
	// bm25ExactWeight weighs the exact index's score against the stemmed one's
	bm25ExactWeight = 2.0
	// bm25PhraseTop is how many of the best videos a phrase is looked for in
	bm25PhraseTop = 100
	// bm25DocPhraseTop is how many of them the phrase is looked for in their documents'
	// normalised text, when their lines do not say it as written (a regular expression over
	// the whole document: the first ones, where a misspelt sentence's own video is)
	bm25DocPhraseTop = 30
	// bm25TopK and bm25StemTopK are how many videos each index gives: rules.search.max_videos'
	// 500. Each row's score is the one its index scan worked out (bm25LegSQL), so the depth
	// costs little: 500 rows of 5,000-word documents in about 2 ms. (Scored again from its
	// text, a row cost 0.15 to 1.8 ms by its length, and the lists were 100 and 50.)
	bm25TopK     = 500
	bm25StemTopK = 500
	// bm25StateTTL is how long the indexes' readiness and the backfill's state are kept
	bm25StateTTL = time.Minute
	// bm25StateRetry is how long a state that could not be read is kept (the line path
	// meanwhile), and bm25StateProbe how long reading it may take
	bm25StateRetry = 5 * time.Second
	bm25StateProbe = 2 * time.Second
	// phraseBudget bounds the phrase boost's query: past it the list keeps its BM25 order
	// (a phrase of common words over a large library's lines), and the query its 50 ms
	phraseBudget = 20 * time.Millisecond
	// passageBudget bounds the page's normalised lines (normalisedLines): past it the page
	// shows the line path's passages and counts
	passageBudget = 60 * time.Millisecond
)

// bm25State is what the handler knows of the BM25 path, refreshed every bm25StateTTL.
type bm25State struct {
	mu      sync.Mutex
	at      time.Time
	ttl     time.Duration
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
	if !st.at.IsZero() && time.Since(st.at) < st.ttl {
		return st.ready, st.missing
	}
	// its own deadline: a request the client gave up on is no reason to drop every search
	// to the line path for a minute
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bm25StateProbe)
	defer cancel()
	st.ttl = bm25StateTTL
	ready, err := searchindex.Ready(pctx, h.DB)
	if err != nil {
		ready, st.ttl = false, bm25StateRetry
	}
	missing = false
	if ready {
		if err := h.DB.QueryRowContext(pctx, `SELECT EXISTS (
			SELECT 1 FROM transcripts t
			 WHERE t.is_active
			   AND NOT EXISTS (SELECT 1 FROM video_search_docs d WHERE d.video_id = t.video_id))`).Scan(&missing); err != nil {
			missing, st.ttl = true, bm25StateRetry
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
	vs, err := h.runBM25Videos(ctx, h.withoutConjunctions(ctx, q, f.LibraryID), limit, f)
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
	return bm25LegSQLScored(ctx, q, limit, f, index, col, fn, false)
}

// bm25LegSQLScored is bm25LegSQL; scored selects the score worked out from each row's text
// (the operator itself), for a plan that is no BM25 index scan (bm25Leg).
func bm25LegSQLScored(ctx context.Context, q string, limit int, f Filters, index, col, fn string, scored bool) (string, []any) {
	args := []any{q}
	score := `d.` + col + ` OPERATOR(textsearch.<@>) textsearch.to_bm25query(public.` + fn + `($1), '` + index + `')`
	where := append([]string{"true"}, whereVideos(f, &args)...)
	where = append(where, limitWhere(ctx)...)
	args = append(args, limit)
	// the score is the one the index scan worked out (bm25_get_current_score), and the ORDER
	// BY is the operator alone, not in the select list: pg_textsearch's planner hook then
	// gives each row the scan's score instead of scoring its whole document again (1.8 ms
	// a row for a 5,000-word document: 180 ms a search on the home). The hook finds its
	// objects on the session's search_path: the bundle puts the extension's schema on the
	// app role's (pgbundle.textsearchSQL). Without it the order and the scores are the same,
	// only slower. A plan with no BM25 index scan has no score to give: bm25Leg checks.
	if scored {
		// ORDER BY the alias: the select list's score (one evaluation a row)
		return `
		SELECT d.video_id::text, ` + score + ` AS score
		  FROM video_search_docs d
		  JOIN videos v ON v.id = d.video_id AND v.deleted_at IS NULL
		 WHERE ` + strings.Join(where, " AND ") + `
		 ORDER BY score
		 LIMIT $` + strconv.Itoa(len(args)), args
	}
	return `
		SELECT d.video_id::text, textsearch.bm25_get_current_score() AS score
		  FROM video_search_docs d
		  JOIN videos v ON v.id = d.video_id AND v.deleted_at IS NULL
		 WHERE ` + strings.Join(where, " AND ") + `
		 ORDER BY ` + score + `
		 LIMIT $` + strconv.Itoa(len(args)), args
}

// bm25Leg is one index's top videos: (video id, score), best first.
func (h *Handler) bm25Leg(ctx context.Context, q string, limit int, f Filters, index, col, fn string) ([]savedVideo, error) {
	out, ends, err := h.bm25LegRun(ctx, q, limit, f, index, col, fn, false)
	if err != nil || len(ends) == 0 {
		return out, err
	}
	// the scores are the index scan's only when the plan has one: bm25_get_current_score is
	// the last score of the connection's last BM25 index scan, whatever query ran it (the
	// extension never clears it), so a plan without one (a table, or a library's rows, few
	// enough to read whole and sort) gives every row one score, another search's. The first
	// and last rows' scores are worked out from their text and compared (two documents, a
	// few ms at most); when they differ, or the check cannot be made, the leg asks again
	// with each row's score worked out
	ok, why := h.bm25ScoresAre(ctx, q, index, col, fn, ends)
	if ok {
		return out, nil
	}
	h.log().InfoContext(ctx, "search: BM25 scores worked out from the text", "event", "search_bm25_rescored", "index", index, "why", why)
	out, _, err = h.bm25LegRun(ctx, q, limit, f, index, col, fn, true)
	return out, err
}

// bm25ScoresAre reports whether each of rows (a leg's rows, scores as the query returned
// them) has the score the operator works out for its video's document, within 1e-3 of it: a
// score of another search is another document's, far from it, and a write between the two
// statements moves every score by about one part in the library's size. why says what
// differed.
func (h *Handler) bm25ScoresAre(ctx context.Context, q, index, col, fn string, rows []savedVideo) (bool, string) {
	args := []any{q}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.V
	}
	got, err := h.DB.QueryContext(ctx, `SELECT d.video_id::text, d.`+col+` OPERATOR(textsearch.<@>) textsearch.to_bm25query(public.`+fn+`($1), '`+index+`')
		  FROM video_search_docs d WHERE d.video_id IN (`+placeholders(ids, &args)+`)`, args...)
	if err != nil {
		return false, "the check failed: " + err.Error()
	}
	defer got.Close()
	want := map[string]float64{}
	for got.Next() {
		var id string
		var sc float64
		if err := got.Scan(&id, &sc); err != nil {
			return false, "the check failed: " + err.Error()
		}
		want[id] = sc
	}
	if err := got.Err(); err != nil {
		return false, "the check failed: " + err.Error()
	}
	for _, r := range rows {
		w, ok := want[r.V]
		if !ok {
			return false, "a row's document is gone"
		}
		if math.Abs(r.S-w) > 1e-9+1e-3*math.Abs(w) {
			return false, fmt.Sprintf("a row's score %v, its document's %v", r.S, w)
		}
	}
	return true, ""
}

// bm25LegRun runs one leg; ends are its first and last rows as returned (none, one or two),
// the ones bm25Leg checks the scores by.
func (h *Handler) bm25LegRun(ctx context.Context, q string, limit int, f Filters, index, col, fn string, scored bool) (out []savedVideo, ends []savedVideo, err error) {
	sqlText, args := bm25LegSQLScored(ctx, q, limit, f, index, col, fn, scored)
	rows, err := h.DB.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("bm25 %s: %w", index, err)
	}
	defer rows.Close()
	var last savedVideo
	n := 0
	for rows.Next() {
		var v savedVideo
		if err := rows.Scan(&v.V, &v.S); err != nil {
			return nil, nil, err
		}
		if n == 0 {
			ends = append(ends, v)
		}
		last = v
		n++
		v.S = -v.S   // <@> is a distance: the BM25 score negated
		if v.S > 0 { // a document none of whose words is the query's
			out = append(out, v)
		}
	}
	if n > 1 {
		ends = append(ends, last)
	}
	return out, ends, rows.Err()
}

// runBM25Videos ranks the videos by their documents (see the file's words).
func (h *Handler) runBM25Videos(ctx context.Context, q string, limit int, f Filters) ([]savedVideo, error) {
	var (
		wg          sync.WaitGroup
		exact, stem []savedVideo
		e1, e2      error
	)
	leg, stemLeg := min(limit, bm25TopK), min(limit, bm25StemTopK)
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer safego.Recover(nil, "search_bm25_exact")
		e1 = errSearchPanicked
		exact, e1 = h.bm25Leg(ctx, q, leg, f, "video_search_docs_bm25_text", "search_text", "maktaba_normalize")
	}()
	go func() {
		defer wg.Done()
		defer safego.Recover(nil, "search_bm25_stem")
		e2 = errSearchPanicked
		stem, e2 = h.bm25Leg(ctx, q, stemLeg, f, "video_search_docs_bm25_stem_text", "stem_text", "maktaba_stem_prepare")
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
	pctx, cancel := context.WithTimeout(ctx, phraseBudget)
	defer cancel()
	rows, err := h.DB.QueryContext(pctx, `
		SELECT DISTINCT t.video_id::text FROM transcripts t
		  JOIN transcript_segments s ON s.transcript_id = t.id
		 WHERE t.is_active AND t.video_id IN (`+placeholders(ids, &args)+`)
		   AND s.search_tsv @@ phraseto_tsquery('maktaba_search', $1)`, args...)
	if err != nil {
		if pctx.Err() != nil && ctx.Err() == nil {
			h.phraseAbandoned.Add(1)
			return nil // over its budget: the BM25 order stands
		}
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
		if pctx.Err() != nil && ctx.Err() == nil {
			h.phraseAbandoned.Add(1)
			return nil // over its budget while reading: the BM25 order stands
		}
		return err
	}
	// story 49.9: the lines' search_tsv is not normalised (49.4 left the 23 M lines as they
	// were), so a query spelled otherwise than its line (اسلام for إسلام, harakat in the line)
	// says no phrase there. The documents' search_text is the same lines normalised: the
	// videos left are looked for there, the words together within one line.
	var rest []string
	for _, v := range top {
		if !phrase[v.V] && len(rest) < bm25DocPhraseTop {
			rest = append(rest, v.V)
		}
	}
	if pat := phrasePattern(q); pat != "" && len(rest) > 0 {
		// a document an hour long is some 200 KB: strpos of the query's longest Arabic word
		// (a memory search, no copy) passes over the documents that cannot hold the phrase
		// before the regular expression reads one; ~* folds ASCII case as lower() would,
		// without copying the text
		dargs := []any{pat, phraseProbe(q)}
		drows, err := h.DB.QueryContext(pctx, `SELECT d.video_id::text FROM video_search_docs d
			 WHERE d.video_id IN (`+placeholders(rest, &dargs)+`)
			   AND ($2 = '' OR strpos(d.search_text, $2) > 0) AND d.search_text ~* $1`, dargs...)
		if err != nil {
			if pctx.Err() != nil && ctx.Err() == nil {
				h.phraseAbandoned.Add(1)
				return nil
			}
			return fmt.Errorf("phrase (documents): %w", err)
		}
		for drows.Next() {
			var id string
			if err := drows.Scan(&id); err != nil {
				_ = drows.Close()
				return err
			}
			phrase[id] = true
		}
		if err := drows.Close(); err != nil {
			return err
		}
		if err := drows.Err(); err != nil {
			if pctx.Err() != nil && ctx.Err() == nil {
				h.phraseAbandoned.Add(1)
				return nil
			}
			return err
		}
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

// normalisedLines reads the videos' matching lines the way the BM25 ranking matches
// (story 49.7): normalised (slot 0231's folding, api/internal/textnorm: إسلام is اسلام,
// هدى هدي, مدرسة مدرسه) or, failing that, by the word's other forms (maktaba_stem). For
// each video it returns the best perVideo lines and how many lines match, and for each line
// returned the line's words that matched, to mark (as they are written).
//
// No index holds the normalised lines, and working maktaba_tsv out line by line in SQL
// costs about 40 µs a line (a page of 20 videos is some 6,000 lines). So the lines are read
// once and matched here: a line whose normalised words hold every one of the query's exact
// lexemes matches (the exact configuration is simple: its lexemes are the normalised,
// lowercased words). Only the lines that fail that and yet contain every stemmed lexeme are
// asked of Postgres's stemmer. All of it runs within passageBudget. Past it, ok is false
// and the caller keeps the line path's passages.
func (h *Handler) normalisedLines(ctx context.Context, q string, videoIDs []string, perVideo int) (
	hits map[string][]Hit, counts map[string]int, marks map[int64][]string, ok bool, err error) {
	hits, counts, marks = map[string][]Hit{}, map[string]int{}, map[int64][]string{}
	if len(videoIDs) == 0 {
		return hits, counts, marks, true, nil
	}
	pctx, cancel := context.WithTimeout(ctx, passageBudget)
	defer cancel()
	late := func(err error) (map[string][]Hit, map[string]int, map[int64][]string, bool, error) {
		if pctx.Err() != nil && ctx.Err() == nil {
			return nil, nil, nil, false, nil // over the budget: the line path's passages
		}
		return nil, nil, nil, false, fmt.Errorf("normalised lines: %w", err)
	}
	// websearch's operators (a quoted phrase, -word, OR) are not a list of words: such a
	// query keeps the line path's passages
	if queryHasOperators(q) {
		return nil, nil, nil, false, nil
	}
	var exact, stems []string
	if err := h.DB.QueryRowContext(pctx, `SELECT tsvector_to_array(public.maktaba_tsv($1)),
		tsvector_to_array(public.maktaba_tsv_stem($1))`, q).Scan(pq.Array(&exact), pq.Array(&stems)); err != nil {
		return late(err)
	}
	if len(exact) == 0 && len(stems) == 0 {
		return hits, counts, marks, true, nil
	}
	loose := looseStems(stems)
	args := []any{}
	rows, err := h.DB.QueryContext(pctx, `
		SELECT s.id, t.video_id::text, s.start_sec, s.end_sec, s.text
		  FROM transcripts t JOIN transcript_segments s ON s.transcript_id = t.id
		 WHERE t.is_active AND t.video_id IN (`+placeholders(videoIDs, &args)+`)
		 ORDER BY t.video_id, s.seq`, args...) // in order: maybeCap keeps the earliest lines
	if err != nil {
		return late(err)
	}
	type line struct {
		hit   Hit
		words []string // its words as written
		norm  []string // the same words, normalised and lowercased
	}
	var matched []line
	var maybe []line // Postgres decides: they hold the query's letters but not its words as split here
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.hit.SegmentID, &l.hit.VideoID, &l.hit.StartSec, &l.hit.EndSec, &l.hit.Snippet); err != nil {
			_ = rows.Close()
			return late(err)
		}
		// most lines say none of it: a substring test on the folded line skips them before
		// any word is split
		folded := foldWord(l.hit.Snippet)
		inExact := len(exact) > 0 && containsAll(folded, exact)
		if !inExact && (len(loose) == 0 || !containsAll(folded, loose)) {
			continue
		}
		l.words, l.norm = lineWords(l.hit.Snippet)
		if n := hasAll(l.norm, exact); inExact && n > 0 {
			l.hit.Score = exactLine + float64(n)
			matched = append(matched, l)
		} else {
			// a word with punctuation in it (Qur'an, well-known, 2.0: one lexeme to the
			// parser), or another form of the word
			maybe = append(maybe, l)
		}
	}
	if err := rows.Close(); err != nil {
		return late(err)
	}
	if err := rows.Err(); err != nil {
		return late(err)
	}
	exactSet := map[string]bool{}
	for _, e := range exact {
		exactSet[e] = true
	}
	for i := range matched {
		for j, w := range matched[i].norm {
			if exactSet[w] {
				marks[matched[i].hit.SegmentID] = append(marks[matched[i].hit.SegmentID], matched[i].words[j])
			}
		}
	}
	// lines for Postgres: within maybeCap (a common word inside others, والله for الله,
	// sends many), and within the budget's rest; past either, the lines matched here stand
	if len(maybe) > maybeCap {
		maybe = maybe[:maybeCap]
	}
	if len(maybe) > 0 {
		ids := make([]int64, len(maybe))
		byID := map[int64]*line{}
		for i := range maybe {
			ids[i] = maybe[i].hit.SegmentID
			byID[ids[i]] = &maybe[i]
		}
		args := []any{q}
		rows, err := h.DB.QueryContext(pctx, `
			WITH q AS (SELECT public.maktaba_query($1) AS e, public.maktaba_query_stem($1) AS st,
			                  NULLIF(array_to_string(ARRAY(SELECT quote_literal(l)
			                      FROM unnest(tsvector_to_array(public.maktaba_tsv($1))) l), ' | '), '')::tsquery AS eo,
			                  NULLIF(array_to_string(ARRAY(SELECT quote_literal(l)
			                      FROM unnest(tsvector_to_array(public.maktaba_tsv_stem($1))) l), ' | '), '')::tsquery AS so)
			SELECT s.id,
			       CASE WHEN public.maktaba_tsv(s.text) @@ q.e
			            THEN `+strconv.Itoa(exactLine)+` + ts_rank(public.maktaba_tsv(s.text), q.e)
			            ELSE ts_rank(public.maktaba_tsv_stem(s.text), q.st) END,
			       ARRAY(SELECT w FROM regexp_split_to_table(s.text, '\s+') w
			              WHERE w <> '' AND (public.maktaba_tsv(w) @@ q.eo OR public.maktaba_tsv_stem(w) @@ q.so))
			  FROM q, transcript_segments s
			 WHERE s.id IN (`+placeholders(ids, &args)+`)
			   AND (public.maktaba_tsv(s.text) @@ q.e OR public.maktaba_tsv_stem(s.text) @@ q.st)`, args...)
		if err != nil {
			if pctx.Err() == nil || ctx.Err() != nil {
				return late(err)
			}
			rows = nil // out of time: the lines matched here stand
		}
		for rows != nil && rows.Next() {
			var id int64
			var score float64
			var words []string
			if err := rows.Scan(&id, &score, pq.Array(&words)); err != nil {
				_ = rows.Close()
				if pctx.Err() == nil || ctx.Err() != nil {
					return late(err)
				}
				break
			}
			if l := byID[id]; l != nil {
				l.hit.Score = score
				matched = append(matched, *l)
				marks[id] = words
			}
		}
		if rows != nil {
			cerr, rerr := rows.Close(), rows.Err()
			for _, err := range []error{cerr, rerr} {
				if err != nil && (pctx.Err() == nil || ctx.Err() != nil) {
					return late(err)
				}
			}
		}
	}
	// each video's lines, best first (the exact ones above the word forms), then in order
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i].hit, matched[j].hit
		if a.VideoID != b.VideoID {
			return a.VideoID < b.VideoID
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.SegmentID < b.SegmentID
	})
	for _, l := range matched {
		counts[l.hit.VideoID]++
		if len(hits[l.hit.VideoID]) < perVideo {
			hits[l.hit.VideoID] = append(hits[l.hit.VideoID], l.hit)
		} else {
			delete(marks, l.hit.SegmentID)
		}
	}
	return hits, counts, marks, true, nil
}

// maybeCap bounds the lines normalisedLines asks Postgres about. Each costs it some 40 µs
// twice (maktaba_tsv and its stemmed pair): 300 fit in the budget with the rest of it.
var maybeCap = 300

// exactLine lifts a line that says the query's words as typed (normalised) above every
// line that says another form of them (a form's ts_rank stays far below it).
const exactLine = 1000

// queryHasOperators reports whether q uses websearch_to_tsquery's operators: a quoted
// phrase, a word negated with -, or OR between words.
func queryHasOperators(q string) bool {
	if strings.Contains(q, `"`) {
		return true
	}
	for _, w := range strings.Fields(q) {
		// - negates a word it starts (well-known is one word, not a negation)
		if strings.EqualFold(w, "or") || strings.HasPrefix(w, "-") {
			return true
		}
	}
	return false
}

// looseStems are the stems as the line test looks for them: an English stem that Snowball
// ended with an e it added (come from coming, hope from hoping) is looked for without it.
// The test only lets lines through to Postgres's stemmer, which decides.
func looseStems(stems []string) []string {
	out := make([]string, len(stems))
	for i, st := range stems {
		if len(st) > 3 && strings.HasSuffix(st, "e") && st == strings.ToLower(st) && isASCII(st) {
			st = st[:len(st)-1]
		}
		out[i] = st
	}
	return out
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// lineWords splits a line into its words (letters and digits; anything else ends a word,
// as Postgres's text search parser splits) and their normalised, lowercased forms.
func lineWords(s string) (words, norm []string) {
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.Is(unicode.Mn, r) }) {
		words = append(words, w)
		norm = append(norm, foldWord(w))
	}
	return words, norm
}

// foldWord is a word as the exact configuration's lexeme spells it: slot 0231's folding,
// its accents off (the configuration's unaccent: café is cafe), lowercased.
func foldWord(w string) string {
	w = textnorm.Normalize(w)
	accented := false
	for _, r := range w {
		if r >= 0x00C0 && r < 0x0590 { // Latin, Greek, Cyrillic with their accents
			accented = true
			break
		}
	}
	if !accented { // Arabic and plain ASCII: the folding did it all
		return strings.ToLower(w)
	}
	w = norm.NFD.String(w)
	w = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, w)
	return strings.ToLower(norm.NFC.String(w))
}

// hasAll counts the occurrences of the lexemes in words when each lexeme is among them,
// and is 0 otherwise.
func hasAll(words, lexemes []string) int {
	if len(lexemes) == 0 {
		return 0
	}
	n := 0
	for _, l := range lexemes {
		c := 0
		for _, w := range words {
			if w == l {
				c++
			}
		}
		if c == 0 {
			return 0
		}
		n += c
	}
	return n
}

// containsAll reports whether every lexeme is a substring of s (a stem is inside each of
// its forms' normalised words).
func containsAll(s string, lexemes []string) bool {
	for _, l := range lexemes {
		if !strings.Contains(s, l) {
			return false
		}
	}
	return true
}

// phraseSep is what lies between two words of a line in a document's search_text: no
// letter or digit (the Arabic letters spelled out: the C locale's [:alnum:] is ASCII), and
// no line break. phraseEdge is what lies before the first word and after the last: the
// same, or a line break.
const (
	phraseSep  = `[^[:alnum:]` + phraseLetters + `\n]`
	phraseEdge = `[^[:alnum:]` + phraseLetters + `]`
	// phraseLetters are the Arabic script's letters: Arabic's, and Persian's and Urdu's
	// (پ چ ژ گ ی ے), not its punctuation (، ؛ ؟)
	phraseLetters = `\u0621-\u064a\u066e-\u06d3\u06fa-\u06ff`
)

// phrasePattern is the regular expression of q's words together in one line of a document's
// lower-cased search_text: folded as maktaba_normalize folds, lower case ("" for fewer than
// two words).
func phrasePattern(q string) string {
	if queryHasOperators(q) {
		return "" // a quoted phrase, -word or OR: not a phrase of every word
	}
	words := strings.Fields(strings.ToLower(textnorm.Normalize(q)))
	var parts []string
	for _, w := range words {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if w != "" {
			parts = append(parts, regexp.QuoteMeta(w))
		}
	}
	if len(parts) < 2 {
		return ""
	}
	return `(^|` + phraseEdge + `)` + strings.Join(parts, phraseSep+`+`) + `($|` + phraseEdge + `)`
}

// conjunctions are the one-letter prefixes withoutConjunctions takes off a word.
var conjunctions = []string{"و", "ف"}

// withoutConjunctions is q with each word that no document of the libraries holds, prefixed
// by و or ف, written without that letter when documents hold the rest (story 49.9's
// relevance check: Snowball keeps the و of a word it does not know, so وغفران found
// neither index's غفران). A word some documents hold as written is left as it is: والصبر
// and وقت are words. q is unchanged when the dictionary cannot be read, or holds nothing.
func (h *Handler) withoutConjunctions(ctx context.Context, q string, libs []string) string {
	if queryHasOperators(q) {
		return q
	}
	words := strings.Fields(q)
	type cand struct {
		i          int
		key, short string
		cut        int // bytes of the prefix in the word as written
	}
	var cands []cand
	var keys []string
	for i, w := range words {
		// the word without punctuation around it (وغفران، is the word وغفران)
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r) })
		k := searchterms.Key(w)
		for _, c := range conjunctions {
			if strings.HasPrefix(k, c) && utf8.RuneCountInString(k) >= 5 && strings.HasPrefix(words[i], c) {
				cands = append(cands, cand{i: i, key: k, short: strings.TrimPrefix(k, c), cut: len(c)})
				keys = append(keys, k, strings.TrimPrefix(k, c))
			}
		}
	}
	if len(cands) == 0 {
		return q
	}
	df, err := h.dfOf(ctx, keys, libs)
	if err != nil || len(df) == 0 {
		return q
	}
	for _, c := range cands {
		if df[c.key] == 0 && df[c.short] > 0 {
			words[c.i] = words[c.i][c.cut:]
		}
	}
	return strings.Join(words, " ")
}

// phraseProbe is the query's longest word with no ASCII letter, folded as search_text is:
// a document whose text does not hold it holds no phrase of the query ("" none, a query of
// Latin words: their case differs between the query and the text).
func phraseProbe(q string) string {
	best := ""
	for _, w := range strings.Fields(textnorm.Normalize(q)) {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if strings.IndexFunc(w, func(r rune) bool { return r < utf8.RuneSelf && unicode.IsLetter(r) }) >= 0 {
			continue
		}
		if utf8.RuneCountInString(w) > utf8.RuneCountInString(best) {
			best = w
		}
	}
	return best
}

// PhraseAbandoned is how many phrase boosts ran out of their budget (phraseBudget) since
// the handler started: the list kept its BM25 order (story 49.9's check reports it).
func (h *Handler) PhraseAbandoned() int64 { return h.phraseAbandoned.Load() }
