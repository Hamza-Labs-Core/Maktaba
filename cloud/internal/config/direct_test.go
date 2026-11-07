package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Story 50.15: the direct-name domains are configuration (a fifth is a line in the file), the
// CAs' sections, and the EAB secrets from the environment only.
func TestDirectConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cloud.toml")
	err := os.WriteFile(p, []byte(`
[direct]
enabled = true
domains = ["Direct-A.example", "direct-b.example", "direct-c.example"]
ns1 = "203.0.113.11"
ns2 = "203.0.113.12"
check_every = "10m"

[direct.acme.letsencrypt]
directory = "https://acme-v02.api.letsencrypt.org/directory"
account_key = "/etc/maktaba/acme-letsencrypt.key"

[direct.acme.zerossl]
directory = "https://acme.zerossl.com/v2/DV90"
account_key = "/etc/maktaba/acme-zerossl.key"
eab_kid = "kid-1"
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_CLOUD_DIRECT_ZEROSSL_EAB_HMAC", "c2VjcmV0")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	d := c.Direct
	if !d.Enabled || len(d.Domains) != 3 || d.Domains[0] != "direct-a.example" || d.NS2 != "203.0.113.12" || d.CheckEvery != 10*time.Minute {
		t.Fatalf("%+v", d)
	}
	if d.Budget != 45 || d.DNSListen != ":53" {
		t.Fatalf("defaults: %+v", d)
	}
	z := d.ACME["zerossl"]
	if z.EABKeyID != "kid-1" || z.EABHMAC != "c2VjcmV0" || d.ACME["letsencrypt"].Directory == "" {
		t.Fatalf("acme: %+v", d.ACME)
	}
	if got := parseList(`direct-a.example", "direct-b.example`); len(got) != 2 || got[1] != "direct-b.example" {
		t.Fatalf("the parser's trimmed list: %v", got)
	}
}

// Story 50.15: `serve --role dns` is a role Validate knows (it refused it as unknown, so the
// name server never started), and it needs the domains and both name servers' addresses.
func TestValidateTheDNSRole(t *testing.T) {
	c := defaultConfig()
	c.Database.URL = "postgres://x"
	if err := Validate(c, RoleDNS); err == nil {
		t.Fatal("no domains passed")
	}
	c.Direct.Domains = []string{"direct-a.example"}
	c.Direct.NS1, c.Direct.NS2 = "203.0.113.11", "ns2.example"
	if err := Validate(c, RoleDNS); err == nil || !strings.Contains(err.Error(), "ns2") {
		t.Fatalf("a name for ns2: %v", err)
	}
	c.Direct.NS2 = "203.0.113.12"
	if err := Validate(c, Role("dns")); err != nil {
		t.Fatal(err)
	}
}
