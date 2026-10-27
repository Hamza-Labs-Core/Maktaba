package relay

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Editions serves the web app's player and remote editions for a connected home (Epic 42
// story 42.9): https://<slug>.<relay>/play/ and /manage/, built with their base path
// (web: `pnpm build:player:cloud`, `build:remote:cloud`). The SPA's own files come from the
// cloud; everything else on that host (its /api, /stream/, the home's own web UI at the root)
// goes on to the home through the tunnel as before, so the editions talk to the home at the
// same origin, with the home's own cookie session. They grant nothing: the home checks the
// signed-in user on every route.
//
// Root holds one directory per edition, named by its path: play/ and manage/, each with its
// index.html and assets/. Caching as the home's own web UI (cmd/maktaba-server webui):
// index.html (and every fallback to it) is revalidated, the hashed assets/ kept a year, and
// a missing asset or file is a 404, never the page.
type Editions struct {
	Root fs.FS
}

// editionNames are the editions the cloud serves, by their path on a home's relay address.
var editionNames = []string{"play", "manage"}

// editionHeaders are the home SPA's (api/internal/httpsec DefaultHeaders), so an edition is
// no weaker for being served by the cloud.
var editionHeaders = map[string]string{
	"Content-Security-Policy":    "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'",
	"Referrer-Policy":            "strict-origin-when-cross-origin",
	"Cross-Origin-Opener-Policy": "same-origin",
	"X-Content-Type-Options":     "nosniff",
	"X-Frame-Options":            "DENY",
	"Strict-Transport-Security":  "max-age=31536000",
}

// edition is the edition a path is under ("play" for /play or /play/…), or "".
func edition(urlPath string) string {
	for _, n := range editionNames {
		if urlPath == "/"+n || strings.HasPrefix(urlPath, "/"+n+"/") {
			return n
		}
	}
	return ""
}

// Serves reports whether r is for an edition this cloud has (its directory is there).
func (e *Editions) Serves(r *http.Request) bool {
	if e == nil || e.Root == nil {
		return false
	}
	n := edition(r.URL.Path)
	if n == "" {
		return false
	}
	st, err := fs.Stat(e.Root, n+"/index.html")
	return err == nil && !st.IsDir()
}

// ServeHTTP answers a request Serves accepted.
func (e *Editions) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := edition(r.URL.Path)
	for k, v := range editionHeaders {
		w.Header().Set(k, v)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/"+n {
		http.Redirect(w, r, "/"+n+"/", http.StatusMovedPermanently)
		return
	}
	sub, err := fs.Sub(e.Root, n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// the path inside the edition, cleaned: no way out of its directory
	inside := strings.TrimPrefix(path.Clean(strings.TrimPrefix(r.URL.Path, "/"+n)), "/")
	file := inside
	if file == "" || file == "." {
		file = "index.html"
	}
	st, err := fs.Stat(sub, file)
	switch {
	case err == nil && !st.IsDir():
		if strings.HasPrefix(file, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
	case strings.HasPrefix(file, "assets/") || path.Ext(file) != "":
		// a file that is not there (an old bundle's script) is never answered with the page
		http.NotFound(w, r)
		return
	default:
		// a client-side route (/play/library/42): the app's page
		file = "index.html"
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, sub, file)
}
