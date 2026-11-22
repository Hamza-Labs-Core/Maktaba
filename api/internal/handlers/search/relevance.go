package search

// The relevance check (Epic 49 story 49.9): test searches generated from the library's own
// transcripts, no hand-written list, and how keyword search answers them.
//
//   - known-item: distinctive sentences taken from random videos' documents (a run of
//     words that only that video's lines say together), each searched as written, with its
//     spelling folded (hamza forms, ta marbuta, alef maqsura dropped, as a quick typist
//     writes them) and with a word in another form (a conjunction prefixed, 49.4's word
//     forms). Each must bring its own video in the top 3.
//   - topic: chapter titles that name one chapter only, and topic labels' words. A chapter
//     title must bring its video in the top 10; a topic word one of the topic's videos.
//   - common words: الله, الصبر and the most frequent content words of the dictionary
//     (search_terms). Each must answer in under 50 ms at p95, and the top 10 must use the
//     word more per minute than the videos that match it do (their median).
//
// The generator draws with a fixed seed, so a library gives the same searches every run:
// the replica job runs it on a synthetic library (TestBM25_Relevance); `maktaba-api
// search-relevance` runs it on a home, read only.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchindex"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchterms"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/textnorm"
)

// Relevance case kinds.
const (
	RelKnownItem = "known-item"
	RelChapter   = "chapter"
	RelTopic     = "topic"
	RelCommon    = "common"
)

// RelevanceCase is one generated search and what it must find.
type RelevanceCase struct {
	Kind    string   `json:"kind"`
	Variant string   `json:"variant,omitempty"` // known-item: as-written, spelling, word-form
	Query   string   `json:"query"`
	Want    []string `json:"want,omitempty"` // any of these videos
	Top     int      `json:"top"`            // ... within the first Top
}

// RelevanceOptions sizes a run.
type RelevanceOptions struct {
	Seed      int64
	KnownItem int // sentences (each searched in up to three variants)
	Chapters  int
	Topics    int
	Common    int // most frequent content words, besides الله and الصبر
	Runs      int // timed runs of each common word (p95)
}

// DefaultRelevance is the spec's run: 200 sentences, 20 frequent words.
var DefaultRelevance = RelevanceOptions{Seed: 49, KnownItem: 200, Chapters: 50, Topics: 30, Common: 20, Runs: 20}

// RelevanceResult is a case and how it went.
type RelevanceResult struct {
	RelevanceCase
	Rank   int           `json:"rank"` // 1-based place of the first wanted video; 0 none
	Passed bool          `json:"passed"`
	P95    time.Duration `json:"p95,omitempty"` // common
	Note   string        `json:"note,omitempty"`
}

// RelevanceReport sums a run.
type RelevanceReport struct {
	Results []RelevanceResult `json:"results"`
	Passed  map[string]int    `json:"passed"`
	Total   map[string]int    `json:"total"`
	// PhraseAbandoned is how many of the run's phrase boosts ran out of their 20 ms budget
	// (a failed sentence then says why: the boost did not run, not that it ranked wrong)
	PhraseAbandoned int64 `json:"phrase_abandoned"`
}

// Failed are the cases that did not pass.
func (r RelevanceReport) Failed() []RelevanceResult {
	var out []RelevanceResult
	for _, x := range r.Results {
		if !x.Passed {
			out = append(out, x)
		}
	}
	return out
}

// stopwords are the function words a "content word" is not (the dictionary's most frequent
// keys are mostly these).
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`في من على الى عن ان او ما لا هذا هذه ذلك تلك الذي التي الذين هو هي هم نحن انت انا
		كان كانت يكون قد لقد ثم اذا اذ لم لن كل بعض غير مع عند حتى بين قال يقول قالوا ايضا هنا هناك كما لكن بل
		والله لان لانه انه انها فهو فهي وهو وهي ولا فلا الا اي يا ها نعم كذلك هكذا شيء the and of to in is that it`) {
		stopwords[w] = true
	}
}

// relevanceSample orders a table's rows by the seed: the same rows each run.
func relevanceSample(col string, seed int64) string {
	return fmt.Sprintf("md5(%s::text || '%d')", col, seed)
}

