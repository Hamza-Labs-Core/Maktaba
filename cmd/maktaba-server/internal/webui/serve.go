package webui

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/pagetheme"
)

// assetsPrefix is where Vite writes the bundle's files, each named by its content hash.
const assetsPrefix = "/assets/"

// tvPrefix is the TV build of the big screen (story 58.08, `pnpm build:tv`): its
// own page (tv/index.html) and its own hashed files (tv/assets/), served the same
// way as the web app's. A path under /tv/ that is not a file falls back to the TV
// page, never the web app's.
const tvPrefix = "/tv/"

// isAsset reports whether path names a hashed bundle file (web or TV build).
func isAsset(path string) bool {
	return strings.HasPrefix(path, assetsPrefix) || strings.HasPrefix(path, tvPrefix+"assets/")
}

// serveSPA serves the single-page app in sub with history-API fallback: unknown
// non-asset paths return index.html so client-side routes (e.g. /library/42) resolve.
// apiPrefixes are the path prefixes that must NOT fall back to index.html (they belong
// to a reverse-proxied backend); a request under one of them that misses returns 404
// rather than the SPA shell (FallsBack).
//
// Caching: index.html (and every fallback to it) is revalidated on each load, and the
// hashed files under /assets/ are kept for a year. Without that a cached index.html
// outlived an update: it named the old bundle's script, the fallback answered that
// missing script with index.html, WebKit refused HTML as a module script, and the Mac
// app's window stayed blank. A missing /assets/ (or /tv/assets/) file is a 404,
// never the page. /tv redirects to /tv/.
func serveSPA(sub fs.FS, apiPrefixes ...string) http.Handler {
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tv" {
			http.Redirect(w, r, tvPrefix, http.StatusMovedPermanently)
			return
		}
		tv := strings.HasPrefix(r.URL.Path, tvPrefix)
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" || clean == "tv/" {
			clean += "index.html"
		}
		if info, err := fs.Stat(sub, clean); err != nil || info.IsDir() {
			if !FallsBack(r.URL.Path, apiPrefixes...) {
				pagetheme.NotFound(w, r)
				return
			}
			// SPA fallback: the TV page under /tv/, the web app's elsewhere.
			w.Header().Set("Cache-Control", "no-cache")
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			if tv {
				if _, err := fs.Stat(sub, "tv/index.html"); err != nil {
					// a build without the TV bundle (pnpm build alone)
					pagetheme.NotFound(w, r)
					return
				}
				r2.URL.Path = tvPrefix
			}
			fileServer.ServeHTTP(w, r2)
			return
		}
		if isAsset(r.URL.Path) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
