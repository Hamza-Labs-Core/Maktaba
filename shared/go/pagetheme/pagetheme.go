// Package pagetheme is the one look of every HTML page a Maktaba Go
// service renders itself (error pages, the web port's "API is restarting"
// and "live TV is not reachable" pages, the dev-build placeholder, report
// pages): outside the React app there is no theme script, so these pages
// follow the browser's prefers-color-scheme with CSS alone, and fall back
// to DARK when the system states no preference, the same rule the web
// app's theme controller applies (web/src/lib/theme.ts).
//
// No script is ever used: the pages ship under a CSP that allows none.
// The stylesheet is inline and pinned by its sha256 in CSP, so it works
// where the API (or anything else) is down and no stylesheet could be
// fetched.
//
// Colours are the app's tokens (web/src/styles/tokens.css).
package pagetheme

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"strings"
)

// BaseCSS is the minimum every page needs: the UA's color-scheme (form
// controls, scrollbars, the canvas behind the page) and the background
// and text colour, dark unless the OS asks for light. Inject puts it in
// pages that bring their own stylesheet.
const BaseCSS = `:root{color-scheme:dark light;--mk-bg:#09090b;--mk-fg:#fafafa;--mk-muted:#a1a1aa;` +
	`--mk-subtle:#1c1c20;--mk-border:#27272a;--mk-accent:#6f93ff;--mk-error:#f0707f}` +
	`@media (prefers-color-scheme: light){:root{--mk-bg:#fafafa;--mk-fg:#18181b;--mk-muted:#71717a;` +
	`--mk-subtle:#f4f4f5;--mk-border:#e4e4e7;--mk-accent:#1e5ad8;--mk-error:#b6253a}}` +
	`html{background:var(--mk-bg);color:var(--mk-fg)}`

// CSS is the full stylesheet of the pages Page renders: BaseCSS plus a
// small readable layout.
const CSS = BaseCSS +
	`*{box-sizing:border-box}` +
	`body{margin:0;min-height:100vh;background:var(--mk-bg);color:var(--mk-fg);` +
	`font:16px/1.5 Inter,system-ui,-apple-system,"Segoe UI",sans-serif;display:flex;align-items:center;justify-content:center}` +
	`main{max-width:40rem;width:100%;margin:2rem 16px;padding:24px;border:1px solid var(--mk-border);` +
	`border-radius:12px;background:var(--mk-subtle)}` +
	`h1{font-size:22px;margin:0 0 8px}p{margin:8px 0}.mk-status{color:var(--mk-muted);font-size:14px;margin:0 0 4px}` +
	`.mk-detail{color:var(--mk-muted);font-size:14px;overflow-wrap:anywhere}` +
	`a{color:var(--mk-accent)}code,pre{font:13px ui-monospace,SFMono-Regular,Consolas,monospace;background:var(--mk-bg);` +
	`border:1px solid var(--mk-border);border-radius:4px;padding:1px 4px}` +
	`nav{margin-top:16px;display:flex;gap:16px;flex-wrap:wrap}`

// MetaColorScheme tells the UA, before any CSS, that the page is dark
// unless the OS asks for light (the first listed scheme wins when there
// is no preference).
const MetaColorScheme = `<meta name="color-scheme" content="dark light">`

// StyleHash is the CSP source of the inline <style> that StyleTag emits.
var StyleHash = cspHash(CSS)

// CSP is the policy Page responses carry: no script, nothing fetched,
// only the one inline stylesheet (by hash), no framing.
var CSP = "default-src 'none'; style-src '" + StyleHash + "'; img-src 'self' data:; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func cspHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// StyleTag is the inline stylesheet of a Page, ready for a <head>.
func StyleTag() string { return "<style>" + CSS + "</style>" }

// Head is the shared <head> content: charset, viewport, color-scheme,
// title and the stylesheet. extra is raw markup appended (e.g. a meta
// refresh); callers must not pass untrusted input in it.
func Head(title, extra string) string {
	return `<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">` +
		MetaColorScheme + "<title>" + html.EscapeString(title) + "</title>" + StyleTag() + extra
}

// Link is a link shown under a page's message.
type Link struct {
	Href, Text string
}

// Page is a stand-alone themed page (an error, a "please wait").
type Page struct {
	// Status is the HTTP status (0 → 200). It is shown above the heading
	// for errors (>= 400).
	Status int
	// Title is the tab title; "" → "Maktaba".
	Title string
	// Heading is the <h1>; "" → the status text.
	Heading string
	// Message is the main sentence (plain text, escaped).
	Message string
	// Detail is a secondary line (plain text, escaped), e.g. the error.
	Detail string
	// RetrySeconds > 0 reloads the page after that many seconds (meta
	// refresh, no script) and sends Retry-After.
	RetrySeconds int
	// Links are shown under the message ("Back to Maktaba" …).
	Links []Link
}

