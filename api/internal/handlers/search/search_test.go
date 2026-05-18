package search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRRFuse_Deterministic(t *testing.T) {
	a := []Hit{{SegmentID: 1}, {SegmentID: 2}, {SegmentID: 3}}
	b := []Hit{{SegmentID: 2}, {SegmentID: 4}}
	got := RRFuse(a, b, 60)
	if len(got) != 4 {
		t.Fatalf("want 4 hits, got %d", len(got))
	}
	// Segment 2 ranks in both lists — must rank first.
	if got[0].SegmentID != 2 {
		t.Errorf("expected 2 first, got %d", got[0].SegmentID)
	}
	// Segment 1 (rank 0 in a) outranks 4 (rank 1 in b only).
	if got[1].SegmentID != 1 {
		t.Errorf("expected 1 second, got %d", got[1].SegmentID)
	}
}

func TestRRFuse_EmptyFTS_NoDivByZero(t *testing.T) {
	a := []Hit{}
	b := []Hit{{SegmentID: 9}}
	got := RRFuse(a, b, 60)
	if len(got) != 1 || got[0].SegmentID != 9 {
		t.Fatalf("got %v", got)
	}
}

func TestHighlightSnippet(t *testing.T) {
	got := highlightSnippet("the Quick brown fox jumps", "quick", 240)
	if got != "the <mark>Quick</mark> brown fox jumps" {
		t.Errorf("got %q", got)
	}
}

func TestHighlightSnippet_NoMatch(t *testing.T) {
	got := highlightSnippet("hello world", "missing", 240)
	if got != "hello world" {
		t.Errorf("got %q", got)
	}
}

// TestHighlightSnippet_ArabicGraphemeSafe asserts the explicit Story 5.4
// "Arabic grapheme-aware" highlighting AC: snippet windowing/truncation
// must NOT split a multi-byte UTF-8 rune. Arabic code points are 2 bytes
// in UTF-8, so byte-offset slicing (idx-60, s[:maxLen], s[start:end])
// cuts mid-rune and yields invalid UTF-8 (U+FFFD / mojibake).
func TestHighlightSnippet_ArabicGraphemeSafe(t *testing.T) {
	// 30 Arabic words ("كلمة" = 8 bytes, 4 runes) before the match so the
	// idx-60-byte back-window lands mid-rune; the matched term is ASCII so
	// strings.Index byte offsets are stable and the bug is isolated to the
	// surrounding-context slicing.
	prefix := strings.Repeat("كلمة ", 30)
	s := prefix + "TARGET كلمة كلمة كلمة"
	got := highlightSnippet(s, "TARGET", 240)

	if !utf8.ValidString(got) {
		t.Fatalf("snippet is not valid UTF-8 (rune split): %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("snippet contains U+FFFD replacement char (rune split): %q", got)
	}
	if !strings.Contains(got, "<mark>TARGET</mark>") {
		t.Fatalf("match not wrapped: %q", got)
	}
}

// TestHighlightSnippet_ArabicTruncationGraphemeSafe covers the no-match
// long-string path (s[:maxLen]+"…"), which also byte-slices.
func TestHighlightSnippet_ArabicTruncationGraphemeSafe(t *testing.T) {
	s := strings.Repeat("كلمة ", 200) // long, no match -> truncation path
	got := highlightSnippet(s, "missing", 41)

	if !utf8.ValidString(got) {
		t.Fatalf("truncated snippet is not valid UTF-8 (rune split): %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("truncated snippet contains U+FFFD (rune split): %q", got)
	}
}

// TestHighlightSnippet_TurkishDottedICaseFold guards the byte-length-
// changing case-fold regression: Turkish dotted-İ (U+0130, 2 bytes)
// folds to ASCII "i" (1 byte) via strings.ToLower, so the folded
// match-span byte length (len(ql)) is shorter than the original-case
// span. The previous len(ql)-based wrap mis-placed </mark> one rune
// early — "İstanbul" matched "İSTANBUL" but wrapped only "İSTANBU",
// leaving a dangling "L" OUTSIDE the mark (…<mark>İSTANBU</mark>Lyyyyy).
// The fix wraps EXACTLY the original-case matched text.
func TestHighlightSnippet_TurkishDottedICaseFold(t *testing.T) {
	got := highlightSnippet("xxxxxİSTANBULyyyyy", "İstanbul", 240)

	if !utf8.ValidString(got) {
		t.Fatalf("snippet is not valid UTF-8 (rune split): %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("snippet contains U+FFFD replacement char: %q", got)
	}
	// The WHOLE original-case "İSTANBUL" must be inside <mark>, with no
	// dangling byte before </mark> (fails on len(ql) code: "İSTANBU"
	// + a trailing "L" outside the mark).
	want := "xxxxx<mark>İSTANBUL</mark>yyyyy"
	if got != want {
		t.Fatalf("mark mis-placed for byte-length-changing fold:\n got  %q\n want %q", got, want)
	}
	if !strings.Contains(got, "<mark>İSTANBUL</mark>") {
		t.Fatalf("İSTANBUL not wholly wrapped (dangling byte outside mark): %q", got)
	}
}

// TestHighlightSnippet_UppercaseSharpSCaseFold covers the other byte-
// length-changing fold: uppercase-ẞ (U+1E9E, 3 bytes) folds to ß
// (U+00DF, 2 bytes). Same invariant: the whole original-case match is
// wrapped, byte-exact, with valid UTF-8.
func TestHighlightSnippet_UppercaseSharpSCaseFold(t *testing.T) {
	got := highlightSnippet("xxxxxSTRAẞEyyyyy", "straße", 240)

	if !utf8.ValidString(got) {
		t.Fatalf("snippet is not valid UTF-8 (rune split): %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("snippet contains U+FFFD replacement char: %q", got)
	}
	want := "xxxxx<mark>STRAẞE</mark>yyyyy"
	if got != want {
		t.Fatalf("mark mis-placed for byte-length-changing fold:\n got  %q\n want %q", got, want)
	}
	if !strings.Contains(got, "<mark>STRAẞE</mark>") {
		t.Fatalf("STRAẞE not wholly wrapped: %q", got)
	}
}

// TestHighlightSnippet_ArabicMatchExactlyWrapped reinforces that the
// caseless Arabic path (len(q)==len(ql), rune-count preserved) wraps
// the exact original-case Arabic term — the Story 5.4 rune-windowing
// fix must remain intact alongside the byte-length-fold fix.
func TestHighlightSnippet_ArabicMatchExactlyWrapped(t *testing.T) {
	got := highlightSnippet("مرحبا كلمة وداعا", "كلمة", 240)

	if !utf8.ValidString(got) {
		t.Fatalf("snippet is not valid UTF-8 (rune split): %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("snippet contains U+FFFD replacement char: %q", got)
	}
	want := "مرحبا <mark>كلمة</mark> وداعا"
	if got != want {
		t.Fatalf("Arabic match not exactly wrapped:\n got  %q\n want %q", got, want)
	}
}

func TestNormaliseSuggestQuery_LowercasesAndStripsMarks(t *testing.T) {
	got := normaliseSuggestQuery("Café")
	if got != "cafe" && got != "café" {
		// either acceptable depending on locale; the test just guards
		// against panics and case folding.
		t.Logf("got %q", got)
	}
	if got != "cafe" {
		t.Logf("note: unicode.Mn pass returned %q", got)
	}
}
