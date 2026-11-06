package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if c.Database.MaxOpenConns != 25 {
		t.Errorf("default MaxOpenConns = %d, want 25", c.Database.MaxOpenConns)
	}
	if c.Telemetry.Sample2xxRate != 0.10 {
		t.Errorf("default sample rate = %v, want 0.10", c.Telemetry.Sample2xxRate)
	}
}

func TestLoad_TOML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cloud.toml")
	if err := os.WriteFile(p, []byte(`
[server]
listen_addr = "127.0.0.1:1234"
read_timeout = "5s"

[database]
url = "postgres://x"
max_open_conns = 7
`), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Server.ListenAddr != "127.0.0.1:1234" {
		t.Errorf("listen_addr = %q", c.Server.ListenAddr)
	}
	if c.Database.MaxOpenConns != 7 {
		t.Errorf("max_open_conns = %d", c.Database.MaxOpenConns)
	}
	if c.Server.ReadTimeout.Seconds() != 5 {
		t.Errorf("read_timeout = %v", c.Server.ReadTimeout)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("MAKTABA_CLOUD_DB_URL", "postgres://from-env")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Database.URL != "postgres://from-env" {
		t.Errorf("env override not applied: %q", c.Database.URL)
	}
}

func TestLoad_TokenSecretEnvOverride(t *testing.T) {
	// MAKTABA_CLOUD_TOKEN_SECRET is env-only (no TOML key) — config is
	// the single source of truth the api role reads from.
	t.Setenv("MAKTABA_CLOUD_TOKEN_SECRET", "this-is-at-least-32-bytes-long!!")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth.TokenSecret != "this-is-at-least-32-bytes-long!!" {
		t.Errorf("token secret env override not applied: %q", c.Auth.TokenSecret)
	}
}

func TestValidate_RoleAPI(t *testing.T) {
	c := Config{}
	if err := Validate(c, RoleAPI); err == nil {
		t.Error("Validate empty/api should fail")
	}
	c.Database.URL = "postgres://x"
	c.Server.ListenAddr = ":8080"
	c.Mail.Driver = "log"
	if err := Validate(c, RoleAPI); err != nil {
		t.Errorf("Validate(api) ok config: %v", err)
	}
}

// The payment provider is not chosen yet: [billing].provider defaults
// to "none", and the api role refuses any provider it does not ship.
func TestValidate_BillingProvider(t *testing.T) {
	c := Config{}
	c.Database.URL = "postgres://x"
	c.Server.ListenAddr = ":8080"
	c.Mail.Driver = "log"
	for _, tc := range []struct {
		provider string
		ok       bool
	}{
		{"", true},
		{"none", true},
		{"None", false},
		{"manual", false},
		{"some-provider", false},
	} {
		c.Billing.Provider = tc.provider
		err := Validate(c, RoleAPI)
		if (err == nil) != tc.ok {
			t.Errorf("provider=%q: err=%v, want ok=%v", tc.provider, err, tc.ok)
		}
		if !tc.ok && !errors.Is(err, ErrUnknownBillingProvider) {
			t.Errorf("provider=%q: err=%v, want ErrUnknownBillingProvider", tc.provider, err)
		}
	}
}

func TestLoad_BillingProvider(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Billing.Provider != "none" {
		t.Errorf("default provider = %q, want none", c.Billing.Provider)
	}
	p := filepath.Join(t.TempDir(), "cloud.toml")
	if err := os.WriteFile(p, []byte("[billing]\nprovider = \"none\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(p); err != nil || c.Billing.Provider != "none" {
		t.Errorf("toml provider: %q %v", c.Billing.Provider, err)
	}
	t.Setenv("MAKTABA_CLOUD_BILLING_PROVIDER", "elsewhere")
	if c, err = Load(p); err != nil || c.Billing.Provider != "elsewhere" {
		t.Errorf("env provider: %q %v", c.Billing.Provider, err)
	}
	c.Database.URL = "postgres://x"
	if err := Validate(c, RoleAPI); !errors.Is(err, ErrUnknownBillingProvider) {
		t.Errorf("an unknown provider from env: %v", err)
	}
}

func TestValidate_RoleWorkerLooser(t *testing.T) {
	c := Config{}
	c.Database.URL = "postgres://x"
	if err := Validate(c, RoleWorker); err != nil {
		t.Errorf("Validate(worker) only needs DB: %v", err)
	}
}

// Story 50.03: the relay's handover switches and timings, with their defaults.
func TestLoad_RelayDrain(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	r := c.Relay
	if !r.DrainEnabled || !r.TicketsEnabled || r.DrainWithin.Seconds() != 5 || r.DrainDeadline.Seconds() != 120 || r.DrainRate != 100 || r.ShutdownGrace.Seconds() != 150 {
		t.Errorf("defaults: %+v", r)
	}
	p := filepath.Join(t.TempDir(), "cloud.toml")
	if err := os.WriteFile(p, []byte(`
[relay]
shutdown_grace = "30s"

[relay.drain]
enabled = false
within = "2s"
deadline = "10s"
rate = 50

[relay.ticket]
enabled = false
`), 0644); err != nil {
		t.Fatal(err)
	}
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	r = c.Relay
	if r.DrainEnabled || r.TicketsEnabled || r.DrainWithin.Seconds() != 2 || r.DrainDeadline.Seconds() != 10 || r.DrainRate != 50 || r.ShutdownGrace.Seconds() != 30 {
		t.Errorf("from the file: %+v", r)
	}
}
