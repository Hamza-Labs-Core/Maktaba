//go:build !embed_web

// Package webui (default/dev variant): no embed, so the compiler doesn't
// have to read every file under dist/ on each build. The handler returns
// a friendly placeholder so `serve` still stands up a working web role;
// production binaries are built with `-tags embed_web` (via `make
// server`) to bake in the real SPA. See embed_on.go for the embedding
// variant.
package webui

import (
	"net/http"
)

// Embedded reports whether the SPA was compiled in.
const Embedded = false

// Handler returns a placeholder handler explaining the UI was not
// embedded in this (dev) build. The variadic argument is accepted for
// signature parity with the embedding variant but unused here.
func Handler(_ ...string) http.Handler {
	const body = `<!doctype html>
<html><head><meta charset="utf-8"><title>Maktaba</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;padding:0 1rem">
<h1>Maktaba</h1>
<p>This is a development build without the embedded web UI.</p>
<p>Build the production binary with <code>make server</code> (which compiles
the SPA and bakes it in with <code>-tags embed_web</code>), or run the web
dev server separately during development.</p>
</body></html>`
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
}