func (p Page) status() int {
	if p.Status == 0 {
		return http.StatusOK
	}
	return p.Status
}

// HTML renders the page.
func (p Page) HTML() []byte {
	code := p.status()
	title := p.Title
	if title == "" {
		title = "Maktaba"
	}
	heading := p.Heading
	if heading == "" {
		heading = http.StatusText(code)
	}
	extra := ""
	if p.RetrySeconds > 0 {
		extra = fmt.Sprintf(`<meta http-equiv="refresh" content="%d">`, p.RetrySeconds)
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head>`)
	b.WriteString(Head(title, extra))
	b.WriteString(`</head><body><main>`)
	if code >= 400 {
		fmt.Fprintf(&b, `<p class="mk-status">%d · %s</p>`, code, html.EscapeString(http.StatusText(code)))
	}
	b.WriteString("<h1>" + html.EscapeString(heading) + "</h1>")
	if p.Message != "" {
		b.WriteString("<p>" + html.EscapeString(p.Message) + "</p>")
	}
	if p.Detail != "" {
		b.WriteString(`<p class="mk-detail">` + html.EscapeString(p.Detail) + "</p>")
	}
	if p.RetrySeconds > 0 {
		fmt.Fprintf(&b, `<p class="mk-detail">This page retries every %d seconds.</p>`, p.RetrySeconds)
	}
	if len(p.Links) > 0 {
		b.WriteString("<nav>")
		for _, l := range p.Links {
			b.WriteString(`<a href="` + html.EscapeString(l.Href) + `">` + html.EscapeString(l.Text) + "</a>")
		}
		b.WriteString("</nav>")
	}
	b.WriteString("</main></body></html>\n")
	return []byte(b.String())
}

// Write sends the page with its CSP and no caching.
func Write(w http.ResponseWriter, p Page) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", CSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Del("Content-Length")
	if p.RetrySeconds > 0 {
		h.Set("Retry-After", fmt.Sprint(p.RetrySeconds))
	}
	w.WriteHeader(p.status())
	_, _ = w.Write(p.HTML())
}

// WantsHTML reports whether the request is a browser loading a page (a
// navigation or an iframe), not a fetch/XHR or a media player: only then
// is an HTML error better than plain text or JSON.
func WantsHTML(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "document", "iframe", "frame":
		return true
	case "":
		// older browsers / no fetch metadata: fall back to Accept
	default:
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// Error is http.Error that renders a themed page for a browser
// navigation (WantsHTML) and plain text otherwise.
func Error(w http.ResponseWriter, r *http.Request, msg string, code int) {
	if WantsHTML(r) {
		Write(w, Page{Status: code, Message: msg, Links: []Link{{Href: "/", Text: "Back to Maktaba"}}})
		return
	}
	http.Error(w, msg, code)
}

// NotFound is http.NotFound with a themed page for browser navigations.
func NotFound(w http.ResponseWriter, r *http.Request) {
	if WantsHTML(r) {
		Write(w, Page{
			Status:  http.StatusNotFound,
			Heading: "Page not found",
			Message: "There is nothing at this address.",
			Links:   []Link{{Href: "/", Text: "Back to Maktaba"}},
		})
		return
	}
	http.NotFound(w, r)
}

// Inject adds the color-scheme meta and BaseCSS to an HTML document that
// brings its own styling (a report page), right after <head> — or at the
// top when there is no <head>. The page's own CSS still wins; this only
// guarantees a dark-or-system canvas. A document that already declares a
// color-scheme is returned unchanged. The injected <style> needs
// style-src 'unsafe-inline' (or InjectHash) in the page's CSP.
func Inject(doc []byte) []byte {
	lower := strings.ToLower(string(doc))
	if strings.Contains(lower, "color-scheme") {
		return doc
	}
	tag := MetaColorScheme + "<style>" + BaseCSS + "</style>"
	at := 0
	if i := strings.Index(lower, "<head"); i >= 0 {
		if j := strings.IndexByte(lower[i:], '>'); j >= 0 {
			at = i + j + 1
		}
	} else if i := strings.Index(lower, "<!doctype"); i >= 0 {
		if j := strings.IndexByte(lower[i:], '>'); j >= 0 {
			at = i + j + 1
		}
	}
	out := make([]byte, 0, len(doc)+len(tag))
	out = append(out, doc[:at]...)
	out = append(out, tag...)
	return append(out, doc[at:]...)
}

// InjectHash is the CSP source of the <style> Inject adds.
var InjectHash = cspHash(BaseCSS)
