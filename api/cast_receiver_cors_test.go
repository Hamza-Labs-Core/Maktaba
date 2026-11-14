package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/castgrant"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httpsec"
)

// TestTheHomeAnswersTheCastReceiver (Epic 58 story 58.07): the home's CORS lets Maktaba's
// Cast receiver call it with a bearer, and never with credentials, whatever the operator's
// own allowed origins say.
func TestTheHomeAnswersTheCastReceiver(t *testing.T) {
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "")
	st, err := initAuth(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := httpsec.CORS(st.cors)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for origin, want := range map[string]string{castgrant.ReceiverOrigin: castgrant.ReceiverOrigin, "https://elsewhere.example": ""} {
		req := httptest.NewRequest(http.MethodGet, "/api/discovery/home", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Authorization", "Bearer cast_x")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("%s: Access-Control-Allow-Origin %q, want %q", origin, got, want)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("%s: Access-Control-Allow-Credentials %q", origin, got)
		}
	}
}
