package system

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// A headless runtime copy updates itself through `maktaba-server update`, run
// as its own unit; the API answers 202 and replaces nothing.
func TestHandlerRuntimeStartsTheUpdateOnItsOwn(t *testing.T) {
	su := NewSelfUpdater(availableUpdater(t, "v1.0.0", nil))
	su.detect = func(string) installKind { return installRuntime }
	bin := filepath.Join("/home/u/.local/share/maktaba/runtime/v1.0.0_1a2b3c4d", "bin")
	su.selfPathFunc = func() (string, error) { return filepath.Join(bin, "maktaba-api"), nil }
	var server, version string
	su.runRuntime = func(s, v string) (string, []byte, error) {
		server, version = s, v
		return "maktaba-update-1", nil, nil
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/system/update", strings.NewReader(`{"confirm":true}`)).
		WithContext(adminCtx())
	su.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (%s), want 202", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["via"] != "runtime" || resp["unit"] != "maktaba-update-1" || resp["version"] != "v9.9.9" {
		t.Errorf("response %v", resp)
	}
	if server != filepath.Join(bin, "maktaba-server") || version != "v9.9.9" {
		t.Errorf("ran %s update --version %s", server, version)
	}
	if su.InstallName() != "runtime" {
		t.Errorf("install %q", su.InstallName())
	}

	su.runRuntime = func(string, string) (string, []byte, error) {
		return "u", []byte("Failed to connect to bus"), errors.New("exit status 1")
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/system/update", strings.NewReader(`{"confirm":true}`)).
		WithContext(adminCtx())
	su.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "Failed to connect to bus") {
		t.Errorf("a failed start: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRuntimeUpdateArgs(t *testing.T) {
	env := map[string]string{"MAKTABA_GITHUB_TOKEN": "secret", "XDG_DATA_HOME": "/d", "PATH": "/bin"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	got := strings.Join(runtimeUpdateArgs("maktaba-update-7", "/r/bin/maktaba-server", "v2.0.0", lookup), " ")
	want := "--user --collect --quiet --unit=maktaba-update-7 --setenv=MAKTABA_GITHUB_TOKEN --setenv=XDG_DATA_HOME " +
		"-- /r/bin/maktaba-server update --version v2.0.0"
	if got != want {
		t.Errorf("args\n got %s\nwant %s", got, want)
	}
	if strings.Contains(got, "secret") {
		t.Error("a token on the command line")
	}
}

func TestDetectInstallRuntime(t *testing.T) {
	t.Setenv(EnvRuntimeOwner, RuntimeOwnerHeadless)
	if k := detectInstall("/home/u/.local/share/maktaba/runtime/v1_1a2b3c4d/bin/maktaba-api"); k != installRuntime {
		t.Errorf("headless runtime: %v", k)
	}
	t.Setenv(EnvRuntimeOwner, "app")
	if k := detectInstall("/home/u/.local/share/maktaba/runtime/v1_1a2b3c4d/bin/maktaba-api"); k == installRuntime {
		t.Error("an app-owned runtime updates with the app")
	}
}
