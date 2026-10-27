package channel

import (
	"context"
	"os/exec"
	"testing"
)

// ffmpeg answers -h filter=<unknown> with "Unknown filter" and status 0: HasFilter
// reads the answer, not the status. A slate's font is given only to an ffmpeg that
// can draw text (else the slate would fail whole).
func TestHasFilterReadsTheHelp(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	ctx := context.Background()
	if !HasFilter(ctx, "ffmpeg", "overlay") {
		t.Error("overlay not found")
	}
	if HasFilter(ctx, "ffmpeg", "nosuchfilter") {
		t.Error("an unknown filter was found")
	}
	if HasFilter(ctx, "/nonexistent/ffmpeg", "overlay") {
		t.Error("a missing binary has filters")
	}
	if !HasFilter(ctx, "ffmpeg", "drawtext") && SlateFont(ctx, "ffmpeg") != "" {
		t.Error("a font for slate captions with an ffmpeg that cannot draw them")
	}
}
