package handlers

import (
	"strings"
	"testing"
)

func TestSignPlaylist_ChildURIsCarryTheSessionToken(t *testing.T) {
	const tok = "aaa.bbb.ccc"
	master := "#EXTM3U\r\n#EXT-X-VERSION:6\r\n" +
		"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"s\",URI=\"subs/auto.vtt\"\r\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1200000\r\nv0/index.m3u8\r\n" +
		"#EXT-X-KEY:METHOD=AES-128,URI=\"https://keys.example/k\"\r\n" +
		"https://cdn.example/x.ts\r\nalready.ts?sig=old\r\nq.ts?x=1\r\n"
	out := string(SignPlaylist([]byte(master), tok))
	for _, want := range []string{
		"v0/index.m3u8?sig=" + tok + "\r\n",
		`URI="subs/auto.vtt?sig=` + tok + `"`,
		`URI="https://keys.example/k"`,
		"https://cdn.example/x.ts\r\n",
		"already.ts?sig=old\r\n",
		"q.ts?x=1&sig=" + tok,
		"#EXT-X-VERSION:6\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	index := "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.0,\nseg-0.ts\n#EXTINF:6.0,\nseg-1.ts\n#EXT-X-ENDLIST\n"
	out = string(SignPlaylist([]byte(index), tok))
	if strings.Count(out, "?sig="+tok) != 3 || !strings.HasSuffix(out, "#EXT-X-ENDLIST\n") {
		t.Fatalf("index:\n%s", out)
	}
}

func TestSignMPD_TemplateURLs(t *testing.T) {
	mpd := `<SegmentTemplate initialization="init-$RepresentationID$.m4s" media="chunk-$RepresentationID$-$Number%05d$.m4s?a=1"/>`
	out := string(SignMPD([]byte(mpd), "t.k.n"))
	if !strings.Contains(out, `initialization="init-$RepresentationID$.m4s?sig=t.k.n"`) ||
		!strings.Contains(out, `media="chunk-$RepresentationID$-$Number%05d$.m4s?a=1&amp;sig=t.k.n"`) {
		t.Fatalf("mpd: %s", out)
	}
}
