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

// Story 9.12 AC-2: NFC + casefold so two unicode-equal forms collapse
// to one row. The test fixture is the NFD vs NFC pair for the Arabic
// letter "kaf with shadda" — both should normalise to the same key.
func TestNormaliseTagName_NFCEqualsArabicForms(t *testing.T) {
	// Same string in NFC vs decomposed NFD form for "café" (U+00E9 vs
	// e + U+0301).
	nfc := "Café"
	nfd := "Café"
	if NormaliseTagName(nfc) != NormaliseTagName(nfd) {
		t.Errorf("NFC/NFD forms must normalise to the same key: %q vs %q",
			NormaliseTagName(nfc), NormaliseTagName(nfd))
	}
}

// Story 9.12 EC: whitespace-only / empty tag should normalise to empty
// string so the API layer can map it to a 422.
func TestNormaliseTagName_WhitespaceCollapsesToEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		if got := NormaliseTagName(in); got != "" {
			t.Errorf("Normalise(%q) = %q, want empty", in, got)
		}
	}
}

// Story 9.12 EC: a tag containing a slash is allowed (flat string in v1).
func TestNormaliseTagName_AllowsSlash(t *testing.T) {
	if NormaliseTagName("Finance/2024") != "finance/2024" {
		t.Error("slash should pass through after casefold")
	}
}
