package relay

// The relay's own pages: what a browser sees at <slug>.relay.maktaba.app
// when the request never reaches the home server (offline, unknown, over
// its plan). They follow the browser's prefers-color-scheme, dark when it
// states no preference, with CSS only (no script) — the rule every
// Maktaba page follows (shared/go/pagetheme; cloud is a self-contained
// module, so the few lines live here, same tokens).

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"strings"
)

const pageCSS = `:root{color-scheme:dark light;--mk-bg:#09090b;--mk-fg:#fafafa;--mk-muted:#a1a1aa;` +
	`--mk-subtle:#1c1c20;--mk-border:#27272a;--mk-accent:#6f93ff}` +
	`@media (prefers-color-scheme: light){:root{--mk-bg:#fafafa;--mk-fg:#18181b;--mk-muted:#71717a;` +
	`--mk-subtle:#f4f4f5;--mk-border:#e4e4e7;--mk-accent:#1e5ad8}}` +
	`html{background:var(--mk-bg);color:var(--mk-fg)}*{box-sizing:border-box}` +
	`body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;` +
	`background:var(--mk-bg);color:var(--mk-fg);font:16px/1.5 Inter,system-ui,-apple-system,"Segoe UI",sans-serif}` +
	`main{max-width:40rem;width:100%;margin:2rem 16px;padding:24px;border:1px solid var(--mk-border);` +
	`border-radius:12px;background:var(--mk-subtle)}h1{font-size:22px;margin:0 0 8px}` +
	`.mk-status,.mk-detail{color:var(--mk-muted);font-size:14px}a{color:var(--mk-accent)}`

var pageCSP = func() string {
	sum := sha256.Sum256([]byte(pageCSS))
	return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) +
		"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}()

// wantsHTML: a browser loading a page (not a fetch or a media player).
func wantsHTML(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "document", "iframe", "frame":
		return true
	case "":
		return strings.Contains(r.Header.Get("Accept"), "text/html")
	}
	return false
}

// pageText is the heading and sentence of each relay error kind.
func pageText(kind string) (heading, message string, retry int) {
	switch kind {
	case "server_offline", "tunnel_error":
		return "Your Maktaba server is unreachable",
			"The relay cannot reach the home server right now. It may be off, restarting or offline; this page retries by itself.", 15
	case "tier_limit":
		return "Daily relay limit reached", "This server used its plan's relay allowance for today.", 0
	default:
		return "No Maktaba server here", "There is no Maktaba server at this address.", 0
	}
}

func writePage(w http.ResponseWriter, code int, kind, msg string) {
	heading, message, retry := pageText(kind)
	refresh := ""
	if retry > 0 {
		refresh = fmt.Sprintf(`<meta http-equiv="refresh" content="%d">`, retry)
		w.Header().Set("Retry-After", fmt.Sprint(retry))
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<meta name="color-scheme" content="dark light">` + refresh +
		`<title>` + html.EscapeString(heading) + ` · Maktaba</title><style>` + pageCSS + `</style></head><body><main>`)
	fmt.Fprintf(&b, `<p class="mk-status">%d · %s</p>`, code, html.EscapeString(http.StatusText(code)))
	b.WriteString("<h1>" + html.EscapeString(heading) + "</h1><p>" + html.EscapeString(message) + "</p>")
	if msg != "" {
		b.WriteString(`<p class="mk-detail">` + html.EscapeString(msg) + "</p>")
	}
	b.WriteString("</main></body></html>\n")
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", pageCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(b.String()))
}
