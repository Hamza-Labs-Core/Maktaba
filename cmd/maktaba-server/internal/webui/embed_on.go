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
)

//go:embed all:dist
var distFS embed.FS

// Embedded reports whether the SPA was compiled in.
const Embedded = true

// Handler returns an http.Handler that serves the embedded SPA (serveSPA).
func Handler(apiPrefixes ...string) http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Can only happen if the embed dir vanished at build time; fail
		// loud rather than serve a broken UI.
		panic("webui: dist subtree missing: " + err.Error())
	}
	return serveSPA(sub, apiPrefixes...)
}
