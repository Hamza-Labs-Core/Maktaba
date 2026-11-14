package config

import (
	"strings"
	"testing"
)

// Story 50.28: the private series' listener parses from the file and the environment, and is a
// loopback or private address in every environment, never a wildcard, a name, a public address
// or another listener's.
func TestMetricsListenerSettings(t *testing.T) {
	c, err := loadTOML(t, `
[server]
listen_addr = "127.0.0.1:8090"
metrics_listen_addr = "127.0.0.1:8092"
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.MetricsListenAddr != "127.0.0.1:8092" {
		t.Fatalf("metrics_listen_addr = %q", c.Server.MetricsListenAddr)
	}
	t.Setenv("MAKTABA_CLOUD_METRICS_ADDR", "127.0.0.1:8445")
	c, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.MetricsListenAddr != "127.0.0.1:8445" {
		t.Fatalf("MAKTABA_CLOUD_METRICS_ADDR: %q", c.Server.MetricsListenAddr)
	}
}

func TestMetricsListenerValidation(t *testing.T) {
	base := func() Config {
		c, _ := Load("")
		c.Database.URL = "postgres://x"
		c.Server.ListenAddr = "127.0.0.1:8090"
		c.Relay.PublicHost = "relay.maktaba.tv"
		return c
	}
	for _, tc := range []struct {
		name string
		addr string
		want string // "" = valid
	}{
		{"none", "", ""},
		{"loopback", "127.0.0.1:8445", ""},
		{"the node's private address", "10.50.0.2:8445", ""},
		{"ephemeral (tests)", "127.0.0.1:0", ""},
		{"a wildcard", "0.0.0.0:8445", "loopback or private"},
		{"the IPv6 wildcard", "[::]:8445", "loopback or private"},
		{"a public address", "203.0.113.7:8445", "loopback or private"},
		{"a name", "metrics.maktaba.tv:8445", "loopback or private"},
		{"no port", "127.0.0.1", "server.metrics_listen_addr"},
		{"the main listener's", "127.0.0.1:8090", "need their own"},
	} {
		for _, role := range []Role{RoleAPI, RoleRelay} {
			c := base()
			c.Server.MetricsListenAddr = tc.addr
			err := Validate(c, role)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("%s (%s): %v", tc.name, role, err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("%s (%s): %v, want an error naming %q", tc.name, role, err, tc.want)
			}
		}
	}
	// the console's listener is never the private series' either
	c := base()
	c.Server.AdminListenAddr, c.Server.AdminHost, c.Server.MetricsListenAddr = "127.0.0.1:8091", "admin.maktaba.tv", "127.0.0.1:8091"
	if err := Validate(c, RoleAPI); err == nil || !strings.Contains(err.Error(), "need their own") {
		t.Errorf("the admin listener's address: %v", err)
	}
}
