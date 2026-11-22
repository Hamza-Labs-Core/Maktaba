package search

// Suggestions as you type and did-you-mean (Epic 49 story 49.8), from keyword search's
// terms dictionary (search_terms, slot 0235: each library's words and how many documents
// hold each, kept by the keyword_index stage).
//
// Both read only the caller's libraries: a limited user's own (their library set, or the
// request's narrower filter), every library for an account that sees all. A parental
// limit hides videos within a library, and the dictionary cannot tell which video a word
// comes from: a limited account gets no word from it (no term suggestion, no
// did-you-mean), only titles and speakers of the videos it may see, and its own history.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchterms"
)

const (
	// fewResults: a search that found fewer videos than this gets a did-you-mean.
	fewResults = 5
	// similarEnough is the trigram similarity a replacement word needs at least. The spec
	// says 0.4; one wrong letter in a five-letter Arabic word (مدرصه for مدرسه) is 0.33.
	similarEnough = 0.3
	// moreFrequent: a word found in the dictionary is replaced only by one held by this
	// many times more documents (a transcript's own misspelling is a word too).
	moreFrequent = 10
	// suggestMin is how many letters a word needs before the dictionary suggests words
	// that start with it (fewer: the caller's history only).
	suggestMin = 2
	// suggestions is how many suggestions are answered at most, and the share of each
	// source in them.
	suggestions        = 10
	suggestHistory     = 3
	suggestTerms       = 5
	suggestTitles      = 3
	suggestSpeakers    = 2
	sqliteDidYouMeanOf = 2000 // the SQLite path compares a word with this many candidates
)

// arabicLetters and their ASCII spelling: search_terms.trgm's translate() (slot 0235).
const (
	arabicLetters = "ءابتثجحخدذرزسشصضطظعغفقكلمنهوي"
	asciiLetters  = "abcdefghijklmnopqrstuvwxyz012"
)

// libraryWhere is the condition on a table's library_id column for the caller's
// libraries (none: every library).
func libraryWhere(col string, libs []string, args *[]any) string {
	if len(libs) == 0 {
		return "true"
	}
	return col + " IN (" + placeholders(libs, args) + ")"
}

// termCount is a dictionary word in the caller's libraries.
type termCount struct {
	term, shown string
	df          int
}

// dfOf is how many documents of the caller's libraries hold each key.
func (h *Handler) dfOf(ctx context.Context, keys, libs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(keys) == 0 {
		return out, nil
	}
	var args []any
	in := placeholders(keys, &args)
	rows, err := h.DB.QueryContext(ctx, `SELECT term, sum(df) FROM search_terms
		 WHERE term IN (`+in+`) AND `+libraryWhere("library_id", libs, &args)+` GROUP BY term`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// termRows reads (term, shown, df) rows, one a library, and sums them by term: its
// documents in the caller's libraries, and the spelling of the library that holds it most.
func termRows(rows *sql.Rows) ([]termCount, error) {
	defer rows.Close()
	by := map[string]*termCount{}
	best := map[string]int{}
	var order []string
	for rows.Next() {
		var term, shown string
		var df int
		if err := rows.Scan(&term, &shown, &df); err != nil {
			return nil, err
		}
		c := by[term]
		if c == nil {
			c = &termCount{term: term}
			by[term] = c
			order = append(order, term)
		}
		c.df += df
		if df > best[term] {
			best[term], c.shown = df, shown
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]termCount, 0, len(order))
	for _, t := range order {
		out = append(out, *by[t])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].df != out[j].df {
			return out[i].df > out[j].df
		}
		return out[i].term < out[j].term
	})
	return out, nil
}

