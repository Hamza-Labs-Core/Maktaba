package llmserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// fakeOllama is an Ollama API: /api/version, /api/tags, a streamed /api/pull
// (failing when failPull) and the registry's manifests.
type fakeOllama struct {
	mu       sync.Mutex
	have     map[string]bool
	pulls    []string
	failPull string
	srv      *httptest.Server
}

func newFakeOllama(t *testing.T, have ...string) *fakeOllama {
	t.Helper()
	f := &fakeOllama{have: map[string]bool{}}
	for _, h := range have {
		f.have[h] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.40.2"}`))
	})
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var models []map[string]string
		for n := range f.have {
			models = append(models, map[string]string{"name": n, "model": n})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	})
	mux.HandleFunc("/api/pull", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.pulls = append(f.pulls, req.Model)
		fail := f.failPull == req.Model
		f.mu.Unlock()
		lines := []string{
			`{"status":"pulling manifest"}`,
			`{"status":"pulling a","digest":"sha256:a","total":1000,"completed":0}`,
			`{"status":"pulling a","digest":"sha256:a","total":1000,"completed":600}`,
			`{"status":"pulling b","digest":"sha256:b","total":24,"completed":24}`,
			`{"status":"pulling a","digest":"sha256:a","total":1000,"completed":1000}`,
			`{"status":"verifying sha256 digest"}`,
			`{"status":"writing manifest"}`,
			`{"status":"success"}`,
		}
		if fail {
			lines = append(lines[:3], `{"error":"max retries exceeded: connection reset"}`)
		}
		for _, l := range lines {
			_, _ = fmt.Fprintln(w, l)
		}
		if !fail {
			f.mu.Lock()
			f.have[Canonical(req.Model)] = true
			f.mu.Unlock()
		}
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/library/qwen2.5/manifests/7b" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"config":{"size":500},"layers":[{"size":4683075271},{"size":1000}]}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOllama) pulled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.pulls...)
}

// isolate points the config, state and logs at temp dirs and clears the
// variables Resolve reads.
func isolate(t *testing.T) config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("MAKTABA_CONFIG", filepath.Join(home, "server.toml"))
	t.Setenv("MAKTABA_LOG_DIR", filepath.Join(home, "logs"))
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	t.Setenv("MAKTABA_OLLAMA_BIN", "")
	return config.Defaults()
}