// GenerateRelevance draws the cases from db (PostgreSQL with the search documents).
func GenerateRelevance(ctx context.Context, db *sql.DB, o RelevanceOptions) ([]RelevanceCase, error) {
	r := rand.New(rand.NewSource(o.Seed))
	var out []RelevanceCase
	known, err := knownItems(ctx, db, r, o)
	if err != nil {
		return nil, fmt.Errorf("known-item: %w", err)
	}
	out = append(out, known...)
	chapters, err := chapterCases(ctx, db, o)
	if err != nil {
		return nil, fmt.Errorf("chapters: %w", err)
	}
	out = append(out, chapters...)
	topics, err := topicCases(ctx, db, o)
	if err != nil {
		return nil, fmt.Errorf("topics: %w", err)
	}
	out = append(out, topics...)
	common, err := commonCases(ctx, db, o)
	if err != nil {
		return nil, fmt.Errorf("common words: %w", err)
	}
	return append(out, common...), nil
}

// spellingVariant is s as a quick typist writes it: hamza forms as a bare alef, ta marbuta
// as ha, alef maqsura as ya, harakat left out ("" when that changes nothing).
func spellingVariant(s string) string {
	v := textnorm.Normalize(s)
	if v == s {
		return ""
	}
	return v
}

// wordFormVariant is s with its first long word prefixed by the conjunction و (49.4's
// word forms: maktaba_stem strips it), "" when no word is long enough.
func wordFormVariant(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if utf8.RuneCountInString(w) >= 4 && !strings.HasPrefix(w, "و") {
			words[i] = "و" + w
			return strings.Join(words, " ")
		}
	}
	return ""
}

// knownItems draws sentences: a run of 5 to 7 words from a random line of a random
// document, kept when the video's lines are the only ones that say it (the lines' phrase
// index) and at least one of its words is in under 1% of the documents.
func knownItems(ctx context.Context, db *sql.DB, r *rand.Rand, o RelevanceOptions) ([]RelevanceCase, error) {
	if o.KnownItem <= 0 {
		return nil, nil
	}
	var docs int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM video_search_docs`).Scan(&docs); err != nil {
		return nil, err
	}
	// the candidates' ids first, then one body at a time (an hour's lecture is some 200 KB)
	rows, err := db.QueryContext(ctx, `SELECT d.video_id::text FROM video_search_docs d
		 ORDER BY `+relevanceSample("d.video_id", o.Seed)+` LIMIT $1`, o.KnownItem*4)
	if err != nil {
		return nil, err
	}
	var cands []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		cands = append(cands, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rare := max(docs/100, 2)
	var out []RelevanceCase
	for _, id := range cands {
		if len(out)/3 >= o.KnownItem {
			break
		}
		var body string
		if err := db.QueryRowContext(ctx, `SELECT body FROM video_search_docs WHERE video_id = $1`, id).Scan(&body); err != nil {
			return nil, err
		}
		phrase := pickPhrase(r, body)
		if phrase == "" {
			continue
		}
		// distinctive: a word in under 1% of the documents, and only this video says it
		ok, err := hasRareWord(ctx, db, phrase, rare)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var videos int
		if err := db.QueryRowContext(ctx, `SELECT count(DISTINCT t.video_id) FROM transcript_segments s
			 JOIN transcripts t ON t.id = s.transcript_id AND t.is_active
			 WHERE s.search_tsv @@ phraseto_tsquery('maktaba_search', $1)`, phrase).Scan(&videos); err != nil {
			return nil, err
		}
		if videos != 1 {
			continue
		}
		want := []string{id}
		out = append(out, RelevanceCase{Kind: RelKnownItem, Variant: "as-written", Query: phrase, Want: want, Top: 3})
		if v := spellingVariant(phrase); v != "" {
			out = append(out, RelevanceCase{Kind: RelKnownItem, Variant: "spelling", Query: v, Want: want, Top: 3})
		}
		if v := wordFormVariant(phrase); v != "" {
			out = append(out, RelevanceCase{Kind: RelKnownItem, Variant: "word-form", Query: v, Want: want, Top: 3})
		}
	}
	return out, nil
}

// pickPhrase is a run of 5 to 7 words of a random line of body with 5 words or more ("" none).
func pickPhrase(r *rand.Rand, body string) string {
	var lines [][]string
	for _, l := range strings.Split(body, "\n") {
		if w := strings.Fields(l); len(w) >= 5 {
			lines = append(lines, w)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	w := lines[r.Intn(len(lines))]
	n := min(len(w), 5+r.Intn(3))
	at := r.Intn(len(w) - n + 1)
	return strings.Join(w[at:at+n], " ")
}

// chapterCases are chapter titles of two words or more that no other chapter has.
func chapterCases(ctx context.Context, db *sql.DB, o RelevanceOptions) ([]RelevanceCase, error) {
	if o.Chapters <= 0 {
		return nil, nil
	}
	var docs int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM video_search_docs`).Scan(&docs); err != nil {
		return nil, err
	}
	rare := max(docs/100, 2)
	rows, err := db.QueryContext(ctx, `SELECT min(c.video_id::text), c.title FROM chapters c
		 JOIN video_search_docs d ON d.video_id = c.video_id
		 WHERE c.title ~ '\S+\s+\S+' GROUP BY c.title HAVING count(*) = 1
		 ORDER BY `+relevanceSample("c.title", o.Seed)+` LIMIT $1`, o.Chapters*4)
	if err != nil {
		return nil, err
	}
	type chapter struct{ id, title string }
	var all []chapter
	for rows.Next() {
		var c chapter
		if err := rows.Scan(&c.id, &c.title); err != nil {
			_ = rows.Close()
			return nil, err
		}
		all = append(all, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// a title of common words only (مقدمة, "the first lesson") names no one video: kept
	// when a word of it is in under 1% of the documents, as a known-item sentence
	var out []RelevanceCase
	for _, c := range all {
		if len(out) >= o.Chapters {
			break
		}
		ok, err := hasRareWord(ctx, db, c.title, rare)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, RelevanceCase{Kind: RelChapter, Query: c.title, Want: []string{c.id}, Top: 10})
		}
	}
	return out, nil
}

