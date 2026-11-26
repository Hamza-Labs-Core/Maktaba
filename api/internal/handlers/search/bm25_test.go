package search

import (
	"reflect"
	"testing"
)

func TestQueryHasOperators(t *testing.T) {
	for q, want := range map[string]bool{
		"الصبر عند المصيبة": false,
		"well-known":        false, // a hyphen inside a word is no negation
		"2024-01-01":        false,
		`"الصبر عند"`:       true,
		"الصبر -النار":      true,
		"cat OR dog":        true,
		"cat or dog":        true,
		"orange":            false,
	} {
		if got := queryHasOperators(q); got != want {
			t.Errorf("queryHasOperators(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestLooseStems(t *testing.T) {
	got := looseStems([]string{"come", "hope", "صبر", "the", "patienc"})
	if want := []string{"com", "hop", "صبر", "the", "patienc"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("looseStems = %q, want %q", got, want)
	}
}

func TestLineWordsAndFolding(t *testing.T) {
	words, norm := lineWords("قال: إِسْلَام، ثم Café-time")
	if want := []string{"قال", "إِسْلَام", "ثم", "Café", "time"}; !reflect.DeepEqual(words, want) {
		t.Fatalf("words = %q", words)
	}
	if want := []string{"قال", "اسلام", "ثم", "cafe", "time"}; !reflect.DeepEqual(norm, want) {
		t.Fatalf("norm = %q", norm)
	}
	if n := hasAll(norm, []string{"اسلام", "cafe"}); n != 2 {
		t.Fatalf("hasAll = %d", n)
	}
	if n := hasAll(norm, []string{"اسلام", "absent"}); n != 0 {
		t.Fatalf("hasAll with a missing lexeme = %d", n)
	}
}

func TestFoldWordLatinAccents(t *testing.T) {
	for input, want := range map[string]string{
		"ASCII": "ascii", "CAFÉ Cafe\u0301": "cafe cafe",
		"øØ łŁ æÆ œŒ ßẞ":                          "oo ll aeae oeoe ssss",
		"إِسْلَام Øresund Łódź Æsir Œuvre Straße": "اسلام oresund lodz aesir oeuvre strasse",
	} {
		if got := foldWord(input); got != want {
			t.Errorf("foldWord(%q) = %q, want %q", input, got, want)
		}
	}
}
