package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

// A components write through the real sign-in stack (applySecurity: the JWT bearer, the
// auth gate, CSRF) and the production router: no token is 401, a viewer's token 403 with
// nothing written, an admin's token 200. The handler tests inject the principal; this
// one has the middleware make it from a signed access token.
func TestPGComponentsWriteThroughSignIn(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	a, err := initAuth(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	adminID, viewerID := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, 'si-admin', 'x', true), ($2, 'si-viewer', 'x', false)`,
		adminID, viewerID); err != nil {
		t.Fatal(err)
	}
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	router.MountP51(r, router.P51Deps{DB: db})
	h := a.applySecurity(r, nil, nil, nil)

	token := func(user string, admin bool) string {
		t.Helper()
		tok, err := jwt.Sign(a.keys, jwt.Claims{Iss: "maktaba", Aud: "api", Sub: user, Usr: user, IsAdmin: admin})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	put := func(tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/components/telemetry", strings.NewReader(`{"want":"installed"}`))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	want := func() string {
		t.Helper()
		var w string
		err := db.QueryRow(`SELECT want FROM components WHERE id = 'telemetry'`).Scan(&w)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			t.Fatal(err)
		}
		return w
	}
	before := want()
	if rec := put(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}
	if rec := put(token(viewerID, false)); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer: %d %s", rec.Code, rec.Body)
	}
	if got := want(); got != before {
		t.Fatalf("a viewer's write changed telemetry: %q -> %q", before, got)
	}
	if rec := put(token(adminID, true)); rec.Code != http.StatusOK {
		t.Fatalf("an admin: %d %s", rec.Code, rec.Body)
	}
	if got := want(); got != "installed" {
		t.Fatalf("an admin's write: telemetry is %q", got)
	}
}
