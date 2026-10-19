package pagetheme

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// assertThemed fails t unless body is a page that follows the system
// theme with a dark fallback and carries no script.
func assertThemed(t *testing.T, body string) {
	t.Helper()
	for _, want := range []string{
		`<meta name="color-scheme" content="dark light">`,
		"color-scheme:dark light",
		"@media (prefers-color-scheme: light)",
		"--mk-bg:#09090b", // dark is the default (no preference)
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(body), "<script") {
		t.Error("page carries a script")
	}
}

func TestPageIsThemedAndEscaped(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, Page{Status: 502, Heading: "Down", Message: "<b>x</b>", Detail: "dial: refused", RetrySeconds: 5})
	if rec.Code != 502 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	assertThemed(t, body)
	if strings.Contains(body, "<b>x</b>") || !strings.Contains(body, "&lt;b&gt;x&lt;/b&gt;") {
		t.Error("message not escaped")
	}
	if !strings.Contains(body, `http-equiv="refresh" content="5"`) || rec.Header().Get("Retry-After") != "5" {
		t.Error("retry missing")
	}
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
		t.Errorf("content type %q", h.Get("Content-Type"))
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, StyleHash) || strings.Contains(csp, "script-src") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("csp %q", csp)
	}
	// the hash in the CSP is the hash of exactly the inline stylesheet
	if !strings.Contains(body, "<style>"+CSS+"</style>") {
		t.Error("inline style differs from CSS")
	}
}

func TestErrorNegotiates(t *testing.T) {
	nav := httptest.NewRequest("GET", "/x", nil)
	nav.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	nav.Header.Set("Sec-Fetch-Dest", "document")
	rec := httptest.NewRecorder()
	Error(rec, nav, "sign in to watch", http.StatusUnauthorized)
	if rec.Code != 401 {
		t.Fatalf("status %d", rec.Code)
	}
	assertThemed(t, rec.Body.String())

	// a media/XHR fetch keeps the plain-text body
	xhr := httptest.NewRequest("GET", "/x", nil)
	xhr.Header.Set("Accept", "*/*")
	xhr.Header.Set("Sec-Fetch-Dest", "empty")
	rec = httptest.NewRecorder()
	Error(rec, xhr, "sign in to watch", http.StatusUnauthorized)
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || strings.Contains(rec.Body.String(), "<") {
		t.Errorf("xhr got %q %q", rec.Header().Get("Content-Type"), rec.Body.String())
	}

	// no fetch metadata: Accept decides
	old := httptest.NewRequest("GET", "/x", nil)
	old.Header.Set("Accept", "text/html")
	if !WantsHTML(old) {
		t.Error("Accept text/html without Sec-Fetch-Dest should want HTML")
	}

	rec = httptest.NewRecorder()
	NotFound(rec, nav)
	if rec.Code != 404 {
		t.Fatalf("404 status %d", rec.Code)
	}
	assertThemed(t, rec.Body.String())
}

func TestInject(t *testing.T) {
	doc := []byte("<!doctype html><html><head><title>r</title><style>body{color:red}</style></head><body>x</body></html>")
	out := string(Inject(doc))
	if !strings.HasPrefix(out, "<!doctype html><html><head>"+MetaColorScheme+"<style>"+BaseCSS+"</style><title>") {
		t.Errorf("injected at the wrong place: %s", out)
	}
	if got := string(Inject([]byte(out))); got != out {
		t.Error("Inject is not idempotent")
	}
	bare := string(Inject([]byte("<!doctype html><p>hi")))
	if !strings.HasPrefix(bare, "<!doctype html>"+MetaColorScheme) {
		t.Errorf("no-head doc: %s", bare)
	}
	if InjectHash == "" || StyleHash == InjectHash {
		t.Error("hashes")
	}
}
