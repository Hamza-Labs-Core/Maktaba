package webui

import (
	"html"
	"net/http"
	"strings"
)

// PageCSS themes every page the server renders itself (the placeholder, the error
// and "cannot reach" pages of the web port, Epic 35): the system theme, dark when the
// browser does not prefer light — never a bright white page.
const PageCSS = `:root{color-scheme:dark light;--bg:#111317;--fg:#e7e9ee;--muted:#a3a9b5;--card:#1a1d23;--border:#2b2f38;--accent:#d9a441}
@media (prefers-color-scheme: light){:root{--bg:#f7f7f8;--fg:#17181c;--muted:#5d6370;--card:#fff;--border:#e2e4e9;--accent:#a8650b}}
html,body{margin:0;background:var(--bg);color:var(--fg);font-family:system-ui,-apple-system,"Segoe UI",sans-serif}
main{min-height:100vh;display:flex;align-items:center;justify-content:center;padding:1.5rem;box-sizing:border-box}
.card{max-inline-size:36rem;background:var(--card);border:1px solid var(--border);border-inline-start:4px solid var(--accent);border-radius:12px;padding:1.5rem}
h1{margin:0 0 .75rem;font-size:1.4rem}p{margin:.5rem 0;line-height:1.5}.muted{color:var(--muted);font-size:.9rem}a{color:var(--accent)}code{font-size:.9em}`

// Page renders a themed HTML page; body is HTML the caller built (escape user text with
// html.EscapeString).
func Page(title, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<meta name="color-scheme" content="dark light"><title>` + html.EscapeString(title) + `</title><style>` + PageCSS +
		`</style></head><body><main><div class="card"><h1>` + html.EscapeString(title) + `</h1>` + body +
		`</div></main></body></html>`
}

// WantsHTML reports whether the request is a browser page load.
func WantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// Error answers status with a themed page for a browser page load, plain text otherwise.
func Error(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	if WantsHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(Page(title, `<p>`+html.EscapeString(detail)+`</p><p class="muted">This page reloads when you try again.</p>`)))
		return
	}
	http.Error(w, title+": "+detail, status)
}
