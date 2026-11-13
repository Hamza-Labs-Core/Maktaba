package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// clearPortEnv empties every port variable for the test (the machine running it may set one).
func clearPortEnv(t *testing.T) {
	t.Helper()
	for _, pe := range PortEnvs {
		t.Setenv(pe.Env, "")
	}
}

// Epic 59 story 59.08: each port variable sets its port over server.toml, keeping the bind's
// host; an empty one is not set.
func TestPortEnvOverridesTheFile(t *testing.T) {
	clearPortEnv(t)
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(path, []byte(`[server]
listen = "0.0.0.0:8080"
web_listen = "127.0.0.1:8088"
stream_addr = ":8081"
[postgres]
port = 54329
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvWebPort, "18088")
	t.Setenv(EnvAPIPort, " 18080 ")
	t.Setenv(EnvStreamPort, "18081")
	t.Setenv(EnvPostgresPort, "55439")
	t.Setenv(EnvPublicWebPort, "9088")
	t.Setenv(EnvPublicAPIPort, "") // Unraid's empty variable: not set
	cfg, found, err := LoadFrom(path)
	if err != nil || !found {
		t.Fatalf("load: %v %v", found, err)
	}
	if cfg.Server.WebListen != "127.0.0.1:18088" || cfg.Server.Listen != "0.0.0.0:18080" ||
		cfg.Server.StreamAddr != ":18081" || cfg.Postgres.Port != 55439 || cfg.Server.PublicWebPort != 9088 ||
		cfg.Server.PublicAPIPort != 0 {
		t.Fatalf("server %+v postgres %d", cfg.Server, cfg.Postgres.Port)
	}
	for key, env := range map[string]string{PinWeb: EnvWebPort, PinAPI: EnvAPIPort, PinStream: EnvStreamPort,
		PinPostgres: EnvPostgresPort, PinPublicWeb: EnvPublicWebPort} {
		if cfg.PinnedBy(key) != env {
			t.Errorf("%s pinned by %q, want %s", key, cfg.PinnedBy(key), env)
		}
	}
	if cfg.PinnedBy(PinPublicAPI) != "" || len(cfg.PortPins) != 5 {
		t.Errorf("pins %v", cfg.PortPins)
	}
}

// Before setup wrote server.toml (a container waiting for it) the variables hold too.
func TestPortEnvWithoutAFile(t *testing.T) {
	clearPortEnv(t)
	t.Setenv(EnvAPIPort, "18080")
	cfg, found, err := LoadFrom(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || found {
		t.Fatalf("load: %v %v", found, err)
	}
	if cfg.Server.Listen != "0.0.0.0:18080" || cfg.PublicAPIPort() != 18080 {
		t.Fatalf("listen %q", cfg.Server.Listen)
	}
}

// No variable: the config is exactly the file's (nothing pinned).
func TestPortEnvNone(t *testing.T) {
	clearPortEnv(t)
	cfg, _, err := LoadFrom(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || cfg.PortPins != nil || cfg.Server.WebListen != Defaults().Server.WebListen {
		t.Fatalf("%v %v %q", err, cfg.PortPins, cfg.Server.WebListen)
	}
}

// A value that is not a port stops the load, naming the variable and the value in English
// and Arabic.
func TestPortEnvBadValue(t *testing.T) {
	for _, v := range []string{"abc", "0", "65536", "-1", "80a"} {
		clearPortEnv(t)
		t.Setenv(EnvWebPort, v)
		_, _, err := LoadFrom(filepath.Join(t.TempDir(), "none.toml"))
		var pe PortEnvError
		if !errors.As(err, &pe) || pe.Env != EnvWebPort || pe.Value != v {
			t.Fatalf("%q: %v", v, err)
		}
		en, ar := pe.Texts()
		if !strings.Contains(en, "MAKTABA_WEB_PORT") || !strings.Contains(en, "1 to 65535") ||
			!strings.Contains(ar, "MAKTABA_WEB_PORT") || !strings.Contains(ar, "ليست منفذًا") {
			t.Fatalf("%q: %q / %q", v, en, ar)
		}
	}
}

// A save keeps server.toml's own value of every port a variable sets: removing the variable
// goes back to it.
func TestPortEnvNeverSaved(t *testing.T) {
	clearPortEnv(t)
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(path, []byte("[server]\nweb_listen = \"0.0.0.0:8088\"\n[postgres]\nport = 54329\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvWebPort, "18088")
	t.Setenv(EnvPostgresPort, "55439")
	t.Setenv(EnvPublicStreamPort, "30081")
	cfg, _, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.MDNS = "off" // a change of something else
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	var file struct {
		Server struct {
			WebListen        string `toml:"web_listen"`
			MDNS             string `toml:"mdns"`
			PublicStreamPort int    `toml:"public_stream_port"`
		} `toml:"server"`
		Postgres struct {
			Port int `toml:"port"`
		} `toml:"postgres"`
	}
	if _, err := toml.DecodeFile(path, &file); err != nil {
		t.Fatal(err)
	}
	if file.Server.WebListen != "0.0.0.0:8088" || file.Postgres.Port != 54329 || file.Server.PublicStreamPort != 0 ||
		file.Server.MDNS != "off" {
		t.Fatalf("server.toml %+v", file)
	}
	// the variable gone, the file's port is back
	clearPortEnv(t)
	cfg, _, _ = LoadFrom(path)
	if cfg.Server.WebListen != "0.0.0.0:8088" || cfg.Postgres.Port != 54329 {
		t.Fatalf("after: %q %d", cfg.Server.WebListen, cfg.Postgres.Port)
	}
}

// The addresses Maktaba hands out use the published ports when they are set (a bridge's
// "9088:8088"), else the ports it listens on.
func TestPublicPorts(t *testing.T) {
	old := lanIP
	t.Cleanup(func() { lanIP = old })
	lanIP = func() string { return "192.168.1.20" }
	c := Defaults()
	c.Server.WebListen, c.Server.Listen, c.Server.StreamAddr = "0.0.0.0:18088", "0.0.0.0:18080", ":18081"
	if c.PublicWebURL() != "http://192.168.1.20:18088" || c.PublicAPIURL() != "http://192.168.1.20:18080" ||
		c.PublicStreamURL() != "http://192.168.1.20:18081" {
		t.Fatalf("listen ports: %s %s %s", c.PublicWebURL(), c.PublicAPIURL(), c.PublicStreamURL())
	}
	c.Server.PublicWebPort, c.Server.PublicAPIPort, c.Server.PublicStreamPort = 9088, 9080, 9081
	if c.PublicWebURL() != "http://192.168.1.20:9088" || c.PublicAPIURL() != "http://192.168.1.20:9080" ||
		c.PublicStreamURL() != "http://192.168.1.20:9081" || c.PublicWebPort() != 9088 || c.PublicAPIPort() != 9080 ||
		c.PublicStreamPort() != 9081 {
		t.Fatalf("published ports: %s %s %s", c.PublicWebURL(), c.PublicAPIURL(), c.PublicStreamURL())
	}
	// an explicit public URL still wins
	c.Server.WebPublicURL = "https://maktaba.example"
	if c.PublicWebURL() != "https://maktaba.example" {
		t.Fatalf("web_public_url: %s", c.PublicWebURL())
	}
}

// A taken port the environment sets is not moved: ReconcilePorts stops on it with
// ErrPortPinned; a taken default port beside it still moves.
func TestReconcilePinnedPortTaken(t *testing.T) {
	c := Defaults()
	c.Server.WebListen = "0.0.0.0:18088"
	c.PortPins = map[string]string{PinWeb: EnvWebPort}
	changes, err := c.ReconcilePorts(fakeProbe([]int{18088}), PortOptions{})
	var pinned *ErrPortPinned
	if !errors.As(err, &pinned) || pinned.Port != 18088 || pinned.Env != EnvWebPort || pinned.Key != "web_listen" {
		t.Fatalf("err %v", err)
	}
	if len(changes) != 0 || c.Server.WebListen != "0.0.0.0:18088" {
		t.Fatalf("moved: %v %q", changes, c.Server.WebListen)
	}
	if !strings.Contains(err.Error(), "MAKTABA_WEB_PORT") {
		t.Fatalf("message: %v", err)
	}

	// the default API port taken is moved as before; the pinned web port, free, stays
	c = Defaults()
	c.Server.WebListen = "0.0.0.0:18088"
	c.PortPins = map[string]string{PinWeb: EnvWebPort}
	changes, err = c.ReconcilePorts(fakeProbe([]int{8080}), PortOptions{PreferWeb: 8780})
	if err != nil || len(changes) != 1 || changes[0].Key != "listen" || c.Server.WebListen != "0.0.0.0:18088" {
		t.Fatalf("changes %v err %v web %q", changes, err, c.Server.WebListen)
	}
}

func TestWithPort(t *testing.T) {
	for _, tc := range []struct {
		addr, def string
		port      int
		want      string
	}{
		{"0.0.0.0:8088", ":1", 9088, "0.0.0.0:9088"},
		{":8081", ":1", 9081, ":9081"},
		{"[::]:8080", ":1", 9080, "[::]:9080"},
		{"", "0.0.0.0:8088", 9088, "0.0.0.0:9088"},
		{"garbage", "0.0.0.0:8088", 9088, "0.0.0.0:9088"},
	} {
		if got := WithPort(tc.addr, tc.def, tc.port); got != tc.want {
			t.Errorf("WithPort(%q, %q, %d) = %q, want %q", tc.addr, tc.def, tc.port, got, tc.want)
		}
	}
}
