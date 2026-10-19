package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func TestSwapBinaryKeepsBakAndRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "maktaba-server")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapBinary(target, []byte("NEW")); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "NEW" {
		t.Fatalf("target not swapped: %q", b)
	}
	if b, _ := os.ReadFile(target + ".bak"); string(b) != "OLD" {
		t.Fatalf(".bak not preserved: %q", b)
	}
	if err := rollbackBinary(target); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "OLD" {
		t.Fatalf("rollback did not restore OLD: %q", b)
	}
}

// --- handler gate tests ---

func adminCtx() context.Context {
	return principal.WithPrincipal(context.Background(), &principal.Principal{IsAdmin: true})
}

func availableUpdater(t *testing.T, current string, assets []ghAsset) *Updater {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]ghRelease{
			{TagName: "v9.9.9", HTMLURL: "url", Assets: assets},
		})
	}))
	t.Cleanup(srv.Close)
	return NewUpdater(UpdaterConfig{CurrentVersion: current, APIBaseURL: srv.URL})
}

func TestHandlerNonAdmin403(t *testing.T) {
	su := NewSelfUpdater(availableUpdater(t, "v1.0.0", nil))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/system/update", strings.NewReader(`{"confirm":true}`))
	su.Handler().ServeHTTP(rec, req) // no principal
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandlerMissingConfirm400(t *testing.T) {
	su := NewSelfUpdater(availableUpdater(t, "v1.0.0", nil))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/system/update", strings.NewReader(`{}`)).
		WithContext(adminCtx())
	su.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerAlreadyCurrent409(t *testing.T) {
	su := NewSelfUpdater(availableUpdater(t, "v9.9.9", nil)) // already on latest
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/system/update", strings.NewReader(`{"confirm":true}`)).
		WithContext(adminCtx())
	su.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestHandlerDocker409WithInstructions(t *testing.T) {
	assets := []ghAsset{
		{Name: "maktaba-server-9.9.9-" + PlatformToken() + assetExt(), URL: "a"},
		{Name: "checksums.txt", URL: "c"},
	}
	su := NewSelfUpdater(availableUpdater(t, "v1.0.0", assets))
	su.detect = func(string) installKind { return installDocker }
	su.selfPathFunc = func() (string, error) { return "/usr/bin/maktaba-server", nil }

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/system/update", strings.NewReader(`{"confirm":true}`)).
		WithContext(adminCtx())
	su.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var resp dockerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode docker resp: %v", err)
	}
	if resp.Install != "docker" || !strings.Contains(resp.Instructions, "docker compose pull") {
		t.Fatalf("docker response missing instructions: %+v", resp)
	}
}

func assetExt() string {
	if strings.Contains(PlatformToken(), "windows") {
		return ".zip"
	}
	return ".tar.gz"
}
