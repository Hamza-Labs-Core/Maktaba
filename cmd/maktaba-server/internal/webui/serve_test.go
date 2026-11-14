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

// Story 58.08 T05: the home serves the TV build at /tv/. Its client routes fall
// back to the TV page (never the web app's), its page is revalidated, its hashed
// files are kept for a year and a missing one is a 404; /tv goes to /tv/.
func TestTVBuildServed(t *testing.T) {
	h := serveSPA(fstest.MapFS{
		"index.html":              {Data: []byte(`web page`)},
		"tv/index.html":           {Data: []byte(`<script type="module" src="/tv/assets/index.tv-1.js"></script>`)},
		"tv/assets/index.tv-1.js": {Data: []byte("console.log(1)")},
	}, "/api/")
	for _, path := range []string{"/tv/", "/tv/big", "/tv/big/library/42?tab=library"} {
		rec := get(t, h, path)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "index.tv-1.js") {
			t.Fatalf("%s: %d %q, want the TV page", path, rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control %q, want no-cache", path, cc)
		}
	}
	rec := get(t, h, "/tv/assets/index.tv-1.js")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("TV asset: %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	rec = get(t, h, "/tv/assets/index.tv-0.js")
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<script") {
		t.Errorf("a missing TV asset answered %d %q, want a 404", rec.Code, rec.Body.String())
	}
	rec = get(t, h, "/tv")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/tv/" {
		t.Errorf("/tv: %d to %q, want a redirect to /tv/", rec.Code, rec.Header().Get("Location"))
	}
	if rec := get(t, h, "/library/42"); rec.Body.String() != "web page" {
		t.Errorf("a web route answered %q, want the web app's page", rec.Body.String())
	}
	if FallsBack("/tv/assets/x.js") {
		t.Error("FallsBack(/tv/assets/x.js) = true, want a 404")
	}
}

// A build without the TV bundle answers /tv/ with a 404, not the web app's page.
func TestTVPathWithoutTVBuild(t *testing.T) {
	if rec := get(t, spa(), "/tv/big"); rec.Code != http.StatusNotFound {
		t.Errorf("/tv/big without a TV build: %d, want 404", rec.Code)
	}
}
