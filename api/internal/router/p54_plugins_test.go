package router

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// 54.04 T10: the plugin API is never on the TCP router; only the invocation routes are.
func TestP54HasNoPluginAPIOnTCP(t *testing.T) {
	r := chi.NewRouter()
	db, _ := sql.Open("postgres", "postgres://invalid/none")
	MountP54(r, P54Deps{DB: db})
	var routes []string
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+route)
		return nil
	})
	if len(routes) != 2 {
		t.Fatalf("routes %v", routes)
	}
	for _, rt := range routes {
		if strings.Contains(rt, "/plugin-api") || !strings.Contains(rt, "/internal/plugins/invocations") {
			t.Fatalf("route %s on TCP", rt)
		}
	}
}