// payloadWithOllama lays out <payload>/bin/maktaba-server and <payload>/ollama/ollama.
func payloadWithOllama(t *testing.T) (payload, exe string) {
	t.Helper()
	payload, err := filepath.EvalSymlinks(t.TempDir()) // /var is /private/var on macOS
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"bin/maktaba-server", "ollama/ollama"} {
		p := filepath.Join(payload, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return payload, filepath.Join(payload, "bin", "maktaba-server")
}

func TestResolve(t *testing.T) {
	cfg := isolate(t)
	payload, exe := payloadWithOllama(t)

	// beside the running maktaba-server, as in Maktaba.app
	s, ok, err := Resolve(cfg, exe)
	if err != nil || !ok {
		t.Fatalf("Resolve = %v, %v", ok, err)
	}
	if s.Bin != filepath.Join(payload, "ollama", "ollama") || s.Port != DefaultPort || s.PortSet {
		t.Fatalf("server = %+v", s)
	}
	if s.Models != filepath.Join(cfg.StateDir(), "models", "ollama") || s.Logs != filepath.Join(os.Getenv("MAKTABA_LOG_DIR"), "ollama.log") {
		t.Fatalf("paths = %+v", s)
	}
	if !s.Pull || len(s.Want) != 1 || s.Want[0] != DefaultModel {
		t.Fatalf("models = %+v", s)
	}
	env := strings.Join(s.Env(), "\n")
	for _, want := range []string{"OLLAMA_HOST=127.0.0.1:11533", "OLLAMA_MODELS=" + s.Models, "OLLAMA_NOPRUNE=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s:\n%s", want, env)
		}
	}
	if s.URL() != "http://127.0.0.1:11533" {
		t.Errorf("URL = %s", s.URL())
	}

	// MAKTABA_APP_BUNDLE finds it whatever runs
	t.Setenv("MAKTABA_APP_BUNDLE", payload)
	if s, ok, _ := Resolve(cfg, "/elsewhere/maktaba-server"); !ok || s.Bin != filepath.Join(payload, "ollama", "ollama") {
		t.Fatalf("via MAKTABA_APP_BUNDLE: %+v %v", s, ok)
	}
	t.Setenv("MAKTABA_APP_BUNDLE", "")

	// a set port, models and pull = off
	c := cfg
	c.LLM.Port, c.LLM.Models, c.LLM.Pull = 12000, []string{" llama3.2:3b ", "qwen2.5:7b", "llama3.2:3b", ""}, "off"
	s, _, _ = Resolve(c, exe)
	if s.Port != 12000 || !s.PortSet || s.Pull || strings.Join(s.Want, ",") != "llama3.2:3b,qwen2.5:7b" {
		t.Fatalf("configured = %+v", s)
	}

	// off, or a server elsewhere: none
	c = cfg
	c.LLM.Enabled = "off"
	if _, ok, _ := Resolve(c, exe); ok {
		t.Error("enabled = off still resolved")
	}
	c = cfg
	c.LLM.URL = "http://192.168.1.30:11434/"
	if _, ok, _ := Resolve(c, exe); ok || ExternalURL(c) != "http://192.168.1.30:11434" {
		t.Error("[llm] url must replace the bundled server")
	}

	// no binary: none under auto, an error under on; never PATH
	if _, ok, err := Resolve(cfg, "/nowhere/bin/maktaba-server"); ok || err != nil {
		t.Errorf("auto without a binary = %v, %v", ok, err)
	}
	c = cfg
	c.LLM.Enabled = "on"
	if _, _, err := Resolve(c, "/nowhere/bin/maktaba-server"); err == nil {
		t.Error("enabled = on without a binary must fail")
	}
	// MAKTABA_OLLAMA_BIN wins; a wrong one is not silently replaced
	t.Setenv("MAKTABA_OLLAMA_BIN", filepath.Join(payload, "ollama", "ollama"))
	if s, ok, _ := Resolve(cfg, "/nowhere/bin/maktaba-server"); !ok || s.Bin != filepath.Join(payload, "ollama", "ollama") {
		t.Errorf("MAKTABA_OLLAMA_BIN: %+v %v", s, ok)
	}
	t.Setenv("MAKTABA_OLLAMA_BIN", "/missing/ollama")
	if _, ok, _ := Resolve(cfg, exe); ok {
		t.Error("a missing MAKTABA_OLLAMA_BIN fell back to the payload")
	}
}

func TestClaim(t *testing.T) {
	s := Server{Port: DefaultPort}
	if err := s.Claim(func(string) bool { return true }); err != nil || s.Port != DefaultPort {
		t.Fatalf("free default: %d, %v", s.Port, err)
	}
	// another program holds it (a user's own server): a free port instead
	var asked string
	if err := s.Claim(func(a string) bool { asked = a; return false }); err != nil {
		t.Fatal(err)
	}
	if asked != "127.0.0.1:11533" || s.Port == DefaultPort || s.Port <= 0 {
		t.Fatalf("taken default moved to %d (asked %s)", s.Port, asked)
	}
	// a port set in server.toml never moves
	s = Server{Port: 12000, PortSet: true}
	if err := s.Claim(func(string) bool { return false }); err != nil || s.Port != 12000 {
		t.Fatalf("set port: %d, %v", s.Port, err)
	}
	// the real check
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if CanBind(ln.Addr().String()) {
		t.Error("CanBind on a held port")
	}
}

