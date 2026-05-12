package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/security"
)

func newRouter(_ *testing.T) (*Handler, http.Handler) {
	h := &Handler{Policy: security.DefaultPolicy()}
	r := chi.NewRouter()
	h.Mount(r)
	return h, r
}

func TestSecurityTxtIsPublic(t *testing.T) {
	_, r := newRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type=%s", ct)
	}
	if !strings.Contains(rec.Body.String(), "Contact:") {
		t.Fatal("missing Contact:")
	}
}

func TestSBOMRequiresAdmin(t *testing.T) {
	_, r := newRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system/sbom", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous=%d", rec.Code)
	}

	userCtx := principal.WithPrincipal(context.Background(),
		&principal.Principal{UserID: "u", IsAdmin: false})
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/system/sbom", nil).WithContext(userCtx))
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("regular user=%d", rec2.Code)
	}
}

func TestSBOMOkForAdmin(t *testing.T) {
	_, r := newRouter(t)
	ctx := principal.WithPrincipal(context.Background(),
		&principal.Principal{UserID: "u", IsAdmin: true})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system/sbom", nil).WithContext(ctx))
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
}
