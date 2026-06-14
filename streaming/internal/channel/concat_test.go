package channel

import (
	"strings"
	"testing"
)

func TestBuildConcat_HeadIsSeeked(t *testing.T) {
	blocks := []ProgramBlock{
		mkBlock("2026-06-14T20:00:00Z", "2026-06-14T20:30:00Z", 0, 1_800_000),
		mkBlock("2026-06-14T20:30:00Z", "2026-06-14T21:00:00Z", 0, 1_800_000),
	}
	j := Join{Index: 0, Block: blocks[0], SeekMS: 600_000} // 10 min in
	entries := BuildConcat(blocks, j, 4)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].InpointSec != 600 {
		t.Errorf("head inpoint = %v, want 600", entries[0].InpointSec)
	}
	// Second block plays from its own offset (0), not the seek.
	if entries[1].InpointSec != 0 {
		t.Errorf("second inpoint = %v, want 0", entries[1].InpointSec)
	}
	if entries[1].OutpointSec != 1800 {
		t.Errorf("second outpoint = %v, want 1800", entries[1].OutpointSec)
	}
}

func TestBuildConcat_LookaheadBounded(t *testing.T) {
	var blocks []ProgramBlock
	for i := 0; i < 10; i++ {
		blocks = append(blocks, mkBlock("2026-06-14T20:00:00Z", "2026-06-14T20:30:00Z", 0, 1_800_000))
	}
	j := Join{Index: 0, Block: blocks[0]}
	entries := BuildConcat(blocks, j, 3)
	if len(entries) != 3 {
		t.Errorf("lookahead should bound to 3, got %d", len(entries))
	}
}

func TestBuildConcat_SkipsMissingPaths(t *testing.T) {
	b0 := mkBlock("2026-06-14T20:00:00Z", "2026-06-14T20:30:00Z", 0, 1_800_000)
	b1 := mkBlock("2026-06-14T20:30:00Z", "2026-06-14T21:00:00Z", 0, 1_800_000)
	b1.Path = "" // unresolved (filler before slot 0085) — must be skipped
	j := Join{Index: 0, Block: b0}
	entries := BuildConcat([]ProgramBlock{b0, b1}, j, 4)
	if len(entries) != 1 {
		t.Errorf("missing-path block should be skipped, got %d entries", len(entries))
	}
}

func TestFormatConcat_EscapesQuotesAndOmitsZeroInpoint(t *testing.T) {
	entries := []ConcatEntry{
		{Path: "/media/it's a show.mkv", InpointSec: 0, OutpointSec: 1800},
		{Path: "/media/b.mkv", InpointSec: 12.5, OutpointSec: 60},
	}
	out := FormatConcat(entries)
	if !strings.HasPrefix(out, "ffconcat version 1.0\n") {
		t.Error("missing ffconcat header")
	}
	if !strings.Contains(out, `file '/media/it'\''s a show.mkv'`) {
		t.Errorf("quote not escaped:\n%s", out)
	}
	// Zero inpoint omitted; non-zero present.
	if strings.Contains(out, "inpoint 0\n") {
		t.Error("zero inpoint should be omitted")
	}
	if !strings.Contains(out, "inpoint 12.5") {
		t.Errorf("non-zero inpoint missing:\n%s", out)
	}
	if !strings.Contains(out, "outpoint 1800") {
		t.Errorf("outpoint missing:\n%s", out)
	}
}

func TestFormatSec_TrimsTrailingZeros(t *testing.T) {
	cases := map[float64]string{600: "600", 12.5: "12.5", 12.34: "12.34", 0.1: "0.1"}
	for in, want := range cases {
		if got := formatSec(in); got != want {
			t.Errorf("formatSec(%v) = %q want %q", in, got, want)
		}
	}
}
