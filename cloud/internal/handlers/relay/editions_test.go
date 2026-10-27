package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// a player build (manage/ has none here: the relay then leaves /manage/ to the home)
var builds = fstest.MapFS{
	"play/index.html":           {Data: []byte(`<!doctype html><script type="module" src="/play/assets/index-1.js"></script>`)},
	"play/assets/index-1.js":    {Data: []byte("console.log('player')")},
	"play/manifest.webmanifest": {Data: []byte("{}")},
	"secret.txt":                {Data: []byte("not an edition's")},
}

func editionGet(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	r.Host = e2eSlug + ".relay.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// The relay with a home that is offline (no tunnel): an edition still loads, from the
// cloud; anything else on the home's address goes to the home (here: offline).
func editionRelay() http.Handler {
	return (&Deps{
		Registry:   relaypkg.NewRegistry(),
		Lookup:     fakeLookup{},
		PublicHost: "relay.example",
		Editions:   &Editions{Root: builds},
	}).Handler()
}

func TestEditionsAreServedOnAHomesAddress(t *testing.T) {
	h := editionRelay()
	for _, path := range []string{"/play/", "/play/library/42", "/play/videos/v1/watch"} {
		rec := editionGet(t, h, http.MethodGet, path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/play/assets/index-1.js") {
			t.Fatalf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: the page is revalidated, got %q", path, rec.Header().Get("Cache-Control"))
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
			rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: the home SPA's headers: %v", path, rec.Header())
		}
	}
	rec := editionGet(t, h, http.MethodGet, "/play/assets/index-1.js")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("an asset: %d %v", rec.Code, rec.Header())
	}
	if rec := editionGet(t, h, http.MethodGet, "/play"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/play/" {
		t.Errorf("/play: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestEditionsNeverAnswerAMissingFileWithThePage(t *testing.T) {
	h := editionRelay()
	for _, path := range []string{"/play/assets/index-OLD.js", "/play/missing.css"} {
		if rec := editionGet(t, h, http.MethodGet, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}

func TestEditionsStayInTheirDirectory(t *testing.T) {
	h := editionRelay()
	for _, path := range []string{"/play/../secret.txt", "/play/%2e%2e/secret.txt", "/play/..%2fsecret.txt"} {
		rec := editionGet(t, h, http.MethodGet, path)
		if strings.Contains(rec.Body.String(), "not an edition's") {
			t.Errorf("%s left the edition: %d", path, rec.Code)
		}
	}
}

func TestEditionsAreReadOnly(t *testing.T) {
	if rec := editionGet(t, editionRelay(), http.MethodPost, "/play/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestOtherPathsStillGoToTheHome(t *testing.T) {
	h := editionRelay()
	// no manage/ build here, and the home's own paths: through the tunnel (offline: 502)
	for _, path := range []string{"/manage/", "/api/auth/me", "/", "/playlists"} {
		if rec := editionGet(t, h, http.MethodGet, path); rec.Code != http.StatusBadGateway {
			t.Errorf("%s: %d (want the home's answer: offline)", path, rec.Code)
		}
	}
	// an unknown subdomain gets no edition
	r := httptest.NewRequest(http.MethodGet, "/play/", nil)
	r.Host = "nobody.relay.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "index-1.js") {
		t.Errorf("unknown home: %d", rec.Code)
	}
}
