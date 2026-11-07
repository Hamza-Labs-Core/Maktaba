package config

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTOML(t *testing.T, body string) (Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cloud.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func prodAPI() Config {
	c, _ := Load("")
	c.Database.URL = "postgres://x"
	c.Server.Env = "production"
	c.Server.ListenAddr = "127.0.0.1:8090"
	c.Server.AdminListenAddr = "127.0.0.1:8091"
	c.Server.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	return c
}

func TestTrustedProxiesParse(t *testing.T) {
	c, err := loadTOML(t, `
[server]
env = "production"
listen_addr = "127.0.0.1:8090"
admin_listen_addr = "127.0.0.1:8091"
trusted_proxies = ["127.0.0.1/32", "::1/128"]
origin_mode = "tunnel"
`)
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	if len(c.Server.TrustedProxies) != 2 || c.Server.TrustedProxies[0] != want[0] || c.Server.TrustedProxies[1] != want[1] {
		t.Fatalf("trusted_proxies = %v", c.Server.TrustedProxies)
	}
	if !c.Server.Production() || c.Server.AdminListenAddr != "127.0.0.1:8091" || c.Server.OriginMode != OriginTunnel {
		t.Fatalf("server = %+v", c.Server)
	}
	c.Database.URL = "postgres://x"
	if err := Validate(c, RoleAPI); err != nil {
		t.Fatalf("a good production config: %v", err)
	}
	if _, err := loadTOML(t, "[server]\ntrusted_proxies = [\"127.0.0.1\"]\n"); err == nil ||
		!strings.Contains(err.Error(), "server.trusted_proxies") {
		t.Fatalf("a bare address: %v", err)
	}
	d, _ := Load("")
	if d.Server.OriginMode != OriginTunnel || len(d.Server.TrustedProxies) != 0 || d.Server.Production() {
		t.Fatalf("defaults: %+v", d.Server)
	}
}

// T02 (Epic 50 story 50.31): each refusal names its setting.
func TestOriginRefusals(t *testing.T) {
	_, err := loadTOML(t, "[server]\ntrust_proxy_headers = true\n")
	if !errors.Is(err, ErrTrustProxyHeaders) || !strings.Contains(err.Error(), "server.trusted_proxies") {
		t.Fatalf("trust_proxy_headers: %v", err)
	}
	// even false: the switch is gone, so a file still naming it is out of date
	if _, err := loadTOML(t, "[server]\ntrust_proxy_headers = false\n"); !errors.Is(err, ErrTrustProxyHeaders) {
		t.Fatalf("trust_proxy_headers = false: %v", err)
	}

	for _, c := range []struct {
		name, key string
		edit      func(*Config)
	}{
		{"wildcard listener", "server.listen_addr", func(c *Config) { c.Server.ListenAddr = "0.0.0.0:8090" }},
		{"empty host", "server.listen_addr", func(c *Config) { c.Server.ListenAddr = ":8090" }},
		{"public listener", "server.listen_addr", func(c *Config) { c.Server.ListenAddr = "203.0.113.5:8090" }},
		{"a name", "server.listen_addr", func(c *Config) { c.Server.ListenAddr = "localhost:8090" }},
		{"public admin listener", "server.admin_listen_addr", func(c *Config) { c.Server.AdminListenAddr = "[::]:8091" }},
		{"no trusted proxies", "server.trusted_proxies", func(c *Config) { c.Server.TrustedProxies = nil }},
		{"unknown mode", "server.origin_mode", func(c *Config) { c.Server.OriginMode = "open" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := prodAPI()
			c.edit(&cfg)
			err := Validate(cfg, RoleAPI)
			if err == nil || !strings.Contains(err.Error(), c.key) {
				t.Fatalf("want a refusal naming %s, got %v", c.key, err)
			}
		})
	}

	// a private address is allowed (Phase 2's load balancer reaches it)
	cfg := prodAPI()
	cfg.Server.ListenAddr = "10.0.0.2:8090"
	if err := Validate(cfg, RoleAPI); err != nil {
		t.Fatalf("private listener: %v", err)
	}
	// outside production nothing is asked: dev and CI listen where they like, with no proxies
	dev := prodAPI()
	dev.Server.Env = ""
	dev.Server.ListenAddr = "0.0.0.0:8080"
	dev.Server.TrustedProxies = nil
	if err := Validate(dev, RoleAPI); err != nil {
		t.Fatalf("dev: %v", err)
	}
}

// T09: the fallback's CA.
func TestOriginModeAOP(t *testing.T) {
	cfg := prodAPI()
	cfg.Server.OriginMode = OriginAOP
	if err := Validate(cfg, RoleAPI); err == nil || !strings.Contains(err.Error(), "server.aop_ca_file") {
		t.Fatalf("aop without its CA: %v", err)
	}
	cfg.Server.AOPCAFile = filepath.Join(t.TempDir(), "missing.pem")
	if err := Validate(cfg, RoleAPI); err == nil || !strings.Contains(err.Error(), "server.aop_ca_file") {
		t.Fatalf("aop with a missing CA: %v", err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("test CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Server.AOPCAFile = ca
	if err := Validate(cfg, RoleAPI); err != nil {
		t.Fatalf("aop with its CA: %v", err)
	}
	if w := Warnings(cfg); len(w) != 0 {
		t.Fatalf("aop warnings: %v", w)
	}
	cfg.Server.OriginMode = OriginTunnel
	if w := Warnings(cfg); len(w) != 1 || !strings.Contains(w[0], "server.aop_ca_file") {
		t.Fatalf("tunnel with a CA set: %v", w)
	}
}
