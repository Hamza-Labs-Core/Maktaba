package simhome

import (
	"io"
	"net/http"
	"testing"
)

// A /stream/ path is a player's segment of the known pattern, or a playlist, and says the cap
// the relay asked the home to fit (the relay-plans rehearsal, story 50.19 T13).
func TestTheStreamPathsSayTheCap(t *testing.T) {
	api := API()
	defer api.Close()
	get := func(path, capKbps string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, api.URL+path, nil)
		if capKbps != "" {
			req.Header.Set(capHeader, capKbps)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}
	resp, b := get("/stream/s1/720p/seg-1.ts?bytes=1000", "3000")
	if resp.Header.Get("X-Sim-Cap") != "3000" || len(b) != 1000 {
		t.Fatalf("segment: cap %q, %d bytes", resp.Header.Get("X-Sim-Cap"), len(b))
	}
	for i, c := range b {
		if c != Pattern(i) {
			t.Fatalf("byte %d is %q", i, c)
		}
	}
	resp, b = get("/stream/s1/720p/index.m3u8", "")
	if resp.Header.Get("X-Sim-Cap") != "" || string(b) != "#EXTM3U\n" {
		t.Fatalf("playlist: cap %q, %q", resp.Header.Get("X-Sim-Cap"), b)
	}
	if _, b = get("/stream?bytes=10", "1"); len(b) != 10 {
		t.Fatalf("/stream: %d bytes", len(b))
	}
}
