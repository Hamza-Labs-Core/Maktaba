package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// Epic 53 story 53.16: MountLogs mounts the problem reports' Maktaba routes and answers the
// forwarder main declares as api:reports_forwarder.
func TestMountLogsAnswersTheReportsForwarder(t *testing.T) {
	r := chi.NewRouter()
	fw := MountLogs(r, LogsDeps{})
	if fw == nil {
		t.Fatal("no forwarder")
	}
	mounted := map[string]bool{}
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		mounted[method+" "+route] = true
		return nil
	})
	for _, rt := range []string{"GET /api/reports/maktaba", "PUT /api/reports/maktaba", "DELETE /api/reports/maktaba",
		"POST /api/reports/{id}/share", "GET /api/reports/{id}/maktaba-preview", "GET /api/reports/{id}/screenshot"} {
		if !mounted[rt] {
			t.Errorf("%s not mounted", rt)
		}
	}
	// nobody signed in: refused like every report route
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/reports/maktaba", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}
}

// Story 53.12: the dashboards' offline machines come from the cluster handler MountP31
// mounted (after MountLogs); none before it is mounted, or on a home without one.
func TestDashboardOfflineMachinesFollowTheCluster(t *testing.T) {
	ctx := context.Background()
	prev := dashboardMachines.Load()
	t.Cleanup(func() { dashboardMachines.Store(prev) })
	dashboardMachines.Store(nil)
	if ids, err := offlineMachines(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("no cluster: %v %v", ids, err)
	}
	h := MountP31(chi.NewRouter(), P31Deps{Driver: "sqlite"})
	if dashboardMachines.Load() != h {
		t.Fatal("MountP31 did not hand its handler to the dashboards")
	}
	store := h.Store.(*cluster.MemoryStore)
	if err := store.PutPending(ctx, cluster.Node{ID: "n1", Name: "n1", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(ctx, "n1", "r_n1", nil, "admin", time.Now()); err != nil {
		t.Fatal(err)
	}
	if ids, err := offlineMachines(ctx); err != nil || len(ids) != 1 || ids[0] != "n1" {
		t.Fatalf("an approved machine never seen: %v %v, want it offline", ids, err)
	}
}
