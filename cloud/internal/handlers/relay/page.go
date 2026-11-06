package relay

// The relay's own pages: what a browser sees at <slug>.relay.maktaba.tv
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
	"strconv"
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

// pageWords is a page's heading and sentence in one language.
type pageWords struct{ heading, message string }

// pageTexts are the relay error pages' words, in English and Arabic (story
// 50.04 gave them Arabic; «الجهاز الرئيسي» is the home in every app).
var pageTexts = map[string]map[string]pageWords{
	"en": {
		"offline":           {"Your Maktaba server is unreachable", "The relay cannot reach the home server right now. It may be off, restarting or offline; this page retries by itself."},
		"home_reconnecting": {"Your home is reconnecting", "Your home lost its connection to Maktaba Cloud for a moment and is coming back. This page tries again in a few seconds."},
		"tier_limit":        {"Daily relay limit reached", "This server used its plan's relay allowance for today."},
		"none":              {"No Maktaba server here", "There is no Maktaba server at this address."},
		"retry":             {"Try again", ""},
	},
	"ar": {
		"offline":           {"تعذّر الوصول إلى الجهاز الرئيسي", "لا يستطيع المُرحِّل الوصول إلى الجهاز الرئيسي الآن. ربما هو مطفأ أو يُعاد تشغيله أو غير متصل؛ وستعيد هذه الصفحة المحاولة وحدها."},
		"home_reconnecting": {"الجهاز الرئيسي يعيد الاتصال", "انقطع اتصال الجهاز الرئيسي بسحابة مكتبة لحظة وهو يعود الآن. ستعيد هذه الصفحة المحاولة بعد ثوانٍ."},
		"tier_limit":        {"بلغ المُرحِّل حدّه اليومي", "استهلك هذا الجهاز الرئيسي حصة المُرحِّل في خطته لهذا اليوم."},
		"none":              {"لا يوجد جهاز رئيسي لمكتبة هنا", "لا يوجد جهاز رئيسي لمكتبة على هذا العنوان."},
		"retry":             {"إعادة المحاولة", ""},
	},
}

// pageText is the heading and sentence of each relay error kind in lang, and
// how many seconds the page waits before it retries (0: it does not).
func pageText(kind, lang string) (heading, message string, retry int) {
	words := pageTexts[lang]
	if words == nil {
		words = pageTexts["en"]
	}
	pick := func(k string) (string, string) { return words[k].heading, words[k].message }
	switch kind {
	case "server_offline", "tunnel_error", "tunnel_timeout":
		h, m := pick("offline")
		return h, m, 15
	case "home_reconnecting":
		h, m := pick("home_reconnecting")
		return h, m, 5
	case "tier_limit":
		h, m := pick("tier_limit")
		return h, m, 0
	default:
		h, m := pick("none")
		return h, m, 0
	}
}

// pageLang is the page language for the request's Accept-Language: "ar"
// when Arabic is preferred over English, else "en".
func pageLang(r *http.Request) string {
	best, bestQ := "en", -1.0
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag = strings.ToLower(strings.TrimSpace(tag))
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		lang, _, _ := strings.Cut(tag, "-")
		if (lang == "ar" || lang == "en") && q > bestQ {
			best, bestQ = lang, q
		}
	}
	return best
}

func writePage(w http.ResponseWriter, r *http.Request, code int, kind, msg string) {
	lang := pageLang(r)
	heading, message, retry := pageText(kind, lang)
	dir := "ltr"
	if lang == "ar" {
		dir = "rtl"
	}
	refresh := ""
	if retry > 0 {
		refresh = fmt.Sprintf(`<meta http-equiv="refresh" content="%d">`, retry)
		w.Header().Set("Retry-After", fmt.Sprint(retry))
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="` + lang + `" dir="` + dir + `"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<meta name="color-scheme" content="dark light">` + refresh +
		`<title>` + html.EscapeString(heading) + ` · Maktaba</title><style>` + pageCSS + `</style></head><body><main>`)
	fmt.Fprintf(&b, `<p class="mk-status">%d · %s</p>`, code, html.EscapeString(http.StatusText(code)))
	b.WriteString("<h1>" + html.EscapeString(heading) + "</h1><p>" + html.EscapeString(message) + "</p>")
	if retry > 0 {
		// a plain link: the page has no script (CSP), the refresh retries too
		b.WriteString(`<p><a href="">` + html.EscapeString(pageTexts[lang]["retry"].heading) + "</a></p>")
	}
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
