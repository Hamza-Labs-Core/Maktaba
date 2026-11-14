package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPackagedTVOrigin(t *testing.T) {
	for origin, want := range map[string]bool{
		"null":                 true,
		"file://":              true,
		"app://com.maktaba.tv": true,
		"":                     false,
		"file://evil.example":  false,
		"https://evil.example": false,
		"https://maktaba.tv":   false,
		"Null":                 false,
		"app:/x":               false,
	} {
		if got := PackagedTVOrigin(origin); got != want {
			t.Errorf("PackagedTVOrigin(%q) = %v, want %v", origin, got, want)
		}
	}
}

// TVCORS answers a TV origin on its paths only, never with credentials, and passes all else on.
func TestTVCORS(t *testing.T) {
	reached := 0
	h := TVCORS([]string{"/v1/tv/codes"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusTeapot)
	}))
	do := func(method, path, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// a preflight from a TV is answered here, before the handler
	rec := do(http.MethodOptions, "/v1/tv/codes", "null")
	if rec.Code != http.StatusNoContent || reached != 0 {
		t.Fatalf("preflight: %d, reached %d", rec.Code, reached)
	}
	hd := rec.Header()
	if hd.Get("Access-Control-Allow-Origin") != "null" || hd.Get("Vary") != "Origin" ||
		hd.Get("Access-Control-Allow-Methods") != "POST, OPTIONS" ||
		hd.Get("Access-Control-Allow-Headers") != "Content-Type, X-Maktaba-Client" ||
		hd.Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("preflight headers: %v", hd)
	}
	if hd.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials allowed")
	}

	// the call itself reaches the handler with the origin echoed
	rec = do(http.MethodPost, "/v1/tv/codes", "file://")
	if rec.Code != http.StatusTeapot || rec.Header().Get("Access-Control-Allow-Origin") != "file://" {
		t.Fatalf("post: %d %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials allowed")
	}

	// another origin, another path, or no origin: untouched
	for _, c := range []struct{ method, path, origin string }{
		{http.MethodOptions, "/v1/tv/codes", "https://evil.example"},
		{http.MethodPost, "/v1/tv/codes", "file://evil.example"},
		{http.MethodOptions, "/v1/homes", "null"},
		{http.MethodPost, "/v1/tv/codes/x/approve", "null"},
		{http.MethodPost, "/v1/tv/codes", ""},
	} {
		before := reached
		rec := do(c.method, c.path, c.origin)
		if reached != before+1 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s %s from %q: reached %v, headers %v", c.method, c.path, c.origin, reached > before, rec.Header())
		}
	}
}
