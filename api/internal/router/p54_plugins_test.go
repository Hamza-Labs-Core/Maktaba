package router

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
)

// 54.04 T10: the plugin API is never on the TCP router; only the invocation and breaker
// routes are (and, with the components service, /api/plugins/*).
func TestP54HasNoPluginAPIOnTCP(t *testing.T) {
	r := chi.NewRouter()
	db, _ := sql.Open("postgres", "postgres://invalid/none")
	MountP54(r, P54Deps{DB: db})
	var routes []string
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+route)
		return nil
	})
	if len(routes) != 3 {
		t.Fatalf("routes %v", routes)
	}
	for _, rt := range routes {
		if strings.Contains(rt, "/plugin-api") || !strings.Contains(rt, "/internal/plugins/") {
			t.Fatalf("route %s on TCP", rt)
		}
	}
}

// With the components service, the admin routes are mounted too (and the components
// service has the plugins' hooks).
func TestP54MountsTheAdminRoutesWithTheComponentsService(t *testing.T) {
	r := chi.NewRouter()
	db, _ := sql.Open("postgres", "postgres://invalid/none")
	svc := components.NewService(db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	MountP54(r, P54Deps{DB: db, Components: svc, Ctx: ctx})
	n := 0
	_ = chi.Walk(r, func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/plugins") {
			n++
		}
		return nil
	})
	// story 54.20: the panels' routes, for every signed-in user
	panels := 0
	_ = chi.Walk(r, func(_, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/panels") {
			panels++
		}
		return nil
	})
	if panels != 2 {
		t.Fatalf("%d panel routes", panels)
	}
	if n < 15 || svc.Plugins == nil {
		t.Fatalf("%d plugin routes, hooks %v", n, svc.Plugins)
	}
}
