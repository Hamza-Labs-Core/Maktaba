package main

import (
	"bytes"
	"context"
	"net"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// takenProbe: the listed ports are held by another program.
func takenProbe(taken ...string) func() config.PortProbe {
	return func() config.PortProbe {
		return config.PortProbe{
			Free: func(addr string) bool {
				_, p, _ := net.SplitHostPort(addr)
				for _, t := range taken {
					if t == p {
						return false
					}
				}
				return true
			},
			Ours: func(int) bool { return false },
		}
	}
}

func withProbe(t *testing.T, p func() config.PortProbe) {
	oldWait := servePortWait
	servePortWait = 0
	t.Cleanup(func() { servePortWait = oldWait })
	t.Helper()
	old := portProbe
	portProbe = p
	t.Cleanup(func() { portProbe = old })
}

// setup --node on a PC where Splunk holds 8088 and another program 8080: the node's
// server.toml gets free ports from the fallback range, and the setup says so.
func TestSetupNodeMovesTakenPorts(t *testing.T) {
	withProbe(t, takenProbe("8088", "8080"))
	ss, _, from := fakeRelease(t)
	rec := &recorder{}
	s, _, _ := newTestSetup(t, ss, from, rec)
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("setup --node: %v\n%s", err, s.Out)
	}
	cfg, found, err := config.LoadFrom(s.ConfigPath)
	if err != nil || !found {
		t.Fatalf("server.toml: %v %v", found, err)
	}
	if cfg.Server.WebListen != "0.0.0.0:8780" || cfg.Server.Listen != "0.0.0.0:8782" || cfg.Server.StreamAddr != ":8081" {
		t.Fatalf("ports not moved: %+v", cfg.Server)
	}
	out := s.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "web_listen 0.0.0.0:8088 -> 0.0.0.0:8780") || !strings.Contains(out, "listen 0.0.0.0:8080 -> 0.0.0.0:8782") {
		t.Fatalf("the moves are not logged:\n%s", out)
	}
	if !strings.Contains(out, ":8780") {
		t.Fatalf("the final line names the web address:\n%s", out)
	}
}

// On a Windows full node the web port prefers 8780 even when 8088 is free.
func TestSetupNodePrefersWindowsWebPort(t *testing.T) {
	ss, _, from := fakeRelease(t)
	rec := &recorder{}
	s, _, _ := newTestSetup(t, ss, from, rec)
	if err := s.run(context.Background()); err != nil {
		t.Fatalf("setup --node: %v\n%s", err, s.Out)
	}
	cfg, _, _ := config.LoadFrom(s.ConfigPath)
	want := "0.0.0.0:8088"
	if runtime.GOOS == "windows" {
		want = "0.0.0.0:8780"
	}
	if cfg.Server.WebListen != want {
		t.Fatalf("web_listen = %q, want %q", cfg.Server.WebListen, want)
	}
}

func TestServePortsMovesAndSaves(t *testing.T) {
	withProbe(t, takenProbe("8081"))
	path := t.TempDir() + "/server.toml"
	t.Setenv("MAKTABA_CONFIG", path)
	cfg := config.Defaults()
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	if err := servePorts(&cfg, true, "", false); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.StreamAddr != ":8781" {
		t.Fatalf("stream_addr = %q", cfg.Server.StreamAddr)
	}
	saved, _, _ := config.LoadFrom(path)
	if saved.Server.StreamAddr != ":8781" {
		t.Fatalf("not saved: %q", saved.Server.StreamAddr)
	}
}

// A single role never moves a port: a taken one is an error naming it.
func TestServePortsSingleRoleFails(t *testing.T) {
	withProbe(t, takenProbe("8080"))
	t.Setenv("MAKTABA_CONFIG", t.TempDir()+"/server.toml")
	cfg := config.Defaults()
	err := servePorts(&cfg, true, "api", false)
	if err == nil || !strings.Contains(err.Error(), "listen 0.0.0.0:8080") {
		t.Fatalf("err = %v", err)
	}
	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Fatal("a single role moved its port")
	}
	if err := servePorts(&cfg, true, "pipeline", false); err != nil {
		t.Fatalf("pipeline has no public port: %v", err)
	}
}

// The web server binds before serve goes on: a taken web port is an error.
func TestStartWebServerBindFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer func() { _ = ln.Close() }()
	cfg := config.Defaults()
	cfg.Server.WebListen = ln.Addr().String()
	srv, err := startWebServer(cfg, nil)
	if err == nil {
		_ = srv.Close()
		t.Fatal("startWebServer on a taken port succeeded")
	}
}
