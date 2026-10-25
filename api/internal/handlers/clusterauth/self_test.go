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
	chimw "github.com/go-chi/chi/v5/middleware"

	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// /api/cluster/self is public: the home's machines that cannot log in to its database go
// to this machine's supervisor (its local token) only.
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
}

// Whether the home installs releases by itself goes to a program on this machine (the Linux
// app asks over loopback, straight or through the web port) and its supervisor, never to
// the LAN.
func TestSelfUpdatesAutoLocalOnly(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("t0k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_SUPERVISOR_TOKEN_FILE", tok)
	r := chi.NewRouter()
	r.Use(mw.MarkLocalCaller) // as the router's chain does, before RealIP
	r.Use(chimw.RealIP)       //nolint:staticcheck // SA1019: as the router chains it
	on := true
	MountSelf(r, func(context.Context) SelfInfo { return SelfInfo{Role: "home", UpdatesAuto: &on} })
	get := func(remote string, headers map[string]string) bool {
		req := httptest.NewRequest(http.MethodGet, "/api/cluster/self", nil)
		req.RemoteAddr = remote
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return strings.Contains(rec.Body.String(), `"updates_auto":true`)
	}
	if !get("127.0.0.1:41000", nil) {
		t.Fatal("the app on this machine was not told")
	}
	if !get("127.0.0.1:41000", map[string]string{"X-Forwarded-For": "127.0.0.1"}) {
		t.Fatal("the app through this machine's web port was not told")
	}
	if get("192.168.1.20:41000", nil) {
		t.Fatal("the LAN was told")
	}
	if get("127.0.0.1:41000", map[string]string{"X-Forwarded-For": "192.168.1.20"}) {
		t.Fatal("the LAN through the web port was told")
	}
	if get("192.168.1.20:41000", map[string]string{"X-Real-IP": "127.0.0.1"}) {
		t.Fatal("a LAN client forging X-Real-IP was told")
	}
	if get("127.0.0.1:41000", map[string]string{mw.ViaHeader: "relay"}) {
		t.Fatal("the relay was told")
	}
	if !get("192.168.1.20:41000", map[string]string{LocalTokenHeader: "t0k"}) {
		t.Fatal("the supervisor was not told")
	}
}
