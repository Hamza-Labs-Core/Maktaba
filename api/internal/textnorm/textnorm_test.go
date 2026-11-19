package textnorm

import "testing"

// Epic 49 story 49.4: the spellings one word is written in fold to one form.
func TestNormalizeFoldsArabicSpellings(t *testing.T) {
	for in, want := range map[string]string{
		"إسلام":      "اسلام",
		"أحمد":       "احمد",
		"آمن":        "امن",
		"ٱلله":       "الله",
		"هدى":        "هدي",
		"مدرسة":      "مدرسه",
		"مؤمن":       "مومن",
		"سائل":       "سايل",
		"اللَّهُ":    "الله",
		"صبــر":      "صبر",
		"الرَّحْمٰن": "الرحمن",
		"١٢٣":        "123",
		"۴۵۶":        "456",
		"café 42":    "café 42",
		"":           "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// The forms a query and a text fold to meet: اسلام finds إسلام, هدي finds هدى, and so on.
func TestNormalizedFormsMeet(t *testing.T) {
	for _, p := range [][2]string{{"اسلام", "إسلام"}, {"هدي", "هدى"}, {"مدرسه", "مدرسة"}, {"الله", "اللَّهُ"}} {
		if Normalize(p[0]) != Normalize(p[1]) {
			t.Errorf("%q and %q do not meet: %q, %q", p[0], p[1], Normalize(p[0]), Normalize(p[1]))
		}
	}
}
