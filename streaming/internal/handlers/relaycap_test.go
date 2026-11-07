package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const ladder = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-STREAM-INF:BANDWIDTH=20000000,RESOLUTION=3840x2160,CODECS="avc1.640033,mp4a.40.2"
2160p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=8000000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2"
1080p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2500000,RESOLUTION=1280x720,CODECS="avc1.64001f,mp4a.40.2"
720p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1500000,RESOLUTION=854x480,CODECS="avc1.64001e,mp4a.40.2"
480p/index.m3u8
`

// Epic 50 story 50.19 T05: renditions of 20 / 8 / 2.5 / 1.5 Mbps; a relayed request capped at
// 3000 kbps lists only those within the cap less 10 % (2.5 and 1.5); at 1000 none fits 0.9
// Mbps, so the lowest stays; on the LAN, over direct access or with a bad cap, nothing changes.
func TestTheMasterFitsTheCap(t *testing.T) {
	req := func(via, cap string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/stream/s/manifest.m3u8", nil)
		if via != "" {
			r.Header.Set("X-Maktaba-Via", via)
		}
		r.Header.Set(RelayCapHeader, cap)
		return r
	}
	got := string(FitMasterToCap([]byte(ladder), relayCapBits(req("relay", "3000"))))
	for _, gone := range []string{"2160p", "1080p", "BANDWIDTH=20000000", "BANDWIDTH=8000000"} {
		if strings.Contains(got, gone) {
			t.Errorf("capped at 3000: %s still listed:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"#EXTM3U", "#EXT-X-VERSION:6", "720p/index.m3u8", "480p/index.m3u8"} {
		if !strings.Contains(got, kept) {
			t.Errorf("capped at 3000: %s dropped:\n%s", kept, got)
		}
	}
	slow := string(FitMasterToCap([]byte(ladder), relayCapBits(req("relay", "1000"))))
	if strings.Count(slow, "#EXT-X-STREAM-INF") != 1 || !strings.Contains(slow, "480p/index.m3u8") {
		t.Errorf("nothing fits: the lowest stays:\n%s", slow)
	}
	for _, r := range []*http.Request{req("", "3000"), req("direct", "3000"), req("relay", ""), req("relay", "65536000000")} {
		if string(FitMasterToCap([]byte(ladder), relayCapBits(r))) != ladder {
			t.Errorf("uncapped (via %q cap %q) changed the playlist", r.Header.Get("X-Maktaba-Via"), r.Header.Get(RelayCapHeader))
		}
	}
}
