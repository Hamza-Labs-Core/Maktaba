package filler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// adminReq / userReq build a request carrying a principal so the
// admin-gate branches can be exercised without a database (every path
// tested here returns before any DB access).
func adminReq(method, target, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	ctx := principal.WithPrincipal(r.Context(), &principal.Principal{UserID: "u1", IsAdmin: true})
	return r.WithContext(ctx)
}

func serve(req *http.Request) *httptest.ResponseRecorder {
	h := &Handler{}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TC: mutations are admin-only; an anonymous request is forbidden before
// any DB work happens.
func TestCreatePool_RequiresAdmin(t *testing.T) {
	rec := serve(httptest.NewRequest(http.MethodPost, "/api/filler/pools", strings.NewReader(`{"name":"x"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

func TestDeletePool_RequiresAdmin(t *testing.T) {
	rec := serve(httptest.NewRequest(http.MethodDelete, "/api/filler/pools/abc", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

func TestAddItems_RequiresAdmin(t *testing.T) {
	rec := serve(httptest.NewRequest(http.MethodPost, "/api/filler/pools/p1/items",
		strings.NewReader(`{"items":[{"video_id":"v1","type":"bumper"}]}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

// TC1-ish: a pool with no name is unprocessable (inline validation),
// before the insert.
func TestCreatePool_EmptyName_Unprocessable(t *testing.T) {
	rec := serve(adminReq(http.MethodPost, "/api/filler/pools", `{"name":"   "}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422", rec.Code)
	}
}

// TC: empty item list is rejected with 422.
func TestAddItems_Empty_Unprocessable(t *testing.T) {
	rec := serve(adminReq(http.MethodPost, "/api/filler/pools/p1/items", `{"items":[]}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422", rec.Code)
	}
}

// TC1: a kind outside bumper|filler|station_id is rejected (mirrors the
// 0085 CHECK) before the insert.
func TestAddItems_InvalidKind_Unprocessable(t *testing.T) {
	rec := serve(adminReq(http.MethodPost, "/api/filler/pools/p1/items",
		`{"items":[{"video_id":"v1","type":"trailer"}]}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422", rec.Code)
	}
}

// TC: a missing video_id is rejected before the insert.
func TestAddItems_MissingVideoID_Unprocessable(t *testing.T) {
	rec := serve(adminReq(http.MethodPost, "/api/filler/pools/p1/items",
		`{"items":[{"video_id":"","type":"bumper"}]}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d want 422", rec.Code)
	}
}

// validKinds is the single source of truth shared with the CHECK; assert
// the closed vocabulary so a typo is caught at the unit level.
func TestValidKinds(t *testing.T) {
	for _, ok := range []string{"bumper", "filler", "station_id"} {
		if !validKinds[ok] {
			t.Errorf("%q should be a valid kind", ok)
		}
	}
	for _, bad := range []string{"", "trailer", "ad", "BUMPER"} {
		if validKinds[bad] {
			t.Errorf("%q should not be a valid kind", bad)
		}
	}
}
