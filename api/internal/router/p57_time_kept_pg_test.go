package router_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const tmKeptAdmin = "00000000-0000-4000-8000-0000005714f3"

// Making an account restricted, and lifting it, reach the streaming service at
// once through the production router: restricted, a stream URL signed before it is
// refused there; lifted, nothing stale refuses the account's new URLs.
func TestPGTimeKeptReachesStreaming(t *testing.T) {
	t.Parallel()
	db := seedTime(t)
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'tm-admin','x',true)`, tmKeptAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE app_settings SET value = 'true' WHERE key = 'parental.enabled'`); err != nil {
		t.Fatal(err)
	}
	svc := &tmStreaming{leases: map[string]time.Time{}}
	e := setupTimeRouter(t, db, svc, &tmSigner{})
	inner := e.h
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Test-As") == "admin" {
			req = req.WithContext(principal.WithPrincipal(req.Context(),
				&principal.Principal{UserID: tmKeptAdmin, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}))
		}
		inner.ServeHTTP(w, req)
	})
	said := func() []string {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return slices.Clone(svc.kept)
	}

	// the viewer has a stream open, signed as an account whose time is not kept
	if code, v := open(e, "viewer"); code != http.StatusCreated {
		t.Fatalf("the viewer: %d %v", code, v)
	}
	e.want(http.StatusOK, "admin", http.MethodPut, "/api/users/"+tmViewer+"/parental",
		map[string]any{"max_tier": 9, "unrated": "hide"})
	if want := []string{tmViewer + "=true"}; !slices.Equal(said(), want) {
		t.Fatalf("after restricting, streaming was told %v, want %v", said(), want)
	}
	e.want(http.StatusNoContent, "admin", http.MethodDelete, "/api/users/"+tmViewer+"/parental", nil)
	if want := []string{tmViewer + "=true", tmViewer + "=false"}; !slices.Equal(said(), want) {
		t.Fatalf("after lifting, streaming was told %v, want %v", said(), want)
	}
	// streaming not answering does not undo the policy change
	svc.mu.Lock()
	svc.fail = http.ErrHandlerTimeout
	svc.mu.Unlock()
	e.want(http.StatusOK, "admin", http.MethodPut, "/api/users/"+tmViewer+"/parental",
		map[string]any{"max_tier": 9, "unrated": "hide"})
}
