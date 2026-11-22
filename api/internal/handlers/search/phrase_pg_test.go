package search

import "testing"

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
	} {
		var got bool
		if err := db.QueryRow(`SELECT $1::text ~ $2`, text, pat).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
	if phrasePattern("الصبر") != "" {
		t.Error("one word has no phrase")
	}
}