func TestPullProgressAndErrors(t *testing.T) {
	f := newFakeOllama(t)
	c := NewClient(f.srv.URL + "/")
	var last Progress
	n := 0
	if err := c.Pull(context.Background(), "qwen2.5:7b", func(p Progress) { last = p; n++ }); err != nil {
		t.Fatal(err)
	}
	if last.Done != 1024 || last.Total != 1024 || last.Status != "success" || n != 8 {
		t.Fatalf("last progress %+v after %d", last, n)
	}
	have, err := c.Have(context.Background())
	if err != nil || !have["qwen2.5:7b"] {
		t.Fatalf("have = %v, %v", have, err)
	}
	f.mu.Lock()
	f.failPull = "llama3.2"
	f.mu.Unlock()
	err = c.Pull(context.Background(), "llama3.2", nil)
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("failed pull = %v", err)
	}
	// nothing answers
	if _, err := NewClient("http://127.0.0.1:1").Version(context.Background()); err == nil {
		t.Error("version of nothing")
	}
}

func TestSizeAndNames(t *testing.T) {
	f := newFakeOllama(t)
	c := NewClient(f.srv.URL)
	c.Registry = f.srv.URL
	n, err := c.Size(context.Background(), "qwen2.5:7b")
	if err != nil || n != 4683076771 {
		t.Fatalf("size = %d, %v", n, err)
	}
	if _, err := c.Size(context.Background(), "nope:1b"); err == nil {
		t.Error("size of an unknown model")
	}
	c.Registry = ""
	for in, want := range map[string]string{
		"qwen2.5:7b":           DefaultRegistry + "/v2/library/qwen2.5/manifests/7b",
		"qwen2.5":              DefaultRegistry + "/v2/library/qwen2.5/manifests/latest",
		"someone/model:q4":     DefaultRegistry + "/v2/someone/model/manifests/q4",
		"hf.co/org/repo:Q4_KM": "https://hf.co/v2/org/repo/manifests/Q4_KM",
	} {
		if got, err := c.manifestURL(in); err != nil || got != want {
			t.Errorf("manifestURL(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
	if Canonical("hf.co/org/repo") != "hf.co/org/repo:latest" || Canonical("a:b") != "a:b" {
		t.Error("Canonical")
	}
	if HumanBytes(4683076771) != "4.7 GB" || HumanBytes(999) != "999 B" {
		t.Errorf("HumanBytes = %s", HumanBytes(4683076771))
	}
}

func TestManager(t *testing.T) {
	ctx := context.Background()
	// down: unknown, not ready
	m := NewManager("http://127.0.0.1:1", []string{"qwen2.5:7b"}, true, true, nil)
	if m.Check(ctx) {
		t.Fatal("ready while down")
	}
	st := m.Status()
	if st.Up || st.Ready || st.Models[0].State != ModelUnknown || st.Error == "" || !st.Bundled {
		t.Fatalf("down status = %+v", st)
	}

	// up, missing: downloads it, then ready
	f := newFakeOllama(t, "llama3.2:latest")
	m = NewManager(f.srv.URL, []string{"qwen2.5:7b", "llama3.2"}, true, true, nil)
	if !m.Check(ctx) {
		t.Fatalf("not ready after the pull: %+v", m.Status())
	}
	st = m.Status()
	if !st.Up || !st.Ready || st.Version != "0.40.2" || st.Models[0].State != ModelReady || st.Models[1].State != ModelReady {
		t.Fatalf("status = %+v", st)
	}
	if p := f.pulled(); len(p) != 1 || p[0] != "qwen2.5:7b" {
		t.Fatalf("pulled %v", p)
	}

	// pull off: missing, nothing downloaded
	f2 := newFakeOllama(t)
	m = NewManager(f2.srv.URL, []string{"qwen2.5:7b"}, false, false, nil)
	if m.Check(ctx) || m.Status().Models[0].State != ModelMissing || len(f2.pulled()) != 0 {
		t.Fatalf("pull off: %+v %v", m.Status(), f2.pulled())
	}

	// a failed pull: failed with its error, retried on the next check
	f3 := newFakeOllama(t)
	f3.failPull = "qwen2.5:7b"
	m = NewManager(f3.srv.URL, []string{"qwen2.5:7b"}, true, true, nil)
	if m.Check(ctx) {
		t.Fatal("ready after a failed pull")
	}
	if ms := m.Status().Models[0]; ms.State != ModelFailed || !strings.Contains(ms.Error, "connection reset") {
		t.Fatalf("failed = %+v", ms)
	}
	f3.mu.Lock()
	f3.failPull = ""
	f3.mu.Unlock()
	if !m.Check(ctx) || len(f3.pulled()) != 2 {
		t.Fatalf("retry: %+v %v", m.Status(), f3.pulled())
	}
}

func TestSetup(t *testing.T) {
	cfg := isolate(t)
	ctx := context.Background()
	f := newFakeOllama(t)

	// a server elsewhere ([llm] url): check only reports the space, downloads nothing
	cfg.LLM.URL = f.srv.URL
	var events []Event
	res, err := Setup(ctx, SetupOptions{Config: cfg, CheckOnly: true, Registry: f.srv.URL,
		Progress: func(e Event) { events = append(events, e) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != f.srv.URL || res.Bundled || res.DiskNeeded != 4683076771 || res.DiskFree != -1 ||
		len(res.Models) != 1 || res.Models[0].Ready || res.Models[0].SizeBytes != 4683076771 {
		t.Fatalf("check = %+v", res)
	}
	if len(events) != 1 || events[0].Stage != "check" || !strings.Contains(events[0].Message, "4.7 GB") {
		t.Fatalf("events = %+v", events)
	}
	if len(f.pulled()) != 0 {
		t.Fatal("check only pulled")
	}

	// the running supervisor's server (bundled): downloads with byte progress
	_, exe := payloadWithOllama(t)
	var running atomic.Pointer[fakeOllama]
	running.Store(f)
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/llm" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable) // up, not ready yet
		_ = json.NewEncoder(w).Encode(Status{URL: running.Load().srv.URL, Up: true, Bundled: true})
	}))
	defer sup.Close()
	cfg.LLM.URL = ""
	events = nil
	res, err = Setup(ctx, SetupOptions{Config: cfg, Exe: exe, SupervisorURL: sup.URL, Registry: f.srv.URL,
		Progress: func(e Event) { events = append(events, e) }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Bundled || !res.Models[0].Ready || !res.Models[0].Downloaded || res.DiskFree <= 0 {
		t.Fatalf("setup = %+v", res)
	}
	// byte progress against the manifest's size, each state once, ending at all of it
	var dl []Event
	for _, e := range events {
		if e.Stage == "download" {
			dl = append(dl, e)
		}
	}
	for i := 1; i < len(dl); i++ {
		if dl[i] == dl[i-1] {
			t.Fatalf("repeated event %+v", dl[i])
		}
	}
	if n := len(dl); n != 5 || dl[n-2].Done != 1024 || dl[n-1].Done != 4683076771 || dl[n-1].Total != 4683076771 {
		t.Fatalf("download events = %+v", dl)
	}
	// again: nothing to do
	res, err = Setup(ctx, SetupOptions{Config: cfg, Exe: exe, SupervisorURL: sup.URL, Registry: f.srv.URL})
	if err != nil || !res.Models[0].Ready || res.Models[0].Downloaded || res.DiskNeeded != 0 || len(f.pulled()) != 1 {
		t.Fatalf("again = %+v, %v (pulls %v)", res, err, f.pulled())
	}

	// not enough space: nothing downloaded
	empty := newFakeOllama(t)
	running.Store(empty)
	defer func(old func(string) int64) { freeBytes = old }(freeBytes)
	freeBytes = func(string) int64 { return 1 << 20 }
	_, err = Setup(ctx, SetupOptions{Config: cfg, Exe: exe, SupervisorURL: sup.URL, Registry: empty.srv.URL})
	if !errors.Is(err, ErrDiskFull) || len(empty.pulled()) != 0 {
		t.Fatalf("no space: %v (pulls %v)", err, empty.pulled())
	}

	// nothing to run and nowhere to go
	if _, err := Setup(ctx, SetupOptions{Config: cfg, Exe: "/nowhere/bin/maktaba-server"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no server: %v", err)
	}
}
