package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// The health and version checks send this machine's local token, so a busy home's client
// limits never refuse them (the API's InternalCallers); with no token file they send none.
func TestOwnChecksCarryLocalToken(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.Path+"="+r.Header.Get("X-Maktaba-Local-Token"))
		mu.Unlock()
		if r.URL.Path == "/api/system/version" {
			_, _ = w.Write([]byte(`{"version":"0.3.1"}`))
		}
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	cfg := filepath.Join(dir, "server.toml")
	data := filepath.Join(dir, "data")
	toml := "[server]\nlisten = \"0.0.0.0:" + port + "\"\ndata_dir = \"" + data + "\"\n"
	if err := os.WriteFile(cfg, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}

	// no token file yet: no header
	checkHealth(context.Background(), cfg, srv.Client())
	if v := serverVersion(context.Background(), cfg); v != "0.3.1" {
		t.Fatalf("version %q", v)
	}
	if err := os.MkdirAll(filepath.Join(data, "supervisor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "supervisor", "local.token"), []byte("tok-abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkHealth(context.Background(), cfg, srv.Client())
	serverVersion(context.Background(), cfg)
	want := []string{"/api/system/health=", "/api/system/version=", "/api/system/health=tok-abc", "/api/system/version=tok-abc"}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A request with the token follows no redirect: the token must not go to where a 30x points.
func TestOwnChecksFollowNoRedirect(t *testing.T) {
	var leaked string
	away := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Maktaba-Local-Token")
	}))
	defer away.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, away.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	dir := t.TempDir()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	cfg := filepath.Join(dir, "server.toml")
	data := filepath.Join(dir, "data")
	if err := os.WriteFile(cfg, []byte("[server]\nlisten = \"0.0.0.0:"+port+"\"\ndata_dir = \""+data+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "supervisor"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "supervisor", "local.token"), []byte("tok-abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, ok := checkHealth(context.Background(), cfg, http.DefaultClient); ok || !strings.Contains(msg, "302") {
		t.Fatalf("a redirect: %q %v", msg, ok)
	}
	if v := serverVersion(context.Background(), cfg); v != "" {
		t.Fatalf("version through a redirect: %q", v)
	}
	if leaked != "" {
		t.Fatalf("the token followed a redirect: %q", leaked)
	}
}
