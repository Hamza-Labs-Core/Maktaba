//go:build integration

package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// TestReservedInfraSlugs is story 50.01 T02: the names the cloud's own infrastructure uses
// (docs/cloud-dns.md) are refused as a home's slug, at redeem and at a slug claim, with
// 409 subdomain_reserved; an ordinary slug still works.
func TestReservedInfraSlugs(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	users := stores.NewUsers(pool.DB)
	srvs := stores.NewServers(pool.DB)
	u, err := users.Create(ctx, "owner@example.test", "x", "Owner", true)
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{Servers: srvs}
	sd := SubdomainDeps{DB: pool.DB}
	r := chi.NewRouter()
	MountAgent(r, d)
	r.Group(func(g chi.Router) {
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(middleware.WithUserID(r.Context(), u.ID)))
			})
		})
		MountSubdomains(g, sd)
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	post := func(path string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, err := srvs.MintClaim(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"canary", "r12", "r1", "r99", "direct", "plugins", "plugins-cdn", "staging", "status", "api"} {
		st, out := post("/v1/servers/claims/redeem", map[string]any{"code": code, "name": "box", "slug": slug, "version": "1"})
		if st != http.StatusConflict || out["error"] != "subdomain_reserved" {
			t.Errorf("redeem slug %q: %d %v, want 409 subdomain_reserved", slug, st, out)
		}
	}
	// the code survives the refusals and an ordinary slug takes it
	st, out := post("/v1/servers/claims/redeem", map[string]any{"code": code, "name": "box", "slug": "skylark-12", "version": "1"})
	if st != http.StatusCreated {
		t.Fatalf("redeem skylark-12: %d %v", st, out)
	}
	serverID, _ := out["server_id"].(string)

	for _, slug := range []string{"canary", "r3", "direct"} {
		st, out := post("/v1/subdomains/claim", map[string]any{"slug": slug, "server_id": serverID})
		if st != http.StatusConflict || out["error"] != "subdomain_reserved" {
			t.Errorf("claim slug %q: %d %v, want 409 subdomain_reserved", slug, st, out)
		}
	}
	// r100 is not an infrastructure name (only r1…r99 are)
	if st, out := post("/v1/subdomains/check", map[string]any{"slug": "r100"}); st != 200 || out["available"] != true {
		t.Errorf("check r100: %d %v, want available", st, out)
	}
	if st, out := post("/v1/subdomains/check", map[string]any{"slug": "canary"}); st != 200 || out["reason"] != "reserved:infrastructure" {
		t.Errorf("check canary: %d %v, want reserved:infrastructure", st, out)
	}
}
