package router

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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
