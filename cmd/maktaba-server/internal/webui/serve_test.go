package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func spa() http.Handler {
	return serveSPA(fstest.MapFS{
		"index.html":           {Data: []byte(`<script type="module" src="/assets/index-new.js"></script>`)},
		"assets/index-new.js":  {Data: []byte("console.log(1)")},
		"art/library/dark.jpg": {Data: []byte("jpg")},
	}, "/api/")
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

// After an update a cached index.html named the old bundle's script; the fallback
// answered it with index.html and WebKit, refusing HTML as a module script, left the
// Mac app's window blank. The page is revalidated each load, the hashed assets are
// kept, and a missing asset is a 404.
func TestSPACachingAndMissingAssets(t *testing.T) {
	h := spa()
	for _, path := range []string{"/", "/library/42"} {
		rec := get(t, h, path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "index-new.js") {
			t.Fatalf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", path, cc)
		}
	}
	rec := get(t, h, "/assets/index-new.js")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	rec = get(t, h, "/assets/index-old.js")
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<script") {
		t.Errorf("a missing asset answered %d %q, want a 404 (never the page)", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/art/library/dark.jpg"); rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("art: Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get(t, h, "/api/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("api miss: %d", rec.Code)
	}
}