// closest is the frequent word of the caller's libraries closest to key (by trigram
// similarity, then by how many documents hold it), at least similarEnough; ok false when
// there is none.
func (h *Handler) closest(ctx context.Context, key string, libs []string) (termCount, float64, bool, error) {
	var rows *sql.Rows
	var err error
	if isSQLite(h.DB) {
		// no pg_trgm: the most frequent words that share the key's first letter or its
		// last two, compared in Go
		r := []rune(key)
		first, last := string(r[0]), string(r[len(r)-2:])
		args := []any{escapeLike(first) + "%", "%" + escapeLike(last)}
		rows, err = h.DB.QueryContext(ctx, `SELECT term, shown, df FROM search_terms
			 WHERE (term LIKE $1 ESCAPE '\' OR term LIKE $2 ESCAPE '\') AND `+libraryWhere("library_id", libs, &args)+`
			 ORDER BY df DESC, term LIMIT `+strconv.Itoa(sqliteDidYouMeanOf), args...)
	} else {
		sameScript := "s.term !~ '[^ -~]'"
		if searchterms.Arabic(key) {
			sameScript = "s.term ~ '[^ -~]'"
		}
		// the most similar first, then the most frequent: the candidates past the limit are
		// the least likely
		args := []any{key, similarEnough}
		rows, err = h.DB.QueryContext(ctx, `
			WITH q AS (SELECT translate($1::text, '`+arabicLetters+`', '`+asciiLetters+`') AS t)
			SELECT s.term, s.shown, s.df FROM search_terms s, q
			 WHERE s.trgm OPERATOR(public.%) q.t AND public.similarity(s.trgm, q.t) >= $2
			   AND `+sameScript+` AND `+libraryWhere("s.library_id", libs, &args)+`
			 ORDER BY public.similarity(s.trgm, q.t) DESC, s.df DESC, s.term LIMIT 200`, args...)
	}
	if err != nil {
		return termCount{}, 0, false, err
	}
	cands, err := termRows(rows)
	if err != nil {
		return termCount{}, 0, false, err
	}
	var best termCount
	bestSim := 0.0
	for _, c := range cands {
		if c.term == key || searchterms.Arabic(c.term) != searchterms.Arabic(key) {
			continue
		}
		s := searchterms.Similarity(c.term, key)
		if s < similarEnough {
			continue
		}
		if s > bestSim || (s == bestSim && (c.df > best.df || (c.df == best.df && c.term < best.term))) {
			best, bestSim = c, s
		}
	}
	return best, bestSim, bestSim > 0, nil
}

// didYouMean is q with each word the caller's libraries hold rarely or not at all
// replaced by the closest frequent word, when the search found fewer than fewResults
// videos; "" when nothing would change.
func (h *Handler) didYouMean(ctx context.Context, q string, libs []string, found int) (string, error) {
	if h.DB == nil || found >= fewResults || limited(ctx) || queryHasOperators(q) {
		return "", nil
	}
	words := searchterms.Words(q)
	if len(words) == 0 || len(words) > 8 {
		return "", nil
	}
	keys := make([]string, len(words))
	for i, w := range words {
		keys[i] = searchterms.Key(w)
	}
	df, err := h.dfOf(ctx, keys, libs)
	if err != nil {
		return "", fmt.Errorf("did you mean: %w", err)
	}
	changed := false
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w
		if utf8.RuneCountInString(keys[i]) < 3 {
			continue // too short to tell a misspelling from another word
		}
		c, _, ok, err := h.closest(ctx, keys[i], libs)
		if err != nil {
			return "", fmt.Errorf("did you mean: %w", err)
		}
		// a word no document holds takes the closest; a word some hold, one held by many
		// times more
		if ok && (df[keys[i]] == 0 || c.df >= moreFrequent*df[keys[i]]) {
			out[i] = c.shown
			changed = true
		}
	}
	if !changed {
		return "", nil
	}
	return strings.Join(out, " "), nil
}

// Suggestion is one suggestion as you type.
type Suggestion struct {
	Text string `json:"text"`
	// Kind is where it comes from: history (the caller's own searches), term (a word of
	// the caller's libraries: the query with its last word completed), title, speaker.
	Kind string `json:"kind"`
	// VideoID is a title suggestion's video.
	VideoID string `json:"video_id,omitempty"`
}

// Suggest answers suggestions as you type (story 49.8): the caller's recent searches
// first, then the query with its last word completed from the dictionary (most documents
// first), then video titles and speakers' names of the caller's libraries. suggestions
// keeps the texts alone, as before; items says where each comes from.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if rs := []rune(q); len(rs) > 200 {
		q = string(rs[:200])
	}
	items := []Suggestion{}
	if q != "" {
		var err error
		if items, err = h.suggest(r.Context(), p, q); err != nil {
			httperror.Write(w, r, httperror.Internal("suggest").Wrap(err))
			return
		}
	}
	texts := make([]string, len(items))
	for i, s := range items {
		texts[i] = s.Text
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"suggestions": texts, "items": items})
}

