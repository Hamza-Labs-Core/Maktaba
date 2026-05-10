package tags

import "testing"

func TestNormaliseTagName(t *testing.T) {
	cases := map[string]string{
		"Tafsir":   "tafsir",
		" Tafsir ": "tafsir",
		"TAFSIR":   "tafsir",
		"tafsir":   "tafsir",
	}
	for in, want := range cases {
		if got := NormaliseTagName(in); got != want {
			t.Errorf("Normalise(%q) = %q want %q", in, got, want)
		}
	}
}
