package webui

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/pagetheme"
)

// assetsPrefix is where Vite writes the bundle's files, each named by its content hash.
const assetsPrefix = "/assets/"

// serveSPA serves the single-page app in sub with history-API fallback: unknown
// non-asset paths return index.html so client-side routes (e.g. /library/42) resolve.
// apiPrefixes are the path prefixes that must NOT fall back to index.html (they belong
// to a reverse-proxied backend); a request under one of them that misses returns 404
// rather than the SPA shell.
//
// Caching: index.html (and every fallback to it) is revalidated on each load, and the
// hashed files under /assets/ are kept for a year. Without that a cached index.html
// outlived an update: it named the old bundle's script, the fallback answered that
// missing script with index.html, WebKit refused HTML as a module script, and the Mac
// app's window stayed blank. A missing /assets/ file is a 404, never the page.
func serveSPA(sub fs.FS, apiPrefixes ...string) http.Handler {
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(sub, clean); err != nil {
			if strings.HasPrefix(r.URL.Path, assetsPrefix) {
				pagetheme.NotFound(w, r)
				return
			}
			for _, p := range apiPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					pagetheme.NotFound(w, r)
					return
				}
			}
			// SPA fallback.
			w.Header().Set("Cache-Control", "no-cache")
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		if strings.HasPrefix(r.URL.Path, assetsPrefix) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
