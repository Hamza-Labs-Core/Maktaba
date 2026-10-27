package router

import (
	"database/sql"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
)

// everyMount builds the router as main.go does on the home, every surface mounted, over
// a database that is never reached (a mount must not need one to register its routes).
func everyMount(t *testing.T) chi.Router {
	t.Helper()
	db, err := sql.Open("postgres", "postgres://x:x@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := New(Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	MountLogs(r, LogsDeps{})
	MountProcesses(r, ProcessesDeps{})
	MountServerSettings(r, nil)
	MountP28(r, P28Deps{Updater: system.NewUpdater(system.UpdaterConfig{Disabled: true})})
	var mountRemote func(chi.Router)
	MountP6(r, P6Deps{DB: db, OnQueues: func(q *queues.Handler) {
		_, _, mountRemote = WireP32Remote(q, P32RemoteDeps{DB: db, Logger: slog.Default()})
	}})
	ks := keys.NewSet(0)
	MountP9(r, P9Deps{DB: db, Keys: ks})
	MountP10(r, P10Deps{DB: db, Keys: ks, Logger: slog.Default()})
	MountP11(r, P11Deps{DB: db})
	MountP27(r, P27Deps{DB: db})
	if mountRemote != nil {
		mountRemote(r)
	}
	MountP33(r, P33Deps{DB: db})
	MountP29(r, P29Deps{DB: db, Driver: "postgres"})
	h := MountP31(r, P31Deps{DB: db, Driver: "postgres"})
	MountP31Agent(r, h, P31AgentDeps{DB: db, Driver: "postgres", Config: agent.Config{}})
	MountClusterAuth(r, ClusterAuthDeps{DB: db})
	MountNodeLocal(r, NodeLocalDeps{})
	return r
}

// TC1 of story 35.10: every route has a placement class.
func TestEveryRouteHasPlacementClass(t *testing.T) {
	r := everyMount(t)
	var missing []string
	n := 0
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		n++
		if _, ok := Placement.Classify(method, route); !ok {
			missing = append(missing, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 250 {
		t.Fatalf("only %d routes walked: a surface is missing from everyMount", n)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d route(s) without a placement class (add them to router/placement.go):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

func TestPlacementExamples(t *testing.T) {
	cases := []struct {
		method, pattern string
		want            placement.Class
	}{
		{"GET", "/api/system/version", placement.Local},
		{"GET", "/api/cluster/self", placement.Local},
		// the cluster sign-in's routes, removed: every machine answers them 404 itself
		{"GET", "/api/cluster/sso/callback", placement.Local},
		{"GET", "/api/cluster/sso/authorize", placement.Local},
		{"POST", "/api/cluster/sso/exchange", placement.Local},
		{"POST", "/api/stream/sessions", placement.Local},
		{"POST", "/api/stream/sessions/{id}/progress", placement.Home},
		{"POST", "/api/stream/sessions/{id}/errors", placement.Local},
		{"GET", "/api/videos", placement.Any},
		{"GET", "/api/videos/{id}", placement.Any},
		{"PATCH", "/api/videos/{id}", placement.Home},
		{"GET", "/api/videos/{id}/poster", placement.Home},
		{"POST", "/api/auth/login", placement.Home},
		{"GET", "/api/auth/me", placement.Home},
		{"GET", "/api/cluster/ws", placement.Home},
		{"GET", "/api/cluster", placement.Home},
		{"GET", "/api/me/history", placement.Home},
		{"POST", "/api/search", placement.Home},
		{"GET", "/api/tv/changes", placement.Home},
		{"PATCH", "/api/rules/{section}", placement.Home},
		{"GET", "/api/rules", placement.Any},
	}
	for _, c := range cases {
		got, ok := Placement.Classify(c.method, c.pattern)
		if !ok || got != c.want {
			t.Errorf("%s %s = %s (named %v), want %s", c.method, c.pattern, got, ok, c.want)
		}
	}
	if got, ok := Placement.Classify("GET", "/api/brand-new"); ok || got != placement.Home {
		t.Errorf("an unnamed route is home by default, got %s (%v)", got, ok)
	}
}
