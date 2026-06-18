package system

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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

func TestMatchServerAsset(t *testing.T) {
	assets := []Asset{
		{Name: "maktaba-server-1.4.2-linux-amd64.tar.gz"},
		{Name: "maktaba-server-1.4.2-darwin-arm64.tar.gz"},
		{Name: "maktaba-server-1.4.2-windows-amd64.zip"},
		{Name: "checksums.txt"},
	}
	got := matchServerAsset(assets)
	if got == nil {
		t.Fatalf("expected a match for %s", PlatformToken())
	}
	if !strings.Contains(got.Name, PlatformToken()) {
		t.Fatalf("matched wrong asset: %s (token %s)", got.Name, PlatformToken())
	}
}

func TestLookupChecksum(t *testing.T) {
	manifest := "abc123  ./maktaba-server-1.4.2-linux-amd64.tar.gz\n" +
		"def456  ./checksums.txt\n"
	if got := lookupChecksum(manifest, "maktaba-server-1.4.2-linux-amd64.tar.gz"); got != "abc123" {
		t.Fatalf("checksum = %q, want abc123", got)
	}
	if got := lookupChecksum(manifest, "missing.tar.gz"); got != "" {
		t.Fatalf("missing entry should return empty, got %q", got)
	}
}

func TestExtractFromTarGz(t *testing.T) {
	want := []byte("\x7fELF fake binary")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string][]byte{"README.md": []byte("hi"), "maktaba-server": want}
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(body)
	}
	_ = tw.Close()
	_ = gz.Close()

	got, err := extractServerBinary("maktaba-server-1.4.2-linux-amd64.tar.gz", buf.Bytes())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extracted bytes mismatch")
	}
}

func TestExtractFromZip(t *testing.T) {
	want := []byte("MZ fake exe")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("maktaba-server.exe")
	_, _ = w.Write(want)
	_ = zw.Close()

	got, err := extractServerBinary("maktaba-server-1.4.2-windows-amd64.zip", buf.Bytes())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extracted zip bytes mismatch")
	}
}

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

func TestSha256HexAndVerify(t *testing.T) {
	data := []byte("hello")
	// echo -n hello | sha256sum
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := sha256hex(data); got != want {
		t.Fatalf("sha256hex = %s want %s", got, want)
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
