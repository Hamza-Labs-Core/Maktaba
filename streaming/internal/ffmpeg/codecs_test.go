package ffmpeg

import (
	"strings"
	"testing"
)

// The master playlist names High 4.2 for every rung, and every rung's encoder is asked
// for High: the two never disagree (a strict player refuses a stream that is not the
// CODECS it was told).
func TestMasterCodecsMatchTheEncodedProfile(t *testing.T) {
	master := string(BuildMasterPlaylistFor(DefaultLadder(0)))
	if n := strings.Count(master, `CODECS="avc1.64002a,mp4a.40.2"`); n != 3 {
		t.Errorf("%d rungs name High 4.2, want 3:\n%s", n, master)
	}
	for _, enc := range []string{"libx264", "h264_videotoolbox", "h264_nvenc", "h264_qsv"} {
		args := strings.Join(rungArgs(1, DefaultLadder(0)[1], enc, EncodePlan{}), " ")
		if !strings.Contains(args, "-profile:v:1 high") {
			t.Errorf("%s: no High profile asked for: %s", enc, args)
		}
	}
}
