package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// The served Expires is rendered per request: ahead of now and under a year, whenever
// the API started.
func TestSecurityTxtExpiresAhead(t *testing.T) {
	_, r := newRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil))
	var exp time.Time
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if v, ok := strings.CutPrefix(line, "Expires: "); ok {
			var err error
			if exp, err = time.Parse(time.RFC3339, v); err != nil {
				t.Fatalf("Expires %q: %v", v, err)
			}
		}
	}
	now := time.Now()
	if !exp.After(now) || !exp.Before(now.AddDate(1, 0, 0)) {
		t.Fatalf("Expires %v: want ahead of now and under a year", exp)
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
