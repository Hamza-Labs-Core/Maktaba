//go:build !windows

package supervisor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
)

// The bundled Ollama runs as the ollama role: `ollama serve` on its loopback
// port, its models in the app's models/ollama, its output in the logs folder,
// probed at /api/version.
func TestOllamaRole(t *testing.T) {
	dir := t.TempDir()
	s := llmserver.Server{
		Bin:    "/payload/ollama/ollama",
		Port:   11533,
		Models: filepath.Join(dir, "state", "models", "ollama"),
		Logs:   filepath.Join(dir, "logs", "ollama.log"),
	}
	// a large old log moves aside
	if err := os.MkdirAll(filepath.Dir(s.Logs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Logs, make([]byte, ollamaLogMax+1), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := BuildOllama(s)
	if err != nil {
		t.Fatal(err)
	}
	if p.Role != RoleOllama || p.First || p.OnDemand || p.Probe != "http://127.0.0.1:11533/api/version" {
		t.Fatalf("process = %+v", p)
	}
	if got := strings.Join(p.Cmd.Args, " "); got != "/payload/ollama/ollama serve" {
		t.Fatalf("args = %s", got)
	}
	env := envMap(p.Cmd.Env)
	if env["OLLAMA_HOST"] != "127.0.0.1:11533" || env["OLLAMA_MODELS"] != s.Models || env["OLLAMA_NOPRUNE"] != "1" {
		t.Fatalf("env = %v", env)
	}
	if info, err := os.Stat(s.Models); err != nil || !info.IsDir() {
		t.Fatalf("models folder not made: %v", err)
	}
	f, ok := p.Cmd.Stdout.(*os.File)
	if !ok || f.Name() != s.Logs || p.Cmd.Stderr != p.Cmd.Stdout {
		t.Fatalf("output goes to %v / %v, want %s", p.Cmd.Stdout, p.Cmd.Stderr, s.Logs)
	}
	_ = f.Close()
	if info, err := os.Stat(s.Logs + ".1"); err != nil || info.Size() != ollamaLogMax+1 {
		t.Fatalf("old log not moved aside: %v", err)
	}
}

// Every role gets the model server's URL as MAKTABA_OLLAMA_URL: the bundled
// one serve claimed, else [llm] url, else nothing (the pipeline's default). An
// operator's own wins.
func TestLLMURLHandOff(t *testing.T) {
	t.Setenv("MAKTABA_OLLAMA_URL", "")
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	for _, r := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		if v, ok := envMap(childEnvOK(r, cfg))["MAKTABA_OLLAMA_URL"]; ok {
			t.Fatalf("%s: MAKTABA_OLLAMA_URL = %q without a model server", r, v)
		}
	}
	cfg.LLM.URL = "http://192.168.1.30:11434/"
	if got := envMap(childEnvOK(RolePipeline, cfg))["MAKTABA_OLLAMA_URL"]; got != "http://192.168.1.30:11434" {
		t.Fatalf("[llm] url: %q", got)
	}
	cfg.LLM.URL = ""
	cfg.LLM.RuntimeURL = "http://127.0.0.1:11533"
	for _, r := range []Role{RoleAPI, RoleStreaming, RolePipeline, RoleTVSlot} {
		if got := envMap(childEnvOK(r, cfg))["MAKTABA_OLLAMA_URL"]; got != "http://127.0.0.1:11533" {
			t.Fatalf("%s: MAKTABA_OLLAMA_URL = %q", r, got)
		}
	}
	t.Setenv("MAKTABA_OLLAMA_URL", "http://own:11434")
	if _, ok := envMap(childEnvOK(RolePipeline, cfg))["MAKTABA_OLLAMA_URL"]; ok {
		t.Fatal("the operator's own MAKTABA_OLLAMA_URL must be kept (not set again)")
	}
}

// GET /llm and the snapshot's llm field: the model server's state for the menu
// bar and the dependency watcher.
func TestLLMEndpoint(t *testing.T) {
	sup := New(nil, testPolicy(), WithLog(&syncBuffer{}))
	srv := httptest.NewServer(sup.Handler())
	defer srv.Close()
	if resp, err := http.Get(srv.URL + "/llm"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no model server: %v %v", resp, err)
	}
	if sup.Snapshot().LLM != nil {
		t.Fatal("snapshot llm without a model server")
	}

	st := llmserver.Status{URL: "http://127.0.0.1:11533", Bundled: true, Up: true,
		Models: []llmserver.ModelStatus{{Name: "qwen2.5:7b", State: llmserver.ModelDownloading, Done: 10, Total: 100}}}
	var mu sync.Mutex
	sup = New(nil, testPolicy(), WithLog(&syncBuffer{}), WithLLM(func() llmserver.Status {
		mu.Lock()
		defer mu.Unlock()
		return st
	}))
	srv2 := httptest.NewServer(sup.Handler())
	defer srv2.Close()
	get := func(path string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Get(srv2.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}
	code, body := get("/llm")
	if code != http.StatusServiceUnavailable || body["url"] != "http://127.0.0.1:11533" {
		t.Fatalf("downloading: %d %v", code, body)
	}
	_, snap := get("/processes")
	llm, _ := snap["llm"].(map[string]any)
	models, _ := llm["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["state"] != "downloading" {
		t.Fatalf("snapshot llm = %v", snap["llm"])
	}
	mu.Lock()
	st.Ready = true
	st.Models = []llmserver.ModelStatus{{Name: "qwen2.5:7b", State: llmserver.ModelReady}}
	mu.Unlock()
	if code, _ := get("/llm"); code != http.StatusOK {
		t.Fatalf("ready: %d", code)
	}
}
