package homemirror

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mirror's requests go over the machine's cluster link first, unsigned (story 35.16):
// with no delegation key at all (the link required); with the link down, the signed path.
func TestMirrorOverTheLinkFirst(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("t0k"), 0o600)
	linkUp := true
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Maktaba-Local-Token") != "t0k" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if !linkUp {
			w.Header().Set("X-Maktaba-Link", "down")
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "link "+r.URL.Query().Get("purpose")+" "+r.URL.Query().Get("path"))
	}))
	defer api.Close()
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headerSig) == "" {
			http.Error(w, "unsigned", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "signed "+r.URL.Path)
	}))
	defer home.Close()
	c := &Client{Home: home.URL, NodeID: "n1", Link: &LocalLink{API: api.URL, TokenFile: tok}}
	read := func(path string, key Key) string {
		resp, err := c.get(context.Background(), path, key, nil)
		if err != nil {
			return "error: " + err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if _, ok := c.usable(); !ok {
		t.Fatal("a machine with the link but no delegation key cannot use the mirror")
	}
	if got := read("/api/cluster/updates/wheelhouse/1.5.0/linux", Key{}); got != "link wheelhouse /api/cluster/updates/wheelhouse/1.5.0/linux" {
		t.Fatalf("over the link: %q", got)
	}
	linkUp = false
	if got := read("/api/cluster/updates/artifacts/1.5.0/x.tar.gz", Key{}); !strings.HasPrefix(got, "error:") {
		t.Fatalf("link down and no key: %q", got)
	}
	if got := read("/api/cluster/updates/artifacts/1.5.0/x.tar.gz", Key{ID: "dk", Secret: make([]byte, 32)}); got != "signed /api/cluster/updates/artifacts/1.5.0/x.tar.gz" {
		t.Fatalf("link down, a key: %q", got)
	}
}
