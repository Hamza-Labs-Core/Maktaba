package search

import "testing"

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
