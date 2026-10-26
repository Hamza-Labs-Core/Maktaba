package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// configAdminFixture is a /config/ handler over a temp server.toml (never the real
// one), a free-port probe that refuses :7000, and serve started with the file as it
// was written.
func configAdminFixture(t *testing.T, body string) (*configAdmin, http.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_CONFIG", path)
	t.Setenv(layout.EnvLauncher, "")
	cfg, _, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := newConfigAdmin(cfg, "tok", "")
	c.checks.PortFree = func(addr string) error {
		if strings.HasSuffix(addr, ":7000") {
			return errors.New("in use")
		}
		return nil
	}
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	return c, c.adminHandler(base), path
}

func configCall(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestConfigAdminRefusesWithoutTheToken(t *testing.T) {
	c, h, _ := configAdminFixture(t, "")
	for _, r := range []struct{ method, path string }{
		{"GET", "/config/settings"}, {"PUT", "/config/settings"}, {"POST", "/config/restart"}, {"GET", "/config/other"},
	} {
		for _, tok := range []string{"", "wrong"} {
			if rec := configCall(h, r.method, r.path, tok, `{}`); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s with %q: %d", r.method, r.path, tok, rec.Code)
			}
		}
	}
	// no per-boot token at all (it could not be written): nothing gets in
	c.token = ""
	if rec := configCall(c.adminHandler(http.NotFoundHandler()), "GET", "/config/settings", "", ""); rec.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rec.Code)
	}
	// the other admin routes are not this handler's
	if rec := configCall(h, "GET", "/processes", "", ""); rec.Code != http.StatusTeapot {
		t.Errorf("base: %d", rec.Code)
	}
}

func TestConfigSettingsGetAndPut(t *testing.T) {
	share := t.TempDir()
	_, h, path := configAdminFixture(t, `[server]
listen = "0.0.0.0:8080"
web_listen = "0.0.0.0:8088"
stream_addr = ":8081"
unknown_knob = 1

[media]
path_map = "/Volumes/O/=/old/"

[cloud]
claim_code = "keep-me"
`)
	rec := configCall(h, "GET", "/config/settings", "tok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	var doc map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	for _, k := range []string{"file", "saved", "running", "restart_needed", "can_restart"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("GET lacks %q: %s", k, rec.Body)
		}
	}
	if doc["file"] != path || doc["restart_needed"] != false || doc["can_restart"] != false {
		t.Errorf("doc = %s", rec.Body)
	}
	saved := doc["saved"].(map[string]any)
	if pm := saved["media"].(map[string]any)["path_map"].([]any); len(pm) != 1 ||
		pm[0].(map[string]any)["db"] != "/Volumes/O/" || pm[0].(map[string]any)["local"] != "/old/" {
		t.Errorf("saved path_map = %v", pm)
	}
	if saved["server"].(map[string]any)["mdns"] != true {
		t.Errorf("mdns = %v", saved["server"])
	}

	// a bad value: 422 naming the field, the file untouched
	before, _ := os.ReadFile(path)
	rec = configCall(h, "PUT", "/config/settings", "tok",
		`{"media":{"path_map":[{"db":"/Volumes/O/","local":"/no/such/folder"}]},"server":{"listen":":7000"}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad put: %d %s", rec.Code, rec.Body)
	}
	var bad struct {
		Errors []config.FieldError `json:"errors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &bad)
	fields := map[string]bool{}
	for _, e := range bad.Errors {
		fields[e.Field] = true
	}
	if !fields["media.path_map[0].local"] || !fields["server.listen"] || len(fields) != 2 {
		t.Errorf("errors = %+v", bad.Errors)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("a refused write changed the file")
	}

	// a good one: only the keys sent change
	rec = configCall(h, "PUT", "/config/settings", "tok",
		`{"media":{"path_map":[{"db":"/Volumes/O/","local":"`+share+`"}]},"server":{"mdns":false}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	doc = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc["restart_needed"] != true {
		t.Errorf("restart_needed after a change: %s", rec.Body)
	}
	if doc["running"].(map[string]any)["server"].(map[string]any)["mdns"] != true {
		t.Errorf("running changed: %s", rec.Body)
	}
	var raw map[string]map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["media"]["path_map"] != "/Volumes/O/="+share || raw["server"]["mdns"] != "off" {
		t.Errorf("file = %v", raw)
	}
	if raw["server"]["listen"] != "0.0.0.0:8080" || raw["server"]["unknown_knob"] != int64(1) ||
		raw["cloud"]["claim_code"] != "keep-me" {
		t.Errorf("other keys: %v", raw)
	}
	if _, ok := raw["database"]; ok {
		t.Errorf("database url written: %v", raw["database"])
	}

	// a saved value that no longer checks out (the folder gone) does not block another change
	if err := os.Remove(share); err != nil {
		t.Fatal(err)
	}
	if rec := configCall(h, "PUT", "/config/settings", "tok", `{"server":{"mdns":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("mdns back: %d %s", rec.Code, rec.Body)
	}
	if rec := configCall(h, "PUT", "/config/settings", "tok", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", rec.Code)
	}
	if rec := configCall(h, "DELETE", "/config/settings", "tok", ``); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("delete: %d", rec.Code)
	}
}

func TestConfigRestart(t *testing.T) {
	// without the launcher: 409, nothing written
	_, h, _ := configAdminFixture(t, "")
	if rec := configCall(h, "POST", "/config/restart", "tok", ""); rec.Code != http.StatusConflict {
		t.Fatalf("no launcher: %d %s", rec.Code, rec.Body)
	}

	// under the launcher: the restart request watchLauncher takes
	root := t.TempDir()
	l := layout.New(root)
	release := l.ReleaseDir("1.2.3")
	if err := os.MkdirAll(release, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(layout.EnvLauncher, "1")
	c := newConfigAdmin(config.Defaults(), "tok", release)
	h = c.adminHandler(http.NotFoundHandler())
	rec := configCall(h, "GET", "/config/settings", "tok", "")
	if !strings.Contains(rec.Body.String(), `"can_restart":true`) {
		t.Errorf("can_restart: %s", rec.Body)
	}
	if rec := configCall(h, "POST", "/config/restart", "tok", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("restart: %d %s", rec.Code, rec.Body)
	}
	if !l.TakeRestartRequest() {
		t.Fatal("no restart request written")
	}
}