// callerLibraries is the libraries a caller may search (nil: all) and false when none.
func callerLibraries(p *principal.Principal) ([]string, bool) {
	if p.AccessAllLibraries {
		return nil, true
	}
	return p.Libraries, len(p.Libraries) > 0
}

func (h *Handler) suggest(ctx context.Context, p *principal.Principal, q string) ([]Suggestion, error) {
	out := []Suggestion{}
	seen := map[string]bool{}
	add := func(s Suggestion) bool {
		k := strings.ToLower(s.Text)
		if seen[k] || len(out) >= suggestions {
			return false
		}
		seen[k] = true
		out = append(out, s)
		return true
	}
	if h.DB == nil {
		return out, nil
	}
	hist, err := h.historySuggestions(ctx, p.UserID, q)
	if err != nil {
		return nil, err
	}
	for _, s := range hist {
		add(Suggestion{Text: s, Kind: "history"})
	}
	libs, some := callerLibraries(p)
	if !some {
		return out, nil
	}
	// the last word, completed from the dictionary: "الصبر عند ال" → "الصبر عند الله"
	words := searchterms.Words(q)
	endsInWord := len(words) > 0 && strings.HasSuffix(strings.ToLower(q), strings.ToLower(words[len(words)-1]))
	if endsInWord && !limited(ctx) {
		last := words[len(words)-1]
		if key := searchterms.Key(last); utf8.RuneCountInString(key) >= suggestMin {
			head := strings.TrimSpace(q[:len(q)-len(last)])
			terms, err := h.termsStartingWith(ctx, key, libs, suggestTerms+1)
			if err != nil {
				return nil, err
			}
			n := 0
			for _, t := range terms {
				text := t.shown
				if head != "" {
					text = head + " " + t.shown
				}
				if n < suggestTerms && add(Suggestion{Text: text, Kind: "term"}) {
					n++
				}
			}
		}
	}
	if utf8.RuneCountInString(q) < suggestMin {
		return out, nil
	}
	titles, err := h.titleSuggestions(ctx, q, libs)
	if err != nil {
		return nil, err
	}
	for i, t := range titles {
		if i >= suggestTitles {
			break
		}
		add(Suggestion{Text: t.text, Kind: "title", VideoID: t.id})
	}
	speakers, err := h.speakerSuggestions(ctx, q, libs)
	if err != nil {
		return nil, err
	}
	for i, s := range speakers {
		if i >= suggestSpeakers {
			break
		}
		add(Suggestion{Text: s, Kind: "speaker"})
	}
	return out, nil
}

