// Package textnorm is the Arabic normalisation of keyword search (Epic 49 story 49.4) in Go:
// the same folding as Postgres's maktaba_normalize() (migration 0231), for what normalises
// in the application (SQLite installs, whose FTS5 indexes and matches the normalised text).
// A test keeps the two equal on a real Postgres.
//
// It removes harakat (U+064B-U+0652, U+0670) and tatweel (U+0640); folds أ إ آ ٱ to ا, ى to
// ي, ة to ه, ؤ to و, ئ to ي; and turns Arabic-Indic and Persian digits into ASCII. Latin is left
// as it is (Postgres's unaccent step and SQLite's remove_diacritics handle its accents).
package textnorm

import "strings"

// fold is each folded rune's form; -1 removes it.
// fold is the folding as a table over the runes below foldEnd (every rune it folds is an
// Arabic one): 0 leaves a rune, -1 deletes it, anything else replaces it. A table, not a
// map: search folds every word of a page's lines (story 49.7).
var fold = func() *[foldEnd]rune {
	var t [foldEnd]rune
	for r, f := range map[rune]rune{
		'أ': 'ا', 'إ': 'ا', 'آ': 'ا', 'ٱ': 'ا',
		'ى': 'ي', 'ة': 'ه', 'ؤ': 'و', 'ئ': 'ي',
		'ـ': -1, // tatweel
		'ٰ': -1, // superscript alef
	} {
		t[r] = f
	}
	for r := rune(0x064B); r <= 0x0652; r++ { // the harakat
		t[r] = -1
	}
	for i := rune(0); i < 10; i++ {
		t[0x0660+i] = '0' + i // Arabic-Indic digits
		t[0x06F0+i] = '0' + i // Persian (Eastern Arabic-Indic) digits
	}
	return &t
}()

// foldEnd bounds fold: every rune it folds is below it.
const foldEnd = 0x0700

// Normalize folds s as maktaba_normalize() does.
func Normalize(s string) string {
	i := strings.IndexFunc(s, folds)
	if i < 0 {
		return s // nothing to fold
	}
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:i])
	for _, r := range s[i:] {
		if r < foldEnd {
			if f := fold[r]; f != 0 {
				if f > 0 {
					b.WriteRune(f)
				}
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// folds reports whether the folding changes r.
func folds(r rune) bool { return r < foldEnd && fold[r] != 0 }
