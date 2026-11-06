package router_test

// Epic 56 story 56.14 T01: shared/api/openapi.json describes every route the TV apps and
// the Players read from the browsing, series, play queue and collection handlers, so the
// document cannot fall behind them again: a route mounted here and missing there fails.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/browse"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/collections"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/playqueue"
	seriesh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/series"
)

var openapiParam = regexp.MustCompile(`\{[^}]+\}`)

// openapiOperations is "METHOD /path" of every operation, parameters as {}.
func openapiOperations(t *testing.T) map[string]bool {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for p, item := range doc.Paths {
		for m := range item {
			switch m {
			case "get", "post", "put", "patch", "delete":
				out[strings.ToUpper(m)+" "+openapiParam.ReplaceAllString(p, "{}")] = true
			}
		}
	}
	return out
}

func TestOpenAPIDescribesTheLibraryRoutes(t *testing.T) {
	r := chi.NewRouter()
	(&browse.Handler{}).Mount(r)
	(&seriesh.Handler{}).Mount(r)
	(&playqueue.Handler{}).Mount(r)
	(&collections.Handler{}).Mount(r)
	ops := openapiOperations(t)
	var missing []string
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		key := method + " " + openapiParam.ReplaceAllString(strings.TrimPrefix(route, "/api"), "{}")
		if !ops[key] {
			missing = append(missing, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("shared/api/openapi.json does not describe %d route(s):\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}
