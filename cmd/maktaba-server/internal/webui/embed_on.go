//go:build embed_web

// Package webui serves the single-page web app. This file is the
// embedding variant, compiled only under `-tags embed_web`: the built
// SPA from web/dist is copied into ./dist by `make server` and baked
// into the binary here. Dev builds omit the tag (see embed_off.go) so
// they skip the embed and compile faster.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Embedded reports whether the SPA was compiled in.
const Embedded = true

// Handler returns an http.Handler that serves the embedded SPA with
// history-API fallback: unknown non-asset paths return index.html so
// client-side routes (e.g. /library/42) resolve. apiPrefixes are the
// path prefixes that must NOT fall back to index.html (they belong to a
// reverse-proxied backend); a request under one of them that misses
// returns 404 rather than the SPA shell.
func Handler(apiPrefixes ...string) http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Can only happen if the embed dir vanished at build time; fail
		// loud rather than serve a broken UI.
		panic("webui: dist subtree missing: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(sub, clean); err != nil {
			for _, p := range apiPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					http.NotFound(w, r)
					return
				}
			}
			// SPA fallback.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
