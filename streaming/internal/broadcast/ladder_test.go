package broadcast

import (
	"errors"
	"strings"
	"testing"
)

func count(args []string, s string) int {
	n := 0
	for _, a := range args {
		if a == s {
			n++
		}
	}
	return n
}

// T02: copy only, copy + 480p, and the default three rungs each make one ffmpeg with an
// HLS output per rung; the top rung is copied, the lower rungs use the channel's encoder
// family on its keyframe grid, and software rungs are refused unless allowed.
func TestLadderArgs(t *testing.T) {
	for _, c := range []struct {
		rungs   []string
		enc     string
		outputs int
		codec   string
	}{
		{[]string{"src"}, EncVideoToolbox, 1, ""},
		{[]string{"src", "480p"}, EncVideoToolbox, 2, "h264_videotoolbox"},
		{[]string{"src", "720p", "480p"}, EncNVENC, 3, "h264_nvenc"},
		{[]string{"src", "720p", "480p"}, EncVAAPI, 3, "h264_vaapi"},
		{[]string{"src", "720p"}, EncQSV, 2, "h264_qsv"},
	} {
		args, err := Args(LadderSpec{Rungs: c.rungs, Encoder: c.enc, KeyintSeconds: 2, FPS: 25, Dir: "/b", StartNumber: 42})
		if err != nil {
			t.Fatalf("%v %s: %v", c.rungs, c.enc, err)
		}
		if got := count(args, "hls"); got != c.outputs {
			t.Errorf("%v: %d HLS outputs, want %d", c.rungs, got, c.outputs)
		}
		if count(args, "pipe:0") != 1 {
			t.Errorf("%v: not one input", c.rungs)
		}
		if count(args, "copy") != 1 {
			t.Errorf("%v: the top rung is not copied once", c.rungs)
		}
		if c.codec != "" && count(args, c.codec) != len(c.rungs)-1 {
			t.Errorf("%v: %s on %d rungs", c.rungs, c.codec, count(args, c.codec))
		}
		joined := strings.Join(args, " ")
		if len(c.rungs) > 1 && (!strings.Contains(joined, "-g 50") || !strings.Contains(joined, "n_forced*2")) {
			t.Errorf("%v: off the keyframe grid: %s", c.rungs, joined)
		}
		if !strings.Contains(joined, "-start_number 42") || !strings.Contains(joined, "/b/src/%d.ts") {
			t.Errorf("%v: segment naming: %s", c.rungs, joined)
		}
		if Sessions(c.rungs) != len(c.rungs)-1 {
			t.Errorf("%v: sessions %d", c.rungs, Sessions(c.rungs))
		}
	}

	if _, err := Args(LadderSpec{Rungs: []string{"src", "480p"}, Encoder: "libx264", Dir: "/b"}); !errors.Is(err, ErrSoftwareRungs) {
		t.Fatalf("software rungs without the setting: %v", err)
	}
	args, err := Args(LadderSpec{Rungs: []string{"src", "480p"}, Encoder: "", AllowSoftware: true, Dir: "/b"})
	if err != nil || count(args, "libx264") != 1 {
		t.Fatalf("software rungs allowed: %v %v", args, err)
	}
	// copy only never needs an encoder, software or not
	if _, err := Args(LadderSpec{Rungs: []string{"src"}, Encoder: EncSoftware, Dir: "/b"}); err != nil {
		t.Fatalf("copy only on software: %v", err)
	}
	if _, err := Args(LadderSpec{Rungs: []string{"480p", "src"}, Encoder: EncNVENC, Dir: "/b"}); err == nil {
		t.Fatal("src below a lower rung was accepted")
	}
	if _, err := Args(LadderSpec{Rungs: []string{"src", "4k"}, Encoder: EncNVENC, Dir: "/b"}); err == nil {
		t.Fatal("an unknown rung was accepted")
	}
	if _, err := Args(LadderSpec{Dir: "/b"}); err == nil {
		t.Fatal("an empty ladder was accepted")
	}
}