// historySuggestions are the caller's own searches that start with q, most used first.
func (h *Handler) historySuggestions(ctx context.Context, userID, q string) ([]string, error) {
	if userID == "" {
		return nil, nil
	}
	rows, err := h.DB.QueryContext(ctx, `
		SELECT query FROM search_history
		WHERE query_norm LIKE $1 ESCAPE '\' AND user_id = $2
		ORDER BY hits DESC, last_used_at DESC LIMIT `+strconv.Itoa(suggestHistory),
		escapeLike(normaliseSuggestQuery(q))+"%", userID)
	if err != nil {
		// a database with no history table (a test's, an old twin's) has no history; any
		// other failure is the search's
		if m := err.Error(); strings.Contains(m, "no such table: search_history") ||
			strings.Contains(m, `relation "search_history" does not exist`) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// termsStartingWith are the dictionary's words of the caller's libraries whose key starts
// with key, most documents first. The rows are read most frequent first and the lookup
// stops at a few times n: ال starts most Arabic words, and summing every one of them was
// the slowest part of a suggestion (search_terms_df serves that order, the prefix index a
// rare prefix). For one library the order is exact; across several, a word read in fewer
// rows than the window holds can rank below its true place (its other libraries' rows fall
// past the window), so the window grows with the libraries.
func (h *Handler) termsStartingWith(ctx context.Context, key string, libs []string, n int) ([]termCount, error) {
	args := []any{escapeLike(key) + "%"}
	rows, err := h.DB.QueryContext(ctx, `SELECT term, shown, df FROM search_terms
		 WHERE term LIKE $1 ESCAPE '\' AND `+libraryWhere("library_id", libs, &args)+`
		 ORDER BY df DESC, term LIMIT `+strconv.Itoa(n*10*max(len(libs), 4)), args...)
	if err != nil {
		return nil, err
	}
	out, err := termRows(rows)
	if err != nil {
		return nil, err
	}
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

type titleSuggestion struct{ id, text string }

// shownTitle is the title a video shows, as videos_shown_title_prefix indexes it.
const shownTitle = "COALESCE(NULLIF(v.display_title_manual, ''), NULLIF(v.display_title, ''), v.title)"

// titleSuggestions are the titles of the caller's videos (the parental limit applied) that
// start with q, in order: an index scan of videos_shown_title_prefix that stops at the
// third.
func (h *Handler) titleSuggestions(ctx context.Context, q string, libs []string) ([]titleSuggestion, error) {
	args := []any{escapeLike(q) + "%"}
	// in the index's own order (text_pattern_ops sorts with ~<~): an index scan that stops
	// at the third title, not every title that starts so, sorted (درس starts them all)
	idCol, order := "v.id::text", "lower("+shownTitle+") USING OPERATOR(pg_catalog.~<~)"
	if isSQLite(h.DB) {
		idCol, order = "v.id", "lower("+shownTitle+")"
	}
	where := []string{"v.deleted_at IS NULL", "lower(" + shownTitle + ") LIKE lower($1) ESCAPE '\\'",
		libraryWhere("v.library_id", libs, &args)}
	where = append(where, limitWhere(ctx)...)
	rows, err := h.DB.QueryContext(ctx, `SELECT `+idCol+`, `+shownTitle+` FROM videos v
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY `+order+` LIMIT `+strconv.Itoa(suggestTitles), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []titleSuggestion
	for rows.Next() {
		var s titleSuggestion
		if err := rows.Scan(&s.id, &s.text); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// restrictedSpeakerCandidates is how many of the dictionary's names a restricted account's
// suggestion checks against the videos it may see.
const restrictedSpeakerCandidates = 20

// speakerSuggestions are the named speakers of the caller's libraries with a word start
// of their name that starts with q, named in the most documents first (search_speakers).
// The dictionary cannot tell which video a name comes from, so for a restricted account
// each of its first names is kept only if a video the account may see names it
// (speakers_name_idx, then the limit on that video): the names it gets are those it can
// find, in the dictionary's order.
func (h *Handler) speakerSuggestions(ctx context.Context, q string, libs []string) ([]string, error) {
	key := searchterms.Key(strings.Join(strings.Fields(q), " "))
	if key == "" {
		return nil, nil
	}
	n := suggestSpeakers
	if limited(ctx) {
		n = restrictedSpeakerCandidates
	}
	args := []any{escapeLike(key) + "%"}
	rows, err := h.DB.QueryContext(ctx, `SELECT name, sum(df) AS n FROM search_speakers
		 WHERE key LIKE $1 ESCAPE '\' AND `+libraryWhere("library_id", libs, &args)+`
		 GROUP BY name ORDER BY n DESC, name LIMIT `+strconv.Itoa(n), args...)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		var c int
		if err := rows.Scan(&name, &c); err != nil {
			_ = rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !limited(ctx) {
		return names, nil
	}
	var out []string
	for _, name := range names {
		if len(out) >= suggestSpeakers {
			break
		}
		args := []any{name}
		where := []string{"s.name = $1", "s.unknown_index IS NULL", "v.deleted_at IS NULL",
			libraryWhere("v.library_id", libs, &args)}
		where = append(where, limitWhere(ctx)...)
		var seen int
		if err := h.DB.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM speakers s
			 JOIN videos v ON v.id = s.video_id WHERE `+strings.Join(where, " AND ")+` LIMIT 1) x`, args...).Scan(&seen); err != nil {
			return nil, err
		}
		if seen > 0 {
			out = append(out, name)
		}
	}
	return out, nil
}
