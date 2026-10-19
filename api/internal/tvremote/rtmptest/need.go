package rtmptest

import (
	"os"
	"os/exec"
	"testing"
)

// RequireEnv set to 1 (the media-e2e CI job) turns the media tests' skips
// for a missing ffmpeg/ffprobe into failures, so a runner without the tools
// can never pass them as skipped.
const RequireEnv = "MAKTABA_REQUIRE_FFMPEG"

// NeedFFmpeg skips the test (or, with MAKTABA_REQUIRE_FFMPEG=1, fails it)
// unless every one of bins is on PATH; no bins means ffmpeg.
func NeedFFmpeg(t testing.TB, bins ...string) {
	t.Helper()
	if len(bins) == 0 {
		bins = []string{"ffmpeg"}
	}
	for _, b := range bins {
		if _, err := exec.LookPath(b); err != nil {
			Unavailable(t, b+" not on PATH")
		}
	}
}

// Unavailable skips the test for why, or fails it with
// MAKTABA_REQUIRE_FFMPEG=1.
func Unavailable(t testing.TB, why string) {
	t.Helper()
	if os.Getenv(RequireEnv) == "1" {
		t.Fatalf("%s (%s=1: the media tests must run here)", why, RequireEnv)
	}
	t.Skip(why)
}
