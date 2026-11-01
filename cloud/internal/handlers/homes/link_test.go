package homes

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

const (
	linkServer = "0a1b2c3d-0000-4000-8000-000000000000"
	linkNonce  = "aGVsbG8tdGhpcy1pcy1hLW5vbmNl"
)

func linkRouter(d *Deps) http.Handler {
	r := chi.NewRouter()
	MountPublic(r, d)
	return r
}

func getLink(t *testing.T, h http.Handler, server, nonce, ret string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{}
	if nonce != "" {
		q.Set("nonce", nonce)
	}
	if ret != "" {
		q.Set("return", ret)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/homes/"+server+"/link?"+q.Encode(), nil))
	return rec
}

// A home's Account page sends the browser here: it always lands on this cloud's own
// confirm page, carrying what the page needs.
func TestLinkRedirectsToTheCloudsOwnConfirmPage(t *testing.T) {
	h := linkRouter(&Deps{WebURL: "https://maktaba.tv/"})
	rec := getLink(t, h, linkServer, linkNonce, "http://192.168.1.5:8088/account#old")
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || loc.Scheme != "https" || loc.Host != "maktaba.tv" || loc.Path != "/link-home" {
		t.Fatalf("Location %q", rec.Header().Get("Location"))
	}
	q := loc.Query()
	if q.Get("server") != linkServer || q.Get("nonce") != linkNonce || q.Get("return") != "http://192.168.1.5:8088/account" {
		t.Errorf("query %v (the return keeps no fragment)", q)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers %v", rec.Header())
	}
}

func TestLinkRefusesWhatIsNotALinkStart(t *testing.T) {
	h := linkRouter(&Deps{WebURL: "https://maktaba.tv"})
	for name, tc := range map[string]struct{ server, nonce, ret string }{
		"no nonce":             {linkServer, "", "https://home.example/account"},
		"a short nonce":        {linkServer, "abc", "https://home.example/account"},
		"a nonce with a slash": {linkServer, "aGVsbG8tdGhpcy1pcy1h/W5vbmNl", "https://home.example/account"},
		"not a server id":      {"..%2f..%2fadmin", linkNonce, "https://home.example/account"},
		"no return":            {linkServer, linkNonce, ""},
		"a javascript: return": {linkServer, linkNonce, "javascript:alert(1)"},
		"a relative return":    {linkServer, linkNonce, "/account"},
		"credentials in it":    {linkServer, linkNonce, "https://user:pw@home.example/account"},
		"too long":             {linkServer, linkNonce, "https://home.example/" + strings.Repeat("a", 2100)},
	} {
		if rec := getLink(t, h, tc.server, tc.nonce, tc.ret); rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, Location %q", name, rec.Code, rec.Header().Get("Location"))
		} else if rec.Header().Get("Location") != "" {
			t.Errorf("%s: redirected to %q", name, rec.Header().Get("Location"))
		}
	}
}

func TestLinkWithoutAWebAppIsUnavailable(t *testing.T) {
	rec := getLink(t, linkRouter(&Deps{}), linkServer, linkNonce, "https://home.example/account")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Location") != "" {
		t.Fatalf("status %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
}
