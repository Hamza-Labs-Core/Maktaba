package router_test

// Epic 58 story 58.01, T03: the client floor at every route that hands out credentials, as
// main.go mounts them (MountP9's sign-in and refresh, MountP10's pairing), behind the same
// X-Maktaba-Client middleware and floor gate router.New installs, on a real migrated
// Postgres. A build below the floor gets 426 client_too_old with the floor; a newer build
// passes; a request without the header (the web) passes.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/compat"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

func TestClientFloorAtSignIn(t *testing.T) {
	t.Parallel()
	db, _ := clustertest.ScratchDB(t)
	set := keys.NewSet(time.Hour)
	k, err := keys.Generate(keys.MinBits)
	if err != nil {
		t.Fatal(err)
	}
	set.Replace(k)
	floor := compat.Floor{"tv-web/webos": "1.4.0", "player/android": "2.0.0"}

	r := chi.NewRouter()
	r.Use(mw.Client)
	r.Use(compat.Gate(floor))
	ah := router.MountP9(r, router.P9Deps{DB: db, Keys: set})
	if ah == nil {
		t.Fatal("MountP9 mounted nothing")
	}
	light := argon2id.DefaultParams()
	light.Memory, light.Time = 64, 1
	ah.Users.Params, ah.RefreshTokens.Params = light, light
	router.MountP10(r, router.P10Deps{DB: db, Keys: set, ClientFloor: floor})

	us := users.New(db)
	us.Params = light
	if _, err := us.Create(context.Background(), users.CreateInput{Username: "floor-viewer", Password: "correct horse battery"}); err != nil {
		t.Fatal(err)
	}

	call := func(path, header string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if header != "" {
			req.Header.Set(client.Header, header)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	tooOld := func(path, header string, body any) {
		t.Helper()
		code, out := call(path, header, body)
		if code != http.StatusUpgradeRequired || out["code"] != compat.Code || out["floor"] == nil {
			t.Fatalf("%s from %q = %d %v, want 426 client_too_old with the floor", path, header, code, out)
		}
	}
	login := map[string]string{"username": "floor-viewer", "password": "correct horse battery"}

	// login: the old launcher is refused before its password is checked
	tooOld("/api/auth/login", "tv-web/1.3.9 (webos; tv)", login)
	tooOld("/api/auth/login", "player/1.9.9 (android; phone)", login)
	// a newer build signs in
	code, out := call("/api/auth/login", "tv-web/1.4.0 (webos; tv)", login)
	if code != http.StatusOK || out["refresh_token"] == nil {
		t.Fatalf("login from 1.4.0 = %d %v, want 200 with tokens", code, out)
	}
	refreshToken := out["refresh_token"].(string)
	// a request without the header (the web) is not the floor's
	if code, out := call("/api/auth/login", "", login); code != http.StatusOK {
		t.Fatalf("login without the header = %d %v, want 200", code, out)
	}

	// refresh: a session an old build kept is refused at its next refresh
	tooOld("/api/auth/refresh", "tv-web/1.3.9 (webos; tv)", map[string]string{"refresh_token": refreshToken})
	code, out = call("/api/auth/refresh", "tv-web/1.4.1 (webos; tv)", map[string]string{"refresh_token": refreshToken})
	if code != http.StatusOK || out["access_token"] == nil {
		t.Fatalf("refresh from 1.4.1 = %d %v, want 200", code, out)
	}

	// pairing token: an old TV polling its sign-in code is refused; a newer one reaches the
	// handler (which answers the unknown code itself)
	tooOld("/api/pairing/device/token", "tv-web/1.3.9 (webos; tv)", map[string]string{"device_code": "nope"})
	if code, out := call("/api/pairing/device/token", "tv-web/1.4.0 (webos; tv)", map[string]string{"device_code": "nope"}); code == http.StatusUpgradeRequired {
		t.Fatalf("device token from 1.4.0 = %d %v, want the handler's own answer", code, out)
	}
	if code, out := call("/api/pairing/device/token", "", map[string]string{"device_code": "nope"}); code == http.StatusUpgradeRequired {
		t.Fatalf("device token without the header = %d %v", code, out)
	}
	tooOld("/api/pairing/exchange", "player/1.0.0 (android; phone)", map[string]string{"code": "ABCD-1234"})

	// the discovery answer publishes the same floor, with no sign-in
	req := httptest.NewRequest("GET", "/api/discovery/home", nil)
	req.Header.Set(client.Header, "tv-web/1.3.9 (webos; tv)")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var home map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &home)
	got, _ := home["client_floor"].(map[string]any)
	if rec.Code != http.StatusOK || got["tv-web/webos"] != "1.4.0" || home["api_level"] != float64(compat.Level) {
		t.Fatalf("discovery = %d %v", rec.Code, home)
	}
}
