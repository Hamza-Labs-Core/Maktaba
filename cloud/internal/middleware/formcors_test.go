package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// FormCORS answers the forms' own origins on the forms' paths only, never with credentials (even
// when the web allow-list behind it would add them), and passes everything else on.
func TestFormCORS(t *testing.T) {
	reached := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusAccepted)
	})
	// the web allow-list (which names maktaba.tv, with credentials) runs after FormCORS
	h := FormCORS([]string{"/v1/moderation/reports"}, []string{"https://watch.maktaba.tv", "https://maktaba.tv"})(
		CORS([]string{"https://maktaba.tv"})(inner))
	do := func(method, path, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	pre := do(http.MethodOptions, "/v1/moderation/reports", "https://watch.maktaba.tv")
	if pre.Code != http.StatusNoContent || pre.Header().Get("Access-Control-Allow-Origin") != "https://watch.maktaba.tv" ||
		pre.Header().Get("Access-Control-Allow-Methods") != "POST, OPTIONS" || pre.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Errorf("preflight = %d %v", pre.Code, pre.Header())
	}
	if reached != 0 {
		t.Error("a preflight reached the handler")
	}
	for _, origin := range []string{"https://watch.maktaba.tv", "https://maktaba.tv"} {
		rec := do(http.MethodPost, "/v1/moderation/reports", origin)
		if rec.Code != http.StatusAccepted || rec.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("%s post = %d %v", origin, rec.Code, rec.Header())
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("%s post allows credentials", origin)
		}
	}
	// another origin, or another path, gets nothing from FormCORS
	if rec := do(http.MethodPost, "/v1/moderation/reports", "https://evil.example"); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("another origin = %v", rec.Header())
	}
	if rec := do(http.MethodPost, "/v1/auth/login", "https://watch.maktaba.tv"); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("the watch origin on another path = %v", rec.Header())
	}
	// the web allow-list still answers maktaba.tv elsewhere, with its credentials
	if rec := do(http.MethodPost, "/v1/auth/login", "https://maktaba.tv"); rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("the web allow-list lost its credentials elsewhere: %v", rec.Header())
	}
}
