package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// story 59.10: the container is healthy while it waits for setup, and once set up only when
// the web app's health route answers 200 on server.toml's listen.
func TestCheckHealth(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "server.toml")

	if msg, ok := checkHealth(context.Background(), cfg, http.DefaultClient); !ok || !strings.Contains(msg, "waiting for setup") {
		t.Fatalf("not set up: %q %v", msg, ok)
	}

	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/system/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err := os.WriteFile(cfg, []byte("[server]\nlisten = \"0.0.0.0:"+port+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, ok := checkHealth(context.Background(), cfg, srv.Client()); !ok {
		t.Fatalf("answering: %q", msg)
	}
	status = http.StatusServiceUnavailable
	if msg, ok := checkHealth(context.Background(), cfg, srv.Client()); ok || !strings.Contains(msg, "503") {
		t.Fatalf("503: %q %v", msg, ok)
	}
	srv.Close()
	if msg, ok := checkHealth(context.Background(), cfg, http.DefaultClient); ok {
		t.Fatalf("nothing listening: %q", msg)
	}

	if err := os.WriteFile(cfg, []byte("[server]\nlisten = \"nope\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, ok := checkHealth(context.Background(), cfg, http.DefaultClient); ok || !strings.Contains(msg, "no port") {
		t.Fatalf("no port: %q %v", msg, ok)
	}
	if err := os.WriteFile(cfg, []byte("[server\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := checkHealth(context.Background(), cfg, http.DefaultClient); ok {
		t.Fatal("a broken server.toml is healthy")
	}
}

// story 59.10: a TrueNAS container not set up says how to run setup from the app's Shell;
// any other says the compose command.
func TestSetupHint(t *testing.T) {
	if h := setupHint("truenas"); !strings.Contains(h, "Shell") || !strings.Contains(h, "maktaba-server container setup") {
		t.Fatalf("truenas: %q", h)
	}
	if h := setupHint(""); !strings.Contains(h, "docker compose run --rm maktaba setup") {
		t.Fatalf("docker: %q", h)
	}
}