// hasRareWord reports whether a word of s is in at most rare documents (search_terms).
func hasRareWord(ctx context.Context, db *sql.DB, s string, rare int) (bool, error) {
	keys := searchterms.QueryKeys(s)
	if len(keys) == 0 {
		return false, nil
	}
	var args []any
	in := placeholders(keys, &args)
	var minDF sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT min(n) FROM (SELECT sum(df) AS n FROM search_terms
		 WHERE term IN (`+in+`) GROUP BY term) x`, args...).Scan(&minDF); err != nil {
		return false, err
	}
	return minDF.Valid && minDF.Int64 <= int64(rare), nil
}

// topicCases are the labelled topics' first content word, wanting one of the topic's
// best-scored videos (its top 20 with a document).
func topicCases(ctx context.Context, db *sql.DB, o RelevanceOptions) ([]RelevanceCase, error) {
	if o.Topics <= 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT t.library_id::text, t.topic_id, t.label FROM library_topics t
		 WHERE coalesce(t.label, '') <> '' AND t.video_count > 0
		 ORDER BY `+relevanceSample("t.label", o.Seed)+` LIMIT $1`, o.Topics)
	if err != nil {
		return nil, err
	}
	type topic struct {
		lib   string
		id    int
		label string
	}
	var topics []topic
	for rows.Next() {
		var t topic
		if err := rows.Scan(&t.lib, &t.id, &t.label); err != nil {
			_ = rows.Close()
			return nil, err
		}
		topics = append(topics, t)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []RelevanceCase
	for _, t := range topics {
		word := ""
		for _, w := range searchterms.Words(t.label) {
			if k := searchterms.Key(w); utf8.RuneCountInString(k) >= 3 && !stopwords[k] {
				word = w
				break
			}
		}
		if word == "" {
			continue
		}
		vr, err := db.QueryContext(ctx, `SELECT vt.video_id::text FROM video_topics vt
			 JOIN video_search_docs d ON d.video_id = vt.video_id
			 WHERE vt.library_id = $1 AND vt.topic_id = $2 ORDER BY vt.score DESC LIMIT 20`, t.lib, t.id)
		if err != nil {
			return nil, err
		}
		var want []string
		for vr.Next() {
			var id string
			if err := vr.Scan(&id); err != nil {
				_ = vr.Close()
				return nil, err
			}
			want = append(want, id)
		}
		if err := vr.Close(); err != nil {
			return nil, err
		}
		if len(want) > 0 {
			out = append(out, RelevanceCase{Kind: RelTopic, Query: word, Want: want, Top: 10})
		}
	}
	return out, nil
}

