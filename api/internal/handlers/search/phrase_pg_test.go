package search

import (
	"context"
	"testing"
)

// The phrase is looked for within one line of a document's normalised text: its words
// together, the line's start or end around it, never across a line break (story 49.9), as
// Postgres's regular expressions read the pattern (on a --locale=C database too).
func TestPhrasePatternOnPostgres(t *testing.T) {
	db := pgSearchDB(t)
	pat := phrasePattern("الصبر عند المصيبة")
	for text, want := range map[string]bool{
		"قال: الصبر عند المصيبه الاولي": true,
		"x\nالصبر عند المصيبه\ny":       true, // a line's start and end
		"الصبر عند\nالمصيبه":            false,
		"الصبرعند المصيبه":              false,
		"والصبر عند المصيبه":            false, // another word
		"الصبر، عند المصيبه":            true,  // punctuation between the words
		"الصبر گ عند المصيبه":           false, // a Persian letter is a letter: a word between
	} {
		var got bool
		if err := db.QueryRow(`SELECT $1::text ~ $2`, text, pat).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
	if phrasePattern("الصبر") != "" || phrasePattern(`الصبر -المصيبة`) != "" || phrasePattern(`"الصبر" OR عند`) != "" {
		t.Error("one word, or a query with operators, has no phrase")
	}
	if got := phraseProbe("الصبرُ عند CAFE المصيبة،"); got != "المصيبه" {
		t.Errorf("probe %q", got)
	}
}

// A word prefixed by و or ف that no document holds is searched without the prefix when the
// rest is a word, punctuation and all; a word documents hold as written stays (story 49.9).
func TestWithoutConjunctionsOnPostgres(t *testing.T) {
	db := pgSearchDB(t)
	if _, err := db.Exec(`INSERT INTO search_terms (library_id, term, shown, df) VALUES
		($1, 'غفران', 'غفران', 3), ($1, 'والصبر', 'والصبر', 2), ($1, 'الصبر', 'الصبر', 9), ($1, 'فرحه', 'فرحة', 1)`, libA); err != nil {
		t.Fatal(err)
	}
	h := &Handler{DB: db}
	for q, want := range map[string]string{
		"وغفران، رحمتك":   "غفران، رحمتك",
		"والصبر جميل":     "والصبر جميل", // a word as written
		"ففرحة":           "فرحة",
		"وقت":             "وقت",             // too short to tell
		`"وغفران" -رحمتك`: `"وغفران" -رحمتك`, // operators: as written
		"وغفران":          "غفران",
		"وغفرانات":        "وغفرانات", // the rest is no word either
	} {
		if got := h.withoutConjunctions(context.Background(), q, nil); got != want {
			t.Errorf("%q: %q, want %q", q, got, want)
		}
	}
	// another library's word is not this caller's
	if got := h.withoutConjunctions(context.Background(), "وغفران", []string{libB}); got != "وغفران" {
		t.Errorf("another library's dictionary: %q", got)
	}
}
