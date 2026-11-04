package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Story 51.04: with the reconciler, a machine whose server.toml leaves the model server
// out (llm off, or no ollama here: a Core Mac) still builds it dormant, its program
// looked up again before each start; without the reconciler it builds none.
func TestTheModelServerIsBuiltDormant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("MAKTABA_CONFIG", filepath.Join(home, "server.toml"))
	t.Setenv("MAKTABA_LOG_DIR", filepath.Join(home, "logs"))
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	t.Setenv("MAKTABA_OLLAMA_BIN", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Defaults()
	cfg.LLM.Port = 47000 + os.Getpid()%1000
	if p, _, err := serveLLM(ctx, &cfg, true, false); err != nil || p != nil {
		t.Fatalf("no reconciler, no ollama: %+v %v", p, err)
	}
	c := cfg
	p, opt, err := serveLLM(ctx, &c, true, true)
	if err != nil || p == nil || !p.Dormant || p.Present == nil || p.Path == nil || opt == nil {
		t.Fatalf("dormant model server: %+v %v", p, err)
	}
	if p.Present() || p.Path() != "" {
		t.Fatal("no ollama on this machine, yet it is present")
	}
	if c.LLM.RuntimeURL == "" {
		t.Fatal("the roles are not told the model server's address")
	}
	// [llm] url: a server elsewhere, none built
	c = cfg
	c.LLM.URL = "http://192.168.1.30:11434"
	if p, _, _ := serveLLM(ctx, &c, true, true); p != nil {
		t.Fatalf("[llm] url, yet a server is built: %+v", p)
	}
}

// [llm] url is logged without a login or a query that may carry a key.
func TestLLMLogURLDropsCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:11434":                        "http://127.0.0.1:11434",
		"https://bob:s3cret@llm.example.net:8443/v1":    "https://…@llm.example.net:8443/v1",
		"https://llm.example.net/v1?api_key=s3cret&x=1": "https://llm.example.net/v1?…",
	} {
		got := llmLogURL(in)
		if got != want || strings.Contains(got, "s3cret") {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}
