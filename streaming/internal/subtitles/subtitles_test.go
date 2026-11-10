package subtitles

import (
	"strings"
	"testing"
)

func TestAsVTT(t *testing.T) {
	v, err := AsVTT("vtt", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhi\n")
	if err != nil || !strings.HasPrefix(string(v), "WEBVTT") {
		t.Fatalf("vtt: %q %v", v, err)
	}
	v, err = AsVTT("srt", "1\n00:00:01,000 --> 00:00:02,000\n<b>hi</b>\n")
	if err != nil || !strings.Contains(string(v), "00:00:01.000 --> 00:00:02.000") || strings.Contains(string(v), "<b>") {
		t.Fatalf("srt: %q %v", v, err)
	}
}
