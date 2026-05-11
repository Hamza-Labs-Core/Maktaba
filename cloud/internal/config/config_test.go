package config

import (
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

func TestValidate_RoleAPI(t *testing.T) {
	c := Config{}
	if err := Validate(c, RoleAPI); err == nil {
		t.Error("Validate empty/api should fail")
	}
	c.Database.URL = "postgres://x"
	c.Server.ListenAddr = ":8080"
	c.OAuthGoogle.ClientID = "g"
	c.Admin.AllowedEmailDomain = "x.com"
	if err := Validate(c, RoleAPI); err != nil {
		t.Errorf("Validate(api) ok config: %v", err)
	}
}

func TestValidate_RoleWorkerLooser(t *testing.T) {
	c := Config{}
	c.Database.URL = "postgres://x"
	if err := Validate(c, RoleWorker); err != nil {
		t.Errorf("Validate(worker) only needs DB: %v", err)
	}
}
