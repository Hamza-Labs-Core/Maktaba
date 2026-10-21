package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
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
	if code, ev := setupStep(t, "nope", "{}"); code != 2 || ev["type"] != "error" || ev["code"] != "unknown_step" {
		t.Fatalf("unknown step = %d %v", code, ev)
	}
}

// The llm step: the language model the LLM stages use, downloaded with byte
// progress; the result says what is ready and the disk space it took.
func TestSetupJSONLLM(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	t.Setenv("MAKTABA_LOG_DIR", filepath.Join(dir, "logs"))
	if err := os.WriteFile(filepath.Join(dir, "server.toml"), []byte("[llm]\nmodels = [\"qwen2.5:7b\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func(old func(context.Context, llmserver.SetupOptions) (llmserver.Result, error)) { llmSetup = old }(llmSetup)
	var got llmserver.SetupOptions
	var fail error
	llmSetup = func(_ context.Context, o llmserver.SetupOptions) (llmserver.Result, error) {
		got = o
		o.Progress(llmserver.Event{Stage: "check", Message: "The language model needs 4.7 GB of disk space (812.3 GB free)"})
		o.Progress(llmserver.Event{Stage: "download", Message: "Downloading qwen2.5:7b", Done: 1 << 30, Total: 4683076771})
		if fail != nil {
			return llmserver.Result{}, fail
		}
		return llmserver.Result{URL: "http://127.0.0.1:11533", Bundled: true, DiskNeeded: 4683076771, DiskFree: 812300000000,
			Models: []llmserver.ResultModel{{Name: "qwen2.5:7b", Ready: true, Downloaded: true, SizeBytes: 4683076771}}}, nil
	}

	var out bytes.Buffer
	if code := runSetupJSON("llm", strings.NewReader(`{"models":["llama3.2:3b"],"check_only":false}`), &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	var prog map[string]any
	_ = json.Unmarshal([]byte(lines[1]), &prog)
	if prog["type"] != "progress" || prog["stage"] != "download" || prog["done"] != float64(1<<30) || prog["total"] != float64(4683076771) {
		t.Fatalf("progress = %v", prog)
	}
	var res map[string]any
	_ = json.Unmarshal([]byte(lines[2]), &res)
	data, _ := res["data"].(map[string]any)
	models, _ := data["models"].([]any)
	if res["type"] != "result" || data["url"] != "http://127.0.0.1:11533" || data["disk_needed_bytes"] != float64(4683076771) ||
		len(models) != 1 || models[0].(map[string]any)["ready"] != true {
		t.Fatalf("result = %v", res)
	}
	if len(got.Models) != 1 || got.Models[0] != "llama3.2:3b" || got.CheckOnly || got.SupervisorURL != "http://127.0.0.1:9103" ||
		len(got.Config.LLM.Models) != 1 {
		t.Fatalf("options = %+v", got)
	}

	// the errors the app knows
	for err, code := range map[error]string{
		llmserver.ErrUnavailable:                        "llm_unavailable",
		fmt.Errorf("%w: 4.7 GB", llmserver.ErrDiskFull): "disk_full",
		errors.New("pull qwen2.5:7b: connection reset"): "llm_failed",
	} {
		fail = err
		if c, ev := setupStep(t, "llm", `{}`); c != 1 || ev["type"] != "error" || ev["code"] != code {
			t.Fatalf("%v: %d %v", err, c, ev)
		}
	}
	if c, ev := setupStep(t, "llm", `{"models":"qwen"}`); c != 2 || ev["field"] != "models" {
		t.Fatalf("bad models: %d %v", c, ev)
	}
}
