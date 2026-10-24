package clusterauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// /api/cluster/self is public: the home's machines that cannot log in to its database go
// to an admin or this machine's supervisor (its local token) only.
func TestSelfMachinesDBNotPublic(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("t0k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", tok)
	r := chi.NewRouter()
	MountSelf(r, func(context.Context) SelfInfo {
		return SelfInfo{Role: "home", MachinesDB: []SelfMachineDB{{ID: "n1", Name: "studio-gpu", Code: "auth_failed"}}}
	})
	get := func(mod func(*http.Request) *http.Request) string {
		req := httptest.NewRequest(http.MethodGet, "/api/cluster/self", nil)
		req = mod(req)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	if b := get(func(r *http.Request) *http.Request { return r }); strings.Contains(b, "studio-gpu") {
		t.Fatalf("public: %s", b)
	}
	if b := get(func(r *http.Request) *http.Request { r.Header.Set(LocalTokenHeader, "nope"); return r }); strings.Contains(b, "studio-gpu") {
		t.Fatalf("a wrong token: %s", b)
	}
	if b := get(func(r *http.Request) *http.Request { r.Header.Set(LocalTokenHeader, "t0k"); return r }); !strings.Contains(b, "studio-gpu") {
		t.Fatalf("the supervisor: %s", b)
	}
	admin := func(r *http.Request) *http.Request {
		return r.WithContext(principal.WithPrincipal(r.Context(), &principal.Principal{UserID: "a", IsAdmin: true}))
	}
	if b := get(admin); !strings.Contains(b, "studio-gpu") {
		t.Fatalf("an admin: %s", b)
	}
}
