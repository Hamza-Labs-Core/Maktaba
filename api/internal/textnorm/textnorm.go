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
var fold = func() map[rune]rune {
	m := map[rune]rune{
		'أ': 'ا', 'إ': 'ا', 'آ': 'ا', 'ٱ': 'ا',
		'ى': 'ي', 'ة': 'ه', 'ؤ': 'و', 'ئ': 'ي',
		'ـ': -1, // tatweel
		'ٰ': -1, // superscript alef
	}
	for r := rune(0x064B); r <= 0x0652; r++ { // the harakat
		m[r] = -1
	}
	for i := rune(0); i < 10; i++ {
		m[0x0660+i] = '0' + i // Arabic-Indic digits
		m[0x06F0+i] = '0' + i // Persian (Eastern Arabic-Indic) digits
	}
	return m
}()

// Normalize folds s as maktaba_normalize() does.
func Normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if f, ok := fold[r]; ok {
			return f
		}
		return r
	}, s)
}
