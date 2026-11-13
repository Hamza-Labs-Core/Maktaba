package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// Epic 59 story 59.08: a port the container sets (MAKTABA_WEB_PORT) that another program holds
// is not moved; the start stops, its log naming the port, the holder and the variable in
// English and Arabic, and server.toml is left as it was.
func TestServePinnedPortTaken(t *testing.T) {
	withProbe(t, takenProbe("32400"))
	oldHolder := portHolder
	t.Cleanup(func() { portHolder = oldHolder })
	portHolder = func(int) string { return "Plex Media Server" }
	for _, pe := range config.PortEnvs {
		t.Setenv(pe.Env, "")
	}
	path := filepath.Join(t.TempDir(), "server.toml")
	t.Setenv("MAKTABA_CONFIG", path)
	if err := config.Defaults().SaveTo(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	t.Setenv(config.EnvWebPort, "32400")
	cfg, _, err := config.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err = reconcilePorts(&cfg, path, config.PortOptions{}, &out, true)
	var pinned *config.ErrPortPinned
	if !errors.As(err, &pinned) || pinned.Holder != "Plex Media Server" {
		t.Fatalf("err = %v", err)
	}
	log := out.String()
	for _, want := range []string{
		"Port 32400 is used by Plex Media Server on this machine, so Maktaba cannot use it. Set MAKTABA_WEB_PORT to a free port",
		"المنفذ 32400 يستخدمه Plex Media Server على هذا الجهاز", "اضبط MAKTABA_WEB_PORT على منفذ متاح",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if cfg.Server.WebListen != "0.0.0.0:32400" {
		t.Fatalf("moved: %q", cfg.Server.WebListen)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatalf("server.toml changed:\n%s", after)
	}
}

// The bundled Postgres's port held by another program (no postmaster.pid of this home's):
// both languages, the variable named; in a container, or when the variable set the port, the
// start stops; elsewhere it goes on (the supervisor holds the role until the port is free).
func TestPostgresPortCheck(t *testing.T) {
	oldFree, oldHolder := pgPortFree, portHolder
	t.Cleanup(func() { pgPortFree, portHolder = oldFree, oldHolder })
	free := false
	pgPortFree = func(string) bool { return free }
	portHolder = func(int) string { return "" }
	b := pgbundle.Bundle{Data: t.TempDir(), Port: 54329}

	t.Setenv(config.EnvContainer, "")
	var out bytes.Buffer
	if err := postgresPortCheck(b, config.Defaults(), &out); err != nil {
		t.Fatalf("outside a container: %v", err)
	}
	for _, want := range []string{"Port 54329 is used by another program", "MAKTABA_POSTGRES_PORT / server.toml [postgres] port",
		"المنفذ 54329 يستخدمه برنامج آخر"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("log lacks %q: %s", want, out.String())
		}
	}

	t.Setenv(config.EnvContainer, config.ContainerDocker)
	if err := postgresPortCheck(b, config.Defaults(), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "MAKTABA_POSTGRES_PORT") {
		t.Fatalf("in a container: %v", err)
	}
	t.Setenv(config.EnvContainer, "")
	pinned := config.Defaults()
	pinned.PortPins = map[string]string{config.PinPostgres: config.EnvPostgresPort}
	out.Reset()
	if err := postgresPortCheck(b, pinned, &out); err == nil || strings.Contains(out.String(), "server.toml") {
		t.Fatalf("pinned: %v %s", err, out.String())
	}
	// this home's own Postgres may still be exiting: left to the supervisor
	if err := os.WriteFile(filepath.Join(b.Data, "postmaster.pid"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := postgresPortCheck(b, pinned, &bytes.Buffer{}); err != nil {
		t.Fatalf("postmaster.pid: %v", err)
	}
	free = true
	_ = os.Remove(filepath.Join(b.Data, "postmaster.pid"))
	if err := postgresPortCheck(b, pinned, &bytes.Buffer{}); err != nil {
		t.Fatalf("free: %v", err)
	}
}

// A node asks its home for the home's live TV port and reaches the TV front door there; a home
// that does not say leaves 8081.
func TestLearnHomeStreamPort(t *testing.T) {
	t.Setenv("MAKTABA_HOME_STREAM_URL", "")
	t.Cleanup(func() { supervisor.SetHomeStreamPort(0) })
	port := 18081
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/discovery/home" {
			http.NotFound(w, r)
			return
		}
		if port == 0 {
			_, _ = w.Write([]byte(`{"id":"x","urls":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x","urls":[],"stream_port":18081}`))
	}))
	defer srv.Close()
	n := config.NodeConfig{Home: config.HomeRef{APIURL: srv.URL}}
	learnHomeStreamPort(context.Background(), n, srv.Client())
	want := strings.Replace(srv.URL, srv.URL[strings.LastIndex(srv.URL, ":"):], ":18081", 1)
	if got := supervisor.HomeStreamURL(n); got != want {
		t.Fatalf("home stream url %q, want %q", got, want)
	}
	supervisor.SetHomeStreamPort(0)
	port = 0
	learnHomeStreamPort(context.Background(), n, srv.Client())
	if got := supervisor.HomeStreamURL(n); !strings.HasSuffix(got, ":8081") {
		t.Fatalf("home that does not say: %q", got)
	}
}
