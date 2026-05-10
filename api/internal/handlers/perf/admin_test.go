package perf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	libperf "github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
)

func adminCtx() context.Context {
	return principal.WithPrincipal(context.Background(),
		&principal.Principal{UserID: "admin", IsAdmin: true})
}

func TestFlushCacheRequiresAdmin(t *testing.T) {
	h := &Handler{Registry: libperf.NewRegistry()}
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/admin/cache/manifest/flush", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestFlushCacheUnknownCache(t *testing.T) {
	h := &Handler{Registry: libperf.NewRegistry()}
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/admin/cache/mystery/flush", nil).WithContext(adminCtx())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestFlushCacheSuccess(t *testing.T) {
	cache := libperf.NewCache[int]("manifest", 4, time.Minute)
	cache.Put("a", 1)
	reg := libperf.NewRegistry()
	reg.Register(cache)
	h := &Handler{Registry: reg}
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/admin/cache/manifest/flush", nil).WithContext(adminCtx())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := cache.Get("a"); ok {
		t.Fatal("flush did not clear cache")
	}
}