// commonCases are الله, الصبر and the dictionary's most frequent content words.
func commonCases(ctx context.Context, db *sql.DB, o RelevanceOptions) ([]RelevanceCase, error) {
	out := []RelevanceCase{{Kind: RelCommon, Query: "الله", Top: 10}, {Kind: RelCommon, Query: "الصبر", Top: 10}}
	if o.Common <= 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT term, (array_agg(shown ORDER BY df DESC, shown))[1], sum(df) AS n FROM search_terms
		 WHERE length(term) >= 3 GROUP BY term ORDER BY n DESC, term LIMIT $1`, o.Common*5)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{"الله": true, "الصبر": true}
	n := 0
	for rows.Next() {
		var term, shown string
		var df int
		if err := rows.Scan(&term, &shown, &df); err != nil {
			return nil, err
		}
		if stopwords[term] || seen[term] || n >= o.Common {
			continue
		}
		seen[term] = true
		n++
		out = append(out, RelevanceCase{Kind: RelCommon, Query: shown, Top: 10})
	}
	return out, rows.Err()
}

// RunRelevance searches each case as keyword search does (story 49.7's ranking).
func (h *Handler) RunRelevance(ctx context.Context, cases []RelevanceCase, runs int) (RelevanceReport, error) {
	rep := RelevanceReport{Passed: map[string]int{}, Total: map[string]int{}}
	if runs <= 0 {
		runs = 1
	}
	// the check measures the BM25 ranking: not the line path a library falls back to while
	// its indexes are missing or building
	if ready, err := searchindex.Ready(ctx, h.DB); err != nil || !ready {
		if err != nil {
			return rep, fmt.Errorf("keyword search's BM25 indexes cannot be read: %w", err)
		}
		return rep, errors.New("keyword search's BM25 indexes are not ready: build them first (maktaba-api search-indexes)")
	}
	abandoned := h.PhraseAbandoned()
	defer func() { rep.PhraseAbandoned = h.PhraseAbandoned() - abandoned }()
	for _, c := range cases {
		res := RelevanceResult{RelevanceCase: c}
		var vs []savedVideo
		var took []time.Duration
		n := 1
		if c.Kind == RelCommon {
			n = runs + 2 // the first two warm the cache
		}
		for i := 0; i < n; i++ {
			t0 := time.Now()
			var err error
			vs, err = h.keywordVideos(ctx, c.Query, 500, Filters{})
			if err != nil {
				return rep, fmt.Errorf("%s %q: %w", c.Kind, c.Query, err)
			}
			if c.Kind != RelCommon || i >= 2 {
				took = append(took, time.Since(t0))
			}
		}
		for i, v := range vs {
			if i >= 500 {
				break
			}
			for _, w := range c.Want {
				if canonID(v.V) == canonID(w) && res.Rank == 0 {
					res.Rank = i + 1
				}
			}
		}
		switch c.Kind {
		case RelCommon:
			sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
			res.P95 = took[len(took)*95/100]
			top, all, err := h.perMinute(ctx, c.Query, vs)
			if err != nil {
				return rep, err
			}
			res.Note = fmt.Sprintf("%d videos; per minute: top 10 median %.2f, all matching median %.2f", len(vs), top, all)
			res.Passed = len(vs) > 0 && res.P95 < 50*time.Millisecond && top >= all
		default:
			res.Passed = res.Rank > 0 && res.Rank <= c.Top
		}
		rep.Total[c.Kind]++
		if res.Passed {
			rep.Passed[c.Kind]++
		}
		rep.Results = append(rep.Results, res)
	}
	return rep, nil
}

// perMinute is the median uses of q's word per minute of the top 10 videos and of every
// video found (up to 500): occurrences of its key in the document's normalised text over the
// video's duration.
func (h *Handler) perMinute(ctx context.Context, q string, vs []savedVideo) (top, all float64, err error) {
	keys := searchterms.QueryKeys(q)
	if len(keys) == 0 || len(vs) == 0 {
		return 0, 0, nil
	}
	ids := make([]string, 0, len(vs))
	for _, v := range vs {
		ids = append(ids, v.V)
	}
	ids = h.canonIDs(ids)
	args := []any{" " + keys[0] + " "}
	in := placeholders(ids, &args)
	rows, err := h.DB.QueryContext(ctx, `SELECT d.video_id::text,
			(length(t.x) - length(replace(t.x, $1, ''))) / length($1)::float8 / greatest(coalesce(v.duration_sec, 60) / 60.0, 1)
		  FROM video_search_docs d JOIN videos v ON v.id = d.video_id,
		       LATERAL (SELECT ' ' || regexp_replace(lower(d.search_text), '[^[:alnum:]ء-ي]+', ' ', 'g') || ' ' AS x) t
		 WHERE d.video_id IN (`+in+`)`, args...)
	if err != nil {
		return 0, 0, fmt.Errorf("per minute: %w", err)
	}
	defer rows.Close()
	rate := map[string]float64{}
	for rows.Next() {
		var id string
		var r float64
		if err := rows.Scan(&id, &r); err != nil {
			return 0, 0, err
		}
		rate[canonID(id)] = r
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	var t, a []float64
	for i, v := range vs {
		r := rate[canonID(v.V)]
		if i < 10 {
			t = append(t, r)
		}
		a = append(a, r)
	}
	return median(t), median(a), nil
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[len(s)/2]
}
