package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/vodfallback"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

// The cluster sign-in ("Continue with <home>", Epic 35 story 35.9) is removed: its routes
// (the node's start and callback, the home's authorize and exchange) answer 404 on every
// machine, to anyone and whatever the method (never a 401 that asks for a sign-in), and a
// node answers them itself (never through the home, which may be away).
func TestTheClusterSignInIsGone(t *testing.T) {
	r := chi.NewRouter()
	router.MountNodeLocal(r, router.NodeLocalDeps{})
	proxied := 0
	rt := &nodeRuntime{
		resolver: &nodeauth.Resolver{Link: &fakeHome{}},
		vod:      &vodfallback.Fallback{},
		proxy: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			proxied++
			w.WriteHeader(http.StatusTeapot)
		}),
	}
	machines := map[string]http.Handler{
		"node": rt.handler(&authState{}, r, r),
		"home": (&authState{}).applySecurity(r, nil, nil, nil),
	}
	sess := &http.Cookie{Name: nodeauth.CookieSession, Value: "6f1d2c3b-0000-4000-8000-000000000001"}
	for name, h := range machines {
		for _, path := range []string{"/api/cluster/sso/start?return=/library", "/api/cluster/sso/callback?code=c&state=s",
			"/api/cluster/sso/authorize?node=6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7", "/api/cluster/sso/exchange"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				for _, cookie := range []*http.Cookie{nil, sess} {
					if cookie != nil && method != http.MethodGet {
						continue // a signed-in browser's change needs its CSRF token first, as everywhere
					}
					req := httptest.NewRequest(method, path, nil)
					if cookie != nil {
						req.AddCookie(cookie)
					}
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					if rec.Code != http.StatusNotFound {
						t.Errorf("%s: %s %s (signed in %v): %d, want 404", name, method, path, cookie != nil, rec.Code)
					}
				}
			}
		}
	}
	if proxied != 0 {
		t.Fatalf("the node sent %d of them to the home", proxied)
	}
}
