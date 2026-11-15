package channel

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A channel playlist goes out with its length, never chunked, so one cut short is an
// error for the player and never a whole playlist (httpx.SetLength). The playlist is
// larger than net/http's 2 KB buffer, past which a body with no length is chunked.
func TestChannelPlaylistIsSentWithItsLength(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "#EXTINF:4.000,\nseg-%d.ts\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveShared(w, r, dir, "master.m3u8")
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || string(got) != b.String() {
		t.Fatalf("read %d bytes, %v", len(got), err)
	}
	if resp.ContentLength != int64(b.Len()) || len(resp.TransferEncoding) != 0 {
		t.Fatalf("Content-Length %d, Transfer-Encoding %v: want %d and none", resp.ContentLength, resp.TransferEncoding, b.Len())
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("Content-Type %q", ct)
	}
}
