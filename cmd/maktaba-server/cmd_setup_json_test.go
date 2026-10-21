package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupStep(t *testing.T, step, input string) (int, map[string]any) {
	t.Helper()
	var out bytes.Buffer
	code := runSetupJSON(step, strings.NewReader(input), &out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var ev map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &ev); err != nil {
		t.Fatalf("last line %q: %v", lines[len(lines)-1], err)
	}
	return code, ev
}

func TestSetupJSONStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.toml")
	t.Setenv("MAKTABA_CONFIG", path)
	t.Setenv("MAKTABA_POSTGRES_HOME", "")
	t.Setenv("MAKTABA_APP_BUNDLE", "")

	code, ev := setupStep(t, "status", "{}")
	if code != 0 || ev["type"] != "result" {
		t.Fatalf("status = %d %v", code, ev)
	}
	data := ev["data"].(map[string]any)
	if data["configured"] != false || data["web_url"] != "http://127.0.0.1:8088" || data["supervisor_url"] != "http://127.0.0.1:9103" {
		t.Fatalf("not set up: %v", data)
	}

	// set up (a migrate-from-dev or a finish): server.toml and its database
	if err := os.WriteFile(path, []byte("[server]\nweb_listen = \"0.0.0.0:8090\"\n[database]\nurl = \"postgres://m@h/m\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ev = setupStep(t, "status", "{}")
	data = ev["data"].(map[string]any)
	if data["configured"] != true || data["web_url"] != "http://127.0.0.1:8090" {
		t.Fatalf("set up: %v", data)
	}
}

func TestSetupJSONUsage(t *testing.T) {
	t.Setenv("MAKTABA_CONFIG", filepath.Join(t.TempDir(), "server.toml"))
	if code, ev := setupStep(t, "status", "not json"); code != 2 || ev["code"] != "bad_input" {
		t.Fatalf("bad input = %d %v", code, ev)
	}
	if code, ev := setupStep(t, "plex", "{}"); code != 2 || ev["type"] != "error" || ev["code"] != "unknown_step" {
		t.Fatalf("unknown step = %d %v", code, ev)
	}
}
