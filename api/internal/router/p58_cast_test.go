package router

// Epic 58 story 58.07: every route the cast grant's gate lets a grant call is a route the
// home mounts (a renamed route would leave the receiver refused), and the auth bootstrap
// answers the receiver's origin.

import (
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/castgrant"
)

func TestCastGrantRoutesAreMounted(t *testing.T) {
	r := everyMount(t)
	for k := range castgrant.Routes {
		method, pattern, _ := strings.Cut(k, " ")
		path := strings.ReplaceAll(pattern, "{id}", "00000000-0000-4000-8000-000000580700")
		rctx := chi.NewRouteContext()
		if got := r.Find(rctx, method, path); got != pattern {
			t.Errorf("%s: the home mounts %q for %s", k, got, path)
		}
	}
	if !r.Match(chi.NewRouteContext(), "POST", "/api/cast/grants") {
		t.Fatal("POST /api/cast/grants is not mounted")
	}
}
