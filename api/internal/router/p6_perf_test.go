package router

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	perfh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/perf"
	perfpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
)

// nopConnector yields a non-nil *sql.DB without ever dialing. MountP6
// only needs DB != nil to proceed with handler wiring; no query runs in
// this test.
type nopConnector struct{}

func (nopConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, driver.ErrBadConn
}
func (nopConnector) Driver() driver.Driver { return nil }

// TestMountP6_RegistersEmbedCache_AdminFlushNoLonger404 proves the
// HLB-346 fix: MountP6 registers the search embedding cache into the
// shared perf.Registry, so POST /admin/cache/search_embed/flush (wired
// by MountP10) resolves the cache instead of 404-ing on an empty
// registry (the prior orphaned-infrastructure bug).
func TestMountP6_RegistersEmbedCache_AdminFlushNoLonger404(t *testing.T) {
	reg := perfpkg.NewRegistry()
	db := sql.OpenDB(nopConnector{})
	defer db.Close()

	r := chi.NewRouter()
	MountP6(r, P6Deps{DB: db, PerfRegistry: reg})

	// The embedding cache must now be registered by name.
	if _, ok := reg.Lookup("search_embed"); !ok {
		t.Fatalf("search_embed not registered; registry=%v", reg.Names())
	}

	// And the admin flush endpoint (the previously-dead path) must
	// resolve it: 204, not 404.
	admin := &perfh.Handler{Registry: reg}
	ar := chi.NewRouter()
	admin.Mount(ar)
	req := httptest.NewRequest(http.MethodPost, "/admin/cache/search_embed/flush", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()
	ar.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin flush status=%d body=%s (want 204)", rec.Code, rec.Body.String())
	}
}

// TestMountP6_NilRegistry_StillSafe proves the cache still works when no
// registry is supplied (the dev/test path) — registration is best-effort.
func TestMountP6_NilRegistry_StillSafe(_ *testing.T) {
	db := sql.OpenDB(nopConnector{})
	defer db.Close()
	r := chi.NewRouter()
	MountP6(r, P6Deps{DB: db, PerfRegistry: nil})
	// No panic = pass.
}
